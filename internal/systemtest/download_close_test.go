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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/ncw/swift/v2"
)

type downloadCloseEOFReader struct {
	io.ReadCloser
	arrived chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (r *downloadCloseEOFReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && errors.Is(err, io.EOF) {
		return n, nil
	}
	if errors.Is(err, io.EOF) {
		r.once.Do(func() {
			close(r.arrived)
			select {
			case <-r.release:
			case <-time.After(10 * time.Second):
				err = errors.New("download EOF barrier timed out")
			}
		})
	}
	return n, err
}

func downloadCloseRun(t *testing.T, h *Harness, scenario string) {
	t.Helper()
	fault, collision := strings.HasPrefix(scenario, "source_close"), scenario == "link"
	cleanup := strings.HasSuffix(scenario, "_cleanup")
	source := h.Container(h.Primary, "download-close-source")
	other := h.Container(h.Secondary, "download-close-other")
	prefix, name := "scope\n/", "proof.bin"
	body := []byte("selected source bytes")
	h.Seed(h.Primary, source, prefix+name, body, nil)
	h.Seed(h.Primary, source, "outside", []byte("unselected source"), nil)
	h.Seed(h.Secondary, other, "outside", []byte("other account"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root := t.TempDir()
	seedListingLocal(t, root, "selected/keep", []byte("local sentinel"))
	seedListingLocal(t, root, "other-job/keep", []byte("other local job"))
	destination := filepath.Join(root, "selected")
	beforeLocal := snapshotListingLocal(t, root)
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	selectedPath := storage.Path + "/" + source + "/" + prefix + name
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 10 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	eof, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(resume)
	var gets, lengthHeads atomic.Int32
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.Request.Method == http.MethodGet && response.Request.URL.Path == selectedPath && response.StatusCode == http.StatusOK {
			if gets.Add(1) == 1 && cleanup {
				response.Body = &downloadCloseEOFReader{ReadCloser: response.Body, arrived: eof, release: release}
			}
			response.Header.Del("Content-Length")
			response.ContentLength = -1
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("close probe proxy: %v", err)
		http.Error(w, "fixture proxy failure", http.StatusBadGateway)
	}
	trace := &uploadDeleteTrace{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.mu.Lock()
		trace.requests++
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			trace.writes = append(trace.writes, r.Method+" "+r.URL.Path)
		}
		trace.mu.Unlock()
		if r.Method == http.MethodHead && r.URL.Path == selectedPath && gets.Load() > 0 {
			if lengthHeads.Add(1) == 1 && collision {
				if err := os.WriteFile(filepath.Join(destination, name), []byte("concurrent creator"), 0o600); err != nil {
					t.Errorf("create target after preflight: %v", err)
				}
			}
			if fault {
				w.Header().Set("Content-Length", strconv.Itoa(len(body)+1))
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range bookmarks {
		if bookmark.ID == h.Primary.BookmarkID {
			bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
			if _, err := h.Bookmarks.Save(t.Context(), bookmark, h.Primary.key); err != nil {
				t.Fatal(err)
			}
		}
	}
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: destination})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	request := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix, DestDirToken: picked.Token}
	preflight, err := h.Transfer.PreflightDownload(t.Context(), request)
	if err != nil || preflight == nil || preflight.Rejected || int(preflight.Objects) != 1 || preflight.ItemSamples[0].Route != "native" {
		t.Fatalf("close probe native preflight: %+v %v", preflight, err)
	}
	id, err := h.Transfer.StartDownload(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var retained string
	if cleanup {
		select {
		case <-eof:
		case <-time.After(10 * time.Second):
			t.Fatal("native download did not reach the streamed EOF barrier")
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			paths, err := filepath.Glob(filepath.Join(destination, ".aeronautes-part-*"))
			if err != nil || len(paths) > 1 {
				t.Fatalf("unexpected temporary files: %v %v", paths, err)
			}
			if len(paths) == 1 {
				data, err := os.ReadFile(paths[0])
				if err == nil && bytes.Equal(data, body) {
					retained = paths[0]
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("streamed bytes did not reach the owned temporary file")
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Cleanup(func() {
			if err := os.Chmod(destination, 0o755); err != nil {
				t.Error(err)
			}
		})
		if _, err := os.Lstat(filepath.Join(destination, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("native file appeared before EOF and closure: %v", err)
		}
		if err := os.Chmod(destination, 0o555); err != nil {
			t.Fatal(err)
		}
		resume()
	}
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h, h.Primary)
	if cleanup {
		if err := os.Chmod(destination, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	saved := uploadRecoveryRead(t, h.database, id)
	if gets.Load() != 1 || lengthHeads.Load() != 1 {
		t.Fatalf("close probe missed actual wire length check: gets=%d heads=%d", gets.Load(), lengthHeads.Load())
	}
	delta := map[string][]byte{}
	if fault && !cleanup && !collision {
		// The source advertises more bytes than it serves, so the content
		// comparison rejects what arrived and its bytes are retained as
		// unresolved residue (ADR 0045). This fault previously cleaned the
		// temporary up; it no longer does, because a rejected download
		// must not reach a cleanup whose identity check cannot tell our
		// own file from a replacement that reused its inode.
		paths, globErr := filepath.Glob(filepath.Join(destination, ".aeronautes-part-*"))
		if globErr != nil || len(paths) != 1 {
			t.Fatalf("a rejected download did not retain exactly one temporary: %v %v", paths, globErr)
		}
		held, readErr := os.ReadFile(paths[0])
		if readErr != nil || !bytes.Equal(held, body) {
			t.Fatalf("the retained temporary does not hold the served bytes: %v %q", readErr, held)
		}
		retained = paths[0]
		delta["selected/"+filepath.Base(retained)] = body
	}
	if cleanup {
		delta["selected/"+filepath.Base(retained)] = body
	}
	want := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: int64(len(body)), NativeRouted: 1}
	state, result := "complete", "applied"
	if fault || collision || cleanup {
		want.ObjectsFailed = 1
		if collision {
			want.ObjectsFailed = 0
		}
		state, result = "partial", "verify_mismatch"
		if collision {
			delta["selected/proof.bin"] = []byte("concurrent creator")
		} else if _, err := os.Lstat(filepath.Join(destination, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source close failure placed a final file: %v", err)
		}
	} else {
		want.ObjectsCopied, want.ObjectsVerified, want.BytesCopied = 1, 1, int64(len(body))
		delta["selected/proof.bin"] = body
		placed, err := os.ReadFile(filepath.Join(destination, name))
		if err != nil || !bytes.Equal(placed, body) {
			t.Fatalf("close control lacks complete destination bytes: %q %v", placed, err)
		}
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, root), delta)
	if saved.Job.State != state || saved.Job.Counters != want || !saved.ItemsCaptured || len(saved.Items) != 1 || len(saved.Rows) != 0 {
		t.Fatalf("native close outcome differs: %+v", saved)
	}
	totals := jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1}
	execution := jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(body))}
	verification := &jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}
	if fault || collision || cleanup {
		totals.Unresolved, totals.Problems = 1, 1
		execution = jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: jobs.CodeIO}}
		verification = nil
		if collision {
			totals.Conflicts = 1
			execution.Result, execution.Diagnostic.Code = jobs.ItemConflict, jobs.CodeConflict
		} else {
			totals.Failed = 1
		}
		if fault {
			execution.Diagnostic = jobs.ItemDiagnostic{Stage: jobs.StageVerification, Code: jobs.CodeUnverified}
			verification = &jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted, Diagnostic: execution.Diagnostic}
		}
	}
	item := saved.Items[0]
	if saved.ItemTotals != totals || item.Key != prefix+name || !item.Planned || !item.Admitted || item.ListedBytes != int64(len(body)) ||
		item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: source, Object: prefix + name}) ||
		item.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(destination, name)}) ||
		item.Staging == nil || item.Staging.Kind != jobs.ItemLocal || filepath.Dir(item.Staging.LocalPath) != destination ||
		!strings.HasPrefix(filepath.Base(item.Staging.LocalPath), ".aeronautes-part-") || retained != "" && item.Staging.LocalPath != retained ||
		item.Execution == nil || item.Execution.ItemObservation != execution ||
		(item.Verification == nil) != (verification == nil) || verification != nil && item.Verification.ItemObservation != *verification {
		t.Fatalf("native close records lost exact placement, closure or cleanup facts: %+v", saved)
	}
	assertDownloadItemPages(t, h, id, map[string]jobs.ItemRecord{item.Key: item}, totals)
	summary := fmt.Sprintf(" · item outcomes: %d failed names, %d conflicts, 0 refusals, %d unresolved, 0 unverified, %d with problems", totals.Failed, totals.Conflicts, totals.Unresolved, totals.Problems)
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.local."+state || saved.Audit[0].Result != result || saved.Audit[0].Resource != "job "+id || saved.Audit[0].Summary != summary {
		t.Fatalf("native close audit differs: %+v", saved.Audit)
	}
	var bookmarkID string
	if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT bookmark_id FROM audit_events WHERE id=?", saved.Audit[0].ID).Scan(&bookmarkID); err != nil || bookmarkID != h.Primary.BookmarkID {
		t.Errorf("native close audit binding differs: %q %v", bookmarkID, err)
	}
	visible, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
	if err != nil || !reflect.DeepEqual(visible, saved.Job) {
		t.Fatalf("close probe public/durable mismatch: %+v %v", visible, err)
	}
	witness, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: h.Primary.authURL, Username: h.Primary.user, APIKey: h.Primary.key, StorageURLOverride: front.URL + storage.EscapedPath()})
	if err != nil {
		t.Fatal(err)
	}
	stream, _, err := witness.OpenObject(t.Context(), source, prefix+name)
	if err != nil {
		t.Fatal(err)
	}
	read, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if readErr != nil || !bytes.Equal(read, body) || fault != errors.Is(closeErr, swift.ObjectCorrupted) || !fault && closeErr != nil {
		t.Fatalf("independent close witness differs: read=%q read_error=%v close_error=%v", read, readErr, closeErr)
	}
	if fault && !strings.Contains(saved.Job.Error, "Object Corrupted") {
		t.Errorf("download lost stream close corruption reason: %+v", saved.Job)
	}
	if collision && !strings.Contains(saved.Job.Error, "file exists") {
		t.Errorf("download lost no-replace refusal: %+v", saved.Job)
	}
	// A rejected download no longer attempts cleanup at all, so there is
	// no refusal to record: its temporary is retained by decision rather
	// than because a removal failed (ADR 0045). The refusal assertion
	// still applies to the cleanup variant that does attempt a removal.
	if cleanup && fault && strings.Contains(saved.Job.Error, "remove download temporary file") {
		t.Errorf("a rejected download attempted a cleanup it should not have: %+v", saved.Job)
	}
	if cleanup && !fault && (!strings.Contains(saved.Job.Error, "remove download temporary file") || !strings.Contains(saved.Job.Error, "permission denied")) {
		t.Errorf("download lost temporary cleanup refusal: %+v", saved.Job)
	}
	if scenario == "link_cleanup" && !strings.Contains(saved.Job.Error, "link ") {
		t.Errorf("cleanup erased final link refusal: %+v", saved.Job)
	}
	if !fault && !collision && !cleanup && (saved.Job.State != "complete" || saved.Job.Error != "") {
		t.Errorf("download close control did not complete: %+v", saved.Job)
	}
	requests := trace.requestCount()
	if next, err := h.Transfer.RerunJob(t.Context(), id); next != "" || err == nil || !strings.Contains(err.Error(), "own dialog") || trace.requestCount() != requests {
		t.Fatalf("close probe rerun escaped refusal: %q %v", next, err)
	}
	if writes := trace.writeSnapshot(); len(writes) != 0 {
		t.Fatalf("download wrote remote data: %v", writes)
	}
	uploadRecoveryReopen(t, h, id, saved)
	if requests != trace.requestCount() {
		t.Error("close probe reopen contacted Swift")
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, root), delta)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	encoded, err := json.Marshal(map[string]any{"fault": fault, "wire_close_corrupt": errors.Is(closeErr, swift.ObjectCorrupted), "read_bytes": len(read), "saved": saved})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("download close: %s", encoded)
}

func downloadCloseCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, scenario := range []string{"control", "source_close", "link", "source_close_cleanup", "link_cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			downloadCloseRun(t, makeHarness(t), scenario)
		})
	}
}

func TestDownloadCloseMock(t *testing.T) {
	t.Parallel()
	downloadCloseCases(t, findMockHarness)
}
func TestDownloadCloseRealSwift(t *testing.T) {
	t.Parallel()
	downloadCloseCases(t, New)
}
