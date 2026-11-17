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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

// A name the engine cannot carry is read by the application in one GET into
// a temporary file beside its final name, and linked to that name; the
// temporary is removed however the item ends (ADR 0122 decision 5). These
// cases hold the body before its last byte so the folder can be made
// read-only before the link: the link is then refused and the temporary
// cannot be removed, which the item must say, with the temporary left where
// it is.

// downloadCloseHoldReader passes all but the last byte, then waits to be
// released. The response keeps its length, so the client cannot finish the
// body before the last byte arrives.
type downloadCloseHoldReader struct {
	io.ReadCloser
	before  int64
	sent    int64
	arrived chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (r *downloadCloseHoldReader) Read(p []byte) (int, error) {
	if r.sent >= r.before {
		r.once.Do(func() {
			close(r.arrived)
			select {
			case <-r.release:
			case <-time.After(10 * time.Second):
			}
		})
	} else if remaining := r.before - r.sent; int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := r.ReadCloser.Read(p)
	r.sent += int64(n)
	return n, err
}

func downloadCloseRun(t *testing.T, h *Harness, scenario string) {
	t.Helper()
	cleanup := scenario == "cleanup"
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
	// Each byte passed reaches the client at once, so the temporary holds
	// all but the last while the body is held.
	proxy.FlushInterval = -1
	held, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(resume)
	var gets, heads atomic.Int32
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.Request.Method == http.MethodGet && response.Request.URL.Path == selectedPath && response.StatusCode == http.StatusOK {
			if gets.Add(1) == 1 && cleanup {
				response.Body = &downloadCloseHoldReader{ReadCloser: response.Body, before: int64(len(body) - 1), arrived: held, release: release}
			}
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
		if r.Method == http.MethodHead && r.URL.Path == selectedPath {
			heads.Add(1)
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
		case <-held:
		case <-time.After(10 * time.Second):
			t.Fatal("native download did not reach the hold before the last byte")
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			paths, err := filepath.Glob(filepath.Join(destination, ".aeronautes-part-*"))
			if err != nil || len(paths) > 1 {
				t.Fatalf("unexpected temporary files: %v %v", paths, err)
			}
			if len(paths) == 1 {
				data, err := os.ReadFile(paths[0])
				if err == nil && bytes.Equal(data, body[:len(body)-1]) {
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
			t.Fatalf("native file appeared before its last byte: %v", err)
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
	// One GET, and nothing read about the name before or after it.
	if gets.Load() != 1 || heads.Load() != 0 {
		t.Fatalf("the native route read the source other than once: gets=%d heads=%d", gets.Load(), heads.Load())
	}
	delta := map[string][]byte{}
	want := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: int64(len(body)), NativeRouted: 1}
	state, result := "complete", "applied"
	totals := jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1}
	execution := jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(body))}
	verification := &jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}
	if cleanup {
		want.ObjectsFailed = 1
		state, result = "partial", "verify_mismatch"
		delta["selected/"+filepath.Base(retained)] = body
		if _, err := os.Lstat(filepath.Join(destination, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a refused link placed a final file: %v", err)
		}
		totals.Failed, totals.Unresolved, totals.Problems = 1, 1, 1
		execution = jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: jobs.CodeIO}}
		verification = nil
	} else {
		want.ObjectsCopied, want.ObjectsVerified, want.BytesCopied = 1, 1, int64(len(body))
		delta["selected/proof.bin"] = body
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, root), delta)
	if saved.Job.State != state || saved.Job.Counters != want || !saved.ItemsCaptured || len(saved.Items) != 1 || len(saved.Rows) != 0 {
		t.Fatalf("native close outcome differs: %+v", saved)
	}
	item := saved.Items[0]
	if saved.ItemTotals != totals || item.Key != prefix+name || !item.Planned || !item.Admitted || item.ListedBytes != int64(len(body)) ||
		item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: source, Object: prefix + name}) ||
		item.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(destination, name)}) ||
		item.Execution == nil || item.Execution.ItemObservation != execution ||
		(item.Verification == nil) != (verification == nil) || verification != nil && item.Verification.ItemObservation != *verification {
		t.Fatalf("native close records differ: %+v", saved)
	}
	assertDownloadItemPages(t, h, id, map[string]jobs.ItemRecord{item.Key: item}, totals)
	summary := fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, 0 refusals, %d unresolved, 0 unverified, %d with problems", totals.Failed, totals.Unresolved, totals.Problems)
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
	if cleanup {
		for _, reason := range []string{name + ": permission denied", "remove download temporary file"} {
			if !strings.Contains(saved.Job.Error, reason) {
				t.Errorf("the job does not say %q about the refused link and the temporary left behind: %q", reason, saved.Job.Error)
			}
		}
	} else if saved.Job.Error != "" {
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
	encoded, err := json.Marshal(map[string]any{"scenario": scenario, "saved": saved})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("download close: %s", encoded)
}

func downloadCloseCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, scenario := range []string{"control", "cleanup"} {
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
