package systemtest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

// The native route is selected by an engine-reserved rune; the name is
// otherwise ordinary and materializes on every supported host.
const downloadCancelNative = "scope/n／native.bin"

// downloadCancelEngineHeld sorts after the other engine files of its case,
// so the engine has written them whole before its read of this one is held.
const downloadCancelEngineHeld = "scope/z-held.bin"

func downloadCancelLarge() []byte {
	body := make([]byte, 256*1024)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	return body
}

// downloadCancelAwaitEngine waits until the engine has the given files whole
// at their names and a partial file of its own for the held one, which is
// the moment a cancel interrupts it while it writes.
func downloadCancelAwaitEngine(t *testing.T, root string, whole map[string][]byte) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		done, partial := 0, false
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".partial") {
				partial = true
			}
			if body, ok := whole[entry.Name()]; ok {
				if data, err := os.ReadFile(filepath.Join(root, entry.Name())); err == nil && bytes.Equal(data, body) {
					done++
				}
			}
		}
		if done == len(whole) && partial {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the engine did not reach the held file with the others written: entries=%v", entries)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type downloadCancelHold struct {
	after   int64
	reached chan struct{}
	release chan struct{}
	once    sync.Once
	// mu guards sent, which the reader records and a test reads. The barrier
	// is the only party that knows how much it released, and a test that wants
	// to assert the release was exact has to ask here rather than infer it from
	// bytes on a disk it does not control the flushing of.
	mu   sync.Mutex
	sent int64
}

// released is how many body bytes the barrier has handed downstream.
func (h *downloadCancelHold) released() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sent
}

type downloadCancelReader struct {
	io.ReadCloser
	ctx  context.Context
	hold *downloadCancelHold
	sent int64
}

func (r *downloadCancelReader) Read(p []byte) (int, error) {
	if r.sent >= r.hold.after {
		r.hold.once.Do(func() { close(r.hold.reached) })
		select {
		case <-r.hold.release:
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
	}
	if remaining := r.hold.after - r.sent; remaining > 0 && int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := r.ReadCloser.Read(p)
	r.sent += int64(n)
	r.hold.mu.Lock()
	r.hold.sent = r.sent
	r.hold.mu.Unlock()
	return n, err
}

// downloadCancelBodyProxy fronts the primary account and holds the body of
// the selected GET once `after` bytes have been passed to the client, so
// a cancel can land while the executor is reading the object body.
func downloadCancelBodyProxy(t *testing.T, h *Harness, container, name string, after int64) (*downloadCancelHold, *uploadDeleteTrace) {
	t.Helper()
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	selected := (&url.URL{Path: storage.Path + "/" + container + "/" + name}).EscapedPath()
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	proxy.FlushInterval = -1
	hold := &downloadCancelHold{after: after, reached: make(chan struct{}), release: make(chan struct{})}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.Request.Method == http.MethodGet && resp.Request.URL.EscapedPath() == selected {
			resp.Body = &downloadCancelReader{ReadCloser: resp.Body, ctx: resp.Request.Context(), hold: hold}
		}
		return nil
	}
	trace := &uploadDeleteTrace{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.mu.Lock()
		trace.requests++
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			trace.writes = append(trace.writes, r.Method+" "+r.URL.Path)
		}
		trace.mu.Unlock()
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	t.Cleanup(func() {
		hold.once.Do(func() { close(hold.reached) })
		select {
		case <-hold.release:
		default:
			close(hold.release)
		}
	})
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range bookmarks {
		if bookmark.ID != h.Primary.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, h.Primary.key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	return hold, trace
}

func downloadCancelStart(t *testing.T, h *Harness, container, prefix, root string) string {
	t.Helper()
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: root})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.Transfer.StartDownload(t.Context(), app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: prefix, DestDirToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type downloadCancelWant struct {
	execution    *jobs.ItemObservation
	verification *jobs.ItemObservation
}

var (
	downloadCancelNotAttempted = &jobs.ItemObservation{Result: jobs.ItemNotAttempted, Effect: jobs.EffectNotAttempted, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeCancelled}}
	downloadCancelUnknown      = &jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeCancelled}}
	downloadCancelNativeRead   = &jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageSourceRead, Code: jobs.CodeCancelled}}
	downloadCancelVerified     = &jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}
)

func downloadCancelCopied(bytes int64) *jobs.ItemObservation {
	return &jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: bytes}
}

// assertDownloadCancelItems checks every durable item record against the
// expected observations, then the public pages.
func assertDownloadCancelItems(t *testing.T, h *Harness, id, container, root string, saved uploadRecoveryState, want map[string]downloadCancelWant, totals jobs.ItemTotals) {
	t.Helper()
	if !saved.ItemsCaptured || saved.ItemTotals != totals || len(saved.Items) != len(want) {
		t.Fatalf("cancellation lost admitted identities: captured=%t totals=%+v rows=%d want=%+v", saved.ItemsCaptured, saved.ItemTotals, len(saved.Items), totals)
	}
	byKey := map[string]jobs.ItemRecord{}
	for _, item := range saved.Items {
		expected, ok := want[item.Key]
		if !ok || item.JobID != id || !item.Planned || !item.Admitted ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: container, Object: item.Key}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(item.Key, "scope/")))}) {
			t.Fatalf("cancellation changed an item identity: %+v", item)
		}
		if (item.Execution == nil) != (expected.execution == nil) || expected.execution != nil && item.Execution.ItemObservation != *expected.execution ||
			(item.Verification == nil) != (expected.verification == nil) || expected.verification != nil && item.Verification.ItemObservation != *expected.verification {
			t.Fatalf("cancellation record differs for %q: %+v", item.Key, item)
		}
		byKey[item.Key] = item
	}
	assertDownloadItemPages(t, h, id, byKey, totals)
	summary := fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, 0 refusals, %d unresolved, 0 unverified, %d with problems", totals.Failed, totals.Unresolved, totals.Problems)
	found := false
	for _, event := range saved.Audit {
		if event.Resource == "job "+id {
			found = true
			if !strings.HasSuffix(event.Summary, summary) {
				t.Fatalf("terminal audit lost the item facets: %q want suffix %q", event.Summary, summary)
			}
		}
	}
	if !found {
		t.Fatal("terminal audit event missing")
	}
}

func downloadCancellationCase(t *testing.T, h *Harness, mode string) {
	t.Helper()
	container := h.Container(h.Primary, "download-cancel")
	other := h.Container(h.Secondary, "untouched")
	h.Seed(h.Primary, container, "keep", []byte("primary sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("secondary sentinel"), nil)
	root := t.TempDir()
	seedListingLocal(t, root, "keep", []byte("destination sentinel"))
	a, b := []byte("bytes a"), []byte("bytes b")
	switch mode {
	case "native_held_headers":
		h.Seed(h.Primary, container, "scope/a.bin", a, nil)
		h.Seed(h.Primary, container, "scope/b.bin", b, nil)
		h.Seed(h.Primary, container, downloadCancelNative, []byte("native bytes"), nil)
	case "native_mid_body":
		body := make([]byte, 256*1024)
		for i := range body {
			body[i] = byte('a' + i%26)
		}
		h.Seed(h.Primary, container, "scope/a.bin", a, nil)
		h.Seed(h.Primary, container, downloadCancelNative, body, nil)
	case "engine_running":
		h.Seed(h.Primary, container, "scope/a.bin", a, nil)
		h.Seed(h.Primary, container, "scope/b.bin", b, nil)
		h.Seed(h.Primary, container, downloadCancelEngineHeld, downloadCancelLarge(), nil)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	localBefore := snapshotListingLocal(t, root)
	var trace *uploadDeleteTrace
	var id string
	switch mode {
	case "native_held_headers":
		barrier, ended, selected, held := interruptionProxy(t, h, container, http.MethodGet, downloadCancelNative, nil)
		trace = held
		id = downloadCancelStart(t, h, container, "scope/", root)
		if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK || selected.Load() != 1 {
			t.Fatalf("held native GET: %+v %v count=%d", event, err, selected.Load())
		}
		if err := h.Transfer.CancelJob(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Fatal("held native request did not end")
		}
		if selected.Load() != 1 || barrier.Err() == nil || !strings.Contains(barrier.Err().Error(), "context canceled") {
			t.Fatalf("held native GET was retried or not cancelled: %v count=%d", barrier.Err(), selected.Load())
		}
	case "native_mid_body":
		hold, held := downloadCancelBodyProxy(t, h, container, downloadCancelNative, 64*1024)
		trace = held
		id = downloadCancelStart(t, h, container, "scope/", root)
		select {
		case <-hold.reached:
		case <-time.After(15 * time.Second):
			t.Fatal("native body hold never reached")
		}
		// The cancel must land inside the body read: wait until the
		// executor's temporary holds every byte the proxy let through.
		deadline := time.Now().Add(15 * time.Second)
		size := int64(-1)
		for size < 64*1024 && time.Now().Before(deadline) {
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".aeronautes-part-") {
					if info, err := entry.Info(); err == nil {
						size = info.Size()
					}
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		if size != 64*1024 {
			t.Fatalf("temporary did not reach the delivered bytes: %d", size)
		}
		if err := h.Transfer.CancelJob(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	case "engine_running":
		// The engine is held part way through the last file's body, after it
		// has written the first two whole, so the cancel interrupts the real
		// child while it is writing a file of its own.
		hold, held := downloadCancelBodyProxy(t, h, container, downloadCancelEngineHeld, 64*1024)
		trace = held
		id = downloadCancelStart(t, h, container, "scope/", root)
		select {
		case <-hold.reached:
		case <-time.After(30 * time.Second):
			t.Fatal("the engine's read of the held file never reached the hold")
		}
		downloadCancelAwaitEngine(t, root, map[string][]byte{"a.bin": a, "b.bin": b})
		if err := h.Transfer.CancelJob(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	var counters jobs.Counters
	var fragments []string
	var want map[string]downloadCancelWant
	var totals jobs.ItemTotals
	var files map[string][]byte
	switch mode {
	case "native_held_headers":
		state = "cancelled"
		counters = jobs.Counters{ObjectsPlanned: 3, BytesPlanned: 26, ObjectsFailed: 1, ObjectsIncomplete: 1, NativeRouted: 1}
		fragments = []string{"transfer failures (first: " + downloadCancelNative + ":", "context canceled"}
		want = map[string]downloadCancelWant{
			"scope/a.bin":        {execution: downloadCancelNotAttempted},
			"scope/b.bin":        {execution: downloadCancelNotAttempted},
			downloadCancelNative: {execution: downloadCancelNativeRead},
		}
		totals = jobs.ItemTotals{Items: 3, Planned: 3, Admitted: 3, Failed: 1, Problems: 3, Incomplete: 1}
	case "native_mid_body":
		state = "cancelled"
		counters = jobs.Counters{ObjectsPlanned: 2, BytesPlanned: 7 + 256*1024, ObjectsFailed: 1, ObjectsIncomplete: 1, NativeRouted: 1}
		fragments = []string{"transfer failures (first: " + downloadCancelNative + ":", "context canceled"}
		want = map[string]downloadCancelWant{
			"scope/a.bin":        {execution: downloadCancelNotAttempted},
			downloadCancelNative: {execution: downloadCancelNativeRead},
		}
		totals = jobs.ItemTotals{Items: 2, Planned: 2, Admitted: 2, Failed: 1, Problems: 2, Incomplete: 1}
	case "engine_running":
		// What the engine reported is downloaded; the file it was writing is
		// unknown, and the interrupted engine removed its partial file
		// (ADR 0122 decision 5). The job says such a file may remain,
		// because it cannot know the engine was not killed.
		state = "partial"
		counters = jobs.Counters{ObjectsPlanned: 3, BytesPlanned: 14 + int64(len(downloadCancelLarge())), ObjectsCopied: 2, ObjectsVerified: 2, BytesCopied: 14, ObjectsFailed: 1, ObjectsIncomplete: 1}
		fragments = []string{"transfer failures (first: " + downloadCancelEngineHeld + ": context canceled)", "a name ending .partial"}
		want = map[string]downloadCancelWant{
			"scope/a.bin":            {execution: downloadCancelCopied(7), verification: downloadCancelVerified},
			"scope/b.bin":            {execution: downloadCancelCopied(7), verification: downloadCancelVerified},
			downloadCancelEngineHeld: {execution: downloadCancelUnknown},
		}
		totals = jobs.ItemTotals{Items: 3, Planned: 3, Admitted: 3, Failed: 1, Unresolved: 1, Problems: 1, Incomplete: 1}
		files = map[string][]byte{"a.bin": a, "b.bin": b}
	}
	saved := interruptionOutcome(t, h, id, state, counters, fragments...)
	if len(saved.Rows) != 0 || saved.Status != (app.RecoveryStatusDTO{}) || saved.Job.RecoveryRecords != 0 {
		t.Fatalf("download cancellation invented recovery facts: %+v", saved)
	}
	assertDownloadCancelItems(t, h, id, container, root, saved, want, totals)
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), files)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	if writes := trace.writeSnapshot(); len(writes) != 0 {
		t.Errorf("download cancellation wrote to the remote account: %v", writes)
	}
	requests := trace.requestCount()
	uploadRecoveryReopen(t, h, id, saved)
	if trace.requestCount() != requests {
		t.Error("reopen contacted the remote account")
	}
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), files)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}

// A job cancelled while queued never runs a body: no item, no local
// effect, and the slot-owning job finishes untouched.
func downloadCancellationQueued(t *testing.T, h *Harness) {
	t.Helper()
	settings, err := h.Settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.MaxActiveJobs = 1
	if _, err := h.Settings.Set(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	container := h.Container(h.Primary, "download-cancel")
	h.Seed(h.Primary, container, "hold/first.bin", []byte("hold bytes"), nil)
	h.Seed(h.Primary, container, "scope/a.bin", []byte("bytes a"), nil)
	beforeA := h.SnapshotNamespace(h.Primary)
	root, holdRoot := t.TempDir(), t.TempDir()
	seedListingLocal(t, root, "keep", []byte("destination sentinel"))
	localBefore, holdBefore := snapshotListingLocal(t, root), snapshotListingLocal(t, holdRoot)
	barrier, ended, selected, trace := interruptionProxy(t, h, container, http.MethodGet, "hold/first.bin", nil)
	first := downloadCancelStart(t, h, container, "hold/", holdRoot)
	if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK || selected.Load() != 1 {
		t.Fatalf("slot-owning barrier: %+v %v", event, err)
	}
	id := downloadCancelStart(t, h, container, "scope/", root)
	if queued, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id); err != nil || queued.State != "queued" || queued.StartedAt != "" {
		t.Fatalf("second download was not held after preflight: %+v %v", queued, err)
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if visible := h.AwaitJob(id); visible.State != "cancelled" {
		t.Fatalf("queued cancellation did not end cancelled: %+v", visible)
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err == nil {
		t.Error("cancelled queued job still accepts cancellation")
	}
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("slot-owning request did not end")
	}
	interruptionOutcome(t, h, first, "complete", jobs.Counters{ObjectsPlanned: 1, ObjectsCopied: 1, ObjectsVerified: 1, BytesPlanned: 10, BytesCopied: 10})
	saved := uploadRecoveryRead(t, h.database, id)
	if saved.Job.State != "cancelled" || saved.Job.Error != "" || saved.Job.StartedAt != "" || saved.Job.Counters != (jobs.Counters{ObjectsPlanned: 1, BytesPlanned: 7}) || len(saved.Items) != 0 {
		t.Fatalf("queued cancellation invented a body outcome: %+v items=%d", saved.Job, len(saved.Items))
	}
	assertListingLocalDelta(t, holdBefore, snapshotListingLocal(t, holdRoot), map[string][]byte{"first.bin": []byte("hold bytes")})
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), nil)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	if writes := trace.writeSnapshot(); len(writes) != 0 {
		t.Errorf("queued cancellation wrote to the remote account: %v", writes)
	}
	uploadRecoveryReopen(t, h, id, saved)
}

func downloadCancellationCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, mode := range []string{"native_held_headers", "native_mid_body", "engine_running"} {
		t.Run(mode, func(t *testing.T) { downloadCancellationCase(t, makeHarness(t), mode) })
	}
	t.Run("queued_cancel", func(t *testing.T) { downloadCancellationQueued(t, makeHarness(t)) })
}

func TestDownloadCancellationMock(t *testing.T)      { downloadCancellationCases(t, findMockHarness) }
func TestDownloadCancellationRealSwift(t *testing.T) { downloadCancellationCases(t, New) }
