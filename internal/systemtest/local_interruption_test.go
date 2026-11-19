package systemtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func interruptionProxy(t *testing.T, h *Harness, container, method, name string, refused map[string]bool) (*swifttest.HTTPResponseBarrier, <-chan struct{}, *atomic.Int32, *uploadDeleteTrace) {
	t.Helper()
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	selectedURL := &url.URL{Path: storage.Path + "/" + container + "/" + name}
	selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: method, EscapedPath: selectedURL.EscapedPath(), Occurrence: 1}
	barrier, err := swifttest.NewHTTPResponseBarrier(selector, false, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 10 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("interruption proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	held := barrier.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if object, ok := strings.CutPrefix(r.URL.Path, storage.Path+"/"+container+"/"); ok && r.Method == http.MethodPut && refused[object] {
			// Refusal occurs before upstream acceptance. The account witness,
			// not the interrupted response, proves the selected name is absent.
			http.Error(w, "fixture live write refusal", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	ended := make(chan struct{})
	var endedOnce sync.Once
	selected := &atomic.Int32{}
	trace := &uploadDeleteTrace{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.mu.Lock()
		trace.requests++
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			trace.writes = append(trace.writes, r.Method+" "+r.URL.Path)
		}
		trace.mu.Unlock()
		if r.Method == selector.Method && r.URL.EscapedPath() == selector.EscapedPath && r.URL.RawQuery == selector.RawQuery {
			selected.Add(1)
			defer endedOnce.Do(func() { close(ended) })
			if r.Header.Get("Expect") != "" {
				// Terminate the native client's 100-continue handshake before
				// response capture, which deliberately refuses interim replies.
				// Only this tiny fixture body is buffered, never upstream data.
				body, err := io.ReadAll(io.LimitReader(r.Body, 65))
				closeErr := r.Body.Close()
				if err != nil || closeErr != nil || len(body) > 64 || !strings.EqualFold(r.Header.Get("Expect"), "100-continue") {
					t.Errorf("fixture request body: %v %v bytes=%d", err, closeErr, len(body))
					http.Error(w, "fixture body refused", http.StatusBadRequest)
					return
				}
				r = r.Clone(r.Context())
				r.Header.Del("Expect")
				r.Body = io.NopCloser(bytes.NewReader(body))
				r.ContentLength = int64(len(body))
			}
		}
		held.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	t.Cleanup(barrier.Close)
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, bookmark := range bookmarks {
		if bookmark.ID != h.Primary.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, h.Primary.key); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatal("owned primary bookmark missing")
	}
	if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	return barrier, ended, selected, trace
}

func interruptionOutcome(t *testing.T, h *Harness, id, state string, counters jobs.Counters, fragments ...string) uploadRecoveryState {
	t.Helper()
	visible := h.AwaitJob(id)
	saved := uploadRecoveryRead(t, h.database, id)
	if !reflect.DeepEqual(visible, saved.Job) || saved.Job.State != state || saved.Job.Counters != counters || saved.Job.StartedAt == "" || saved.Job.FinishedAt == "" {
		t.Errorf("terminal result lost observed outcomes: visible=%+v saved=%+v want=%s %+v", visible, saved.Job, state, counters)
	}
	for _, fragment := range fragments {
		if !strings.Contains(saved.Job.Error, fragment) {
			t.Errorf("terminal reason missing %q: %q", fragment, saved.Job.Error)
		}
	}
	if len(fragments) == 0 && saved.Job.Error != "" {
		t.Errorf("successful control acquired an error: %q", saved.Job.Error)
	}
	result := "applied"
	if state == "partial" {
		result = "verify_mismatch"
	} else if state == "failed" || state == "cancelled" {
		result = "failed"
	}
	matched := 0
	for _, event := range saved.Audit {
		if event.Resource != "job "+id {
			continue
		}
		matched++
		var bookmark string
		if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT bookmark_id FROM audit_events WHERE id=?", event.ID).Scan(&bookmark); err != nil {
			t.Fatal(err)
		}
		if event.Operation != "transfer.local."+state || event.Result != result || bookmark != h.Primary.BookmarkID {
			t.Errorf("terminal audit lost outcome or authority: %+v bookmark=%q want=%s %s", event, bookmark, state, result)
		}
	}
	if matched != 1 {
		t.Errorf("terminal audit count=%d", matched)
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err == nil {
		t.Error("finished job still accepts cancellation")
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("interruption observation: %s", encoded)
	return saved
}

func interruptedUpload(t *testing.T, h *Harness, mode string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native upload fixture requires newline-containing local names")
	}
	container := h.Container(h.Primary, "interrupted-upload")
	other := h.Container(h.Secondary, "untouched")
	h.Seed(h.Primary, container, "keep", []byte("primary sentinel"), nil)
	h.Seed(h.Primary, container, ".aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("secondary sentinel"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root := t.TempDir()
	names := []string{"first\nnative", "second\nnative", "engine.bin"}
	bodies := [][]byte{[]byte("first bytes"), []byte("second bytes"), []byte("engine bytes")}
	var paths []string
	var planned int64
	for i, name := range names {
		paths = append(paths, seedListingLocal(t, root, name, bodies[i]))
		planned += int64(len(bodies[i]))
	}
	seedListingLocal(t, root, "unselected/keep", []byte("unselected bytes"))
	localBefore := snapshotListingLocal(t, root)
	cancelled := strings.HasSuffix(mode, "_cancel")
	firstFailed := strings.HasPrefix(mode, "failure")
	refused := map[string]bool{"live/" + names[0]: firstFailed, "live/" + names[1]: mode != "complete_control"}
	barrier, ended, selected, trace := interruptionProxy(t, h, container, http.MethodPut, "live/"+names[1], refused)
	app.WireFilePicker(h.Transfer, listingFilePicker{files: paths})
	picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/", SelectionToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	event, err := barrier.Wait(t.Context())
	wantHTTP := http.StatusForbidden
	if mode == "complete_control" {
		wantHTTP = http.StatusCreated
	}
	if err != nil || event.StatusCode != wantHTTP || selected.Load() != 1 {
		t.Fatalf("selected native PUT barrier: %+v %v count=%d", event, err, selected.Load())
	}
	pending, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
	if err != nil || pending.State != "running" || pending.Counters.ObjectsCopied != 1 {
		t.Fatalf("first native item did not finish before the barrier: %+v %v", pending, err)
	}
	if cancelled {
		if err := h.Transfer.CancelJob(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	} else if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("selected upload request did not end")
	}
	if selected.Load() != 1 || cancelled && (barrier.Err() == nil || !strings.Contains(barrier.Err().Error(), "context canceled")) || !cancelled && barrier.Err() != nil {
		t.Fatalf("unexpected cancellation/timeout/retry: %v count=%d", barrier.Err(), selected.Load())
	}
	state := "partial"
	want := jobs.Counters{ObjectsPlanned: 3, BytesPlanned: planned, NativeRouted: 2}
	var live []int
	if !firstFailed {
		live = append(live, 0)
	}
	if mode == "complete_control" {
		state = "complete"
		live = append(live, 1)
	}
	if !cancelled {
		live = append(live, 2)
	}
	want.ObjectsFailed = 1
	if firstFailed {
		want.ObjectsFailed++
	}
	if mode == "complete_control" {
		want.ObjectsFailed = 0
	}
	if cancelled {
		// The file the cancellation stopped is the incomplete part of the
		// failed names (ADR 0129).
		want.ObjectsIncomplete = 1
	}
	for _, i := range live {
		want.ObjectsCopied++
		want.ObjectsVerified++
		want.BytesCopied += int64(len(bodies[i]))
	}
	var fragments []string
	if want.ObjectsFailed > 0 {
		first := 1
		if firstFailed {
			first = 0
		}
		fragments = append(fragments, fmt.Sprintf("transfer failures (first: live/%s:", names[first]))
	}
	if cancelled {
		fragments = append(fragments, "context canceled")
	}
	saved := interruptionOutcome(t, h, id, state, want, fragments...)
	assertInterruptedUploadItems(t, h, id, container, mode, paths, bodies, want.ObjectsFailed, saved)
	// No file here is large enough for the engine to report a segment
	// path, which is the only recovery record an upload still makes.
	if len(saved.Rows) != 0 || saved.Status != (app.RecoveryStatusDTO{}) || saved.Job.RecoveryRecords != 0 {
		t.Fatalf("an upload of plain files left recovery facts: %+v", saved)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	var allowed []string
	for _, i := range live {
		name := "live/" + names[i]
		object, ok := afterA.Containers[container].Objects[name]
		if !ok {
			t.Fatalf("verified upload missing: %q", name)
		}
		uploadRecoveryVerifyObject(t, object, bodies[i], "plain", "", "")
		allowed = append(allowed, container+"/"+name)
	}
	h.AssertNoCollateral(beforeA, afterA, allowed...)
	h.AssertNoCollateral(beforeB, afterB)
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), nil)
	if cancelled && len(trace.writeSnapshot()) != 2 {
		t.Errorf("cancelled upload admitted later engine writes: %v", trace.writeSnapshot())
	}
	requests := trace.requestCount()
	uploadRecoveryReopen(t, h, id, saved)
	if trace.requestCount() != requests {
		t.Error("reopen contacted the remote account")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
}

func assertInterruptedUploadItems(t *testing.T, h *Harness, id, container, mode string, paths []string, bodies [][]byte, failed int64, saved uploadRecoveryState) {
	t.Helper()
	cancelled := strings.HasSuffix(mode, "_cancel")
	count := 3
	if cancelled {
		count = 2
	}
	// A refused PUT wrote nothing. Only the PUT cancelled before its answer
	// leaves the name unknown.
	var unresolved, incomplete int64
	if cancelled {
		unresolved, incomplete = 1, 1
	}
	want := jobs.ItemTotals{Items: int64(count), Planned: int64(count), Admitted: int64(count), Failed: failed, Unresolved: unresolved, Problems: failed, Incomplete: incomplete}
	if !saved.ItemsCaptured || saved.ItemTotals != want || len(saved.Items) != count {
		t.Fatalf("interruption lost admitted upload identities: captured=%t totals=%+v rows=%d want=%+v", saved.ItemsCaptured, saved.ItemTotals, len(saved.Items), want)
	}
	byKey := map[string]jobs.ItemRecord{}
	for i, item := range saved.Items {
		key := "live/" + filepath.Base(paths[i])
		if item.Key != key || item.JobID != id || !item.Planned || !item.Admitted || item.ListedBytes != int64(len(bodies[i])) ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: paths[i]}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: container, Object: key}) || item.Execution == nil {
			t.Fatalf("interruption changed original upload identity: %+v", item)
		}
		failure := i == 0 && strings.HasPrefix(mode, "failure") || i == 1 && mode != "complete_control"
		if failure {
			wantFailure := jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
				Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: jobs.CodeForbidden, HTTPStatus: 403}}
			if i == 1 && cancelled {
				wantFailure = jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved,
					Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: jobs.CodeCancelled}}
			}
			if item.Execution.ItemObservation != wantFailure || item.Verification != nil {
				t.Fatalf("interruption fabricated verification or lost failed name: %+v", item)
			}
		} else if item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(bodies[i]))}) ||
			item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
			t.Fatalf("interruption erased earlier copied bytes: %+v", item)
		}
		byKey[key] = item
	}
	assertUploadItemPages(t, h, id, byKey, want)
	summary := fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, 0 refusals, %d unresolved, 0 unverified, %d with problems", failed, unresolved, failed)
	if len(saved.Audit) != 1 || saved.Audit[0].Summary != summary {
		t.Fatalf("interruption audit lost distinct item facets: %+v", saved.Audit)
	}
}

func interruptedDownload(t *testing.T, h *Harness, mode string) {
	t.Helper()
	settings, err := h.Settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.MaxActiveJobs = 1
	if _, err := h.Settings.Set(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	container := h.Container(h.Primary, "interrupted-download")
	other := h.Container(h.Secondary, "untouched")
	h.Seed(h.Primary, container, "hold/first.bin", []byte("hold bytes"), nil)
	h.Seed(h.Primary, container, "scope/file.bin", []byte("selected bytes"), nil)
	h.Seed(h.Primary, container, "keep", []byte("primary sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("secondary sentinel"), nil)
	hasDir := mode != "engine_error"
	if hasDir {
		h.Seed(h.Primary, container, "scope/folder/", nil, map[string]string{"Content-Type": "application/directory"})
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root, holdRoot := t.TempDir(), t.TempDir()
	seedListingLocal(t, root, "keep", []byte("destination sentinel"))
	seedListingLocal(t, holdRoot, "keep", []byte("first-job sentinel"))
	holdBefore := snapshotListingLocal(t, holdRoot)
	barrier, ended, selected, trace := interruptionProxy(t, h, container, http.MethodGet, "hold/first.bin", nil)
	start := func(prefix, destination string) string {
		app.WireFilePicker(h.Transfer, listingFilePicker{dir: destination})
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
	first := start("hold/", holdRoot)
	event, err := barrier.Wait(t.Context())
	if err != nil || event.StatusCode != http.StatusOK || selected.Load() != 1 {
		t.Fatalf("slot-owning download barrier: %+v %v count=%d", event, err, selected.Load())
	}
	id := start("scope/", root)
	queued, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
	if err != nil || queued.State != "queued" || queued.StartedAt != "" {
		t.Fatalf("second download was not held after preflight: %+v %v", queued, err)
	}
	if mode != "complete_control" {
		// The queued job's folder can no longer be written: its marker is
		// refused by the application, and its file by the real engine, which
		// says so in an error line that is that file's outcome (ADR 0122
		// decision 5).
		if err := os.Chmod(root, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(root, 0o700); err != nil {
				t.Error(err)
			}
		})
		probe := filepath.Join(root, "permission-probe")
		err := os.Mkdir(probe, 0o700)
		if !errors.Is(err, os.ErrPermission) {
			if err == nil {
				if err := os.Remove(probe); err != nil {
					t.Fatal(err)
				}
			}
			t.Skipf("native directory-creation denial is unavailable: %v", err)
		}
		t.Logf("native directory creation refused with selected root mode 0500: %v", err)
	}
	localBefore := snapshotListingLocal(t, root)
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("slot-owning request did not end")
	}
	if barrier.Err() != nil || selected.Load() != 1 {
		t.Fatalf("unexpected first-job barrier failure/retry: %v count=%d", barrier.Err(), selected.Load())
	}
	interruptionOutcome(t, h, first, "complete", jobs.Counters{ObjectsPlanned: 1, ObjectsCopied: 1, ObjectsVerified: 1, BytesPlanned: 10, BytesCopied: 10})
	want := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: 14}
	state := "partial"
	var dirs []string
	var fragments []string
	var files map[string][]byte
	if hasDir {
		want.ObjectsPlanned++
		if mode == "failure_then_engine_error" {
			want.ObjectsFailed = 1
			fragments = append(fragments, "transfer failures (first: scope/folder/:", filepath.Join(root, "folder"))
		} else {
			want.ObjectsCopied++
			want.ObjectsVerified++
			dirs = append(dirs, "folder")
		}
	}
	if mode == "complete_control" {
		state = "complete"
		want.ObjectsCopied++
		want.ObjectsVerified++
		want.BytesCopied = 14
		files = map[string][]byte{"file.bin": []byte("selected bytes")}
	} else {
		want.ObjectsFailed++
		fragments = append(fragments, "permission denied")
		if mode == "engine_error" {
			fragments = append(fragments, "transfer failures (first: scope/file.bin:")
		}
	}
	saved := interruptionOutcome(t, h, id, state, want, fragments...)
	if len(saved.Rows) != 0 || saved.Status != (app.RecoveryStatusDTO{}) || saved.Job.RecoveryRecords != 0 || len(saved.Audit) != 2 {
		t.Fatalf("download invented recovery facts or lost job audit: %+v", saved)
	}
	assertListingLocalDelta(t, holdBefore, snapshotListingLocal(t, holdRoot), map[string][]byte{"first.bin": []byte("hold bytes")})
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), files, dirs...)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	if len(trace.writeSnapshot()) != 0 {
		t.Errorf("download mutated the remote account: %v", trace.writeSnapshot())
	}
	requests := trace.requestCount()
	uploadRecoveryReopen(t, h, id, saved)
	if trace.requestCount() != requests {
		t.Error("reopen contacted the remote account")
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}

func localInterruptionCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, mode := range []string{"failure_then_cancel", "success_then_cancel", "failure_control", "complete_control"} {
		t.Run("upload_"+mode, func(t *testing.T) { interruptedUpload(t, makeHarness(t), mode) })
	}
	for _, mode := range []string{"failure_then_engine_error", "engine_error", "complete_control"} {
		t.Run("download_"+mode, func(t *testing.T) { interruptedDownload(t, makeHarness(t), mode) })
	}
}

func TestLocalInterruptionMock(t *testing.T)      { localInterruptionCases(t, findMockHarness) }
func TestLocalInterruptionRealSwift(t *testing.T) { localInterruptionCases(t, New) }
