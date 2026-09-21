package systemtest

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

type downloadRecordsTrace struct {
	copyItemRecordsTrace
	gets map[string]int
}

func (t *downloadRecordsTrace) selectedGets() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := map[string]int{}
	for key, count := range t.gets {
		result[key] = count
	}
	return result
}

func downloadRecordsProxy(t *testing.T, h *Harness, container, prefix string, names map[string]bool, beforeGet ...func(string)) *downloadRecordsTrace {
	t.Helper()
	storage, err := url.Parse(h.Primary.storageURL)
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
		t.Errorf("identity probe proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	trace := &downloadRecordsTrace{copyItemRecordsTrace: copyItemRecordsTrace{refused: map[string]int{}}, gets: map[string]int{}}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.traffic.mu.Lock()
		trace.traffic.requests++
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			trace.traffic.writes = append(trace.traffic.writes, r.Method+" "+r.URL.Path)
		}
		trace.traffic.mu.Unlock()
		scope := storage.Path + "/" + container + "/" + prefix
		name, selected := strings.CutPrefix(r.URL.Path, scope)
		if r.Method == http.MethodGet && selected {
			trace.mu.Lock()
			trace.gets[name]++
			trace.mu.Unlock()
			for _, hook := range beforeGet {
				hook(name)
			}
		}
		if r.Method == http.MethodGet && selected && names[name] {
			trace.mu.Lock()
			trace.refused[name]++
			trace.mu.Unlock()
			w.Header().Set("X-Trans-Id", "fixture-private-header")
			http.Error(w, "fixture selected source read refusal <script> token=copy-private https://user:password@fixture.invalid/?token=copy-private", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
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
		t.Fatal("owned source bookmark missing")
	}
	return trace
}

func downloadRecordsRun(t *testing.T, h *Harness, count int, native bool) {
	t.Helper()
	source := h.Container(h.Primary, "download-identity-source")
	segments := h.Container(h.Primary, "download-identity-segments")
	aliases := h.Container(h.Primary, "download-identity-aliases")
	other := h.Container(h.Secondary, "download-identity-other")
	h.Seed(h.Primary, source, "outside", []byte("unselected source"), nil)
	h.Seed(h.Primary, source, ".aeronautes/stage/other-job/keep", []byte("other stage"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other account"), nil)
	shared := []byte("shared bytes")
	h.Seed(h.Primary, segments, "shared/0", shared, nil)
	h.Seed(h.Primary, aliases, "dlo", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	h.SeedSLO(h.Primary, aliases, "slo", fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(shared), len(shared)))
	prefix := "scope/"
	if native {
		prefix = "scope\n/"
	}
	selected := map[string]bool{}
	originals := map[string][]byte{}
	for i := range count {
		name := fmt.Sprintf("f-%04d-étude�.bin", i)
		selected[name] = true
		originals[name] = []byte("download bytes: " + name)
	}
	const successful = "z-success.bin"
	originals[successful] = []byte("independent successful bytes")
	var totalBytes int64
	for name, body := range originals {
		h.Seed(h.Primary, source, prefix+name, body, nil)
		totalBytes += int64(len(body))
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root := t.TempDir()
	seedListingLocal(t, root, "selected/sentinel", []byte("existing destination"))
	seedListingLocal(t, root, "selected/.aeronautes-stage-other-job/keep", []byte("other local stage"))
	seedListingLocal(t, root, "outside/keep", []byte("outside selection"))
	localBefore := snapshotListingLocal(t, root)
	destination := filepath.Join(root, "selected")
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: destination})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix})
	if err != nil || picked == nil || picked.Token == "" {
		t.Fatalf("owned picker failed: %+v %v", picked, err)
	}
	req := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix, DestDirToken: picked.Token}
	preflight, err := h.Transfer.PreflightDownload(t.Context(), req)
	if err != nil || preflight == nil || preflight.Rejected || int(preflight.Objects) != count+1 || preflight.TotalBytes != totalBytes {
		t.Fatalf("download fixture did not pass complete preflight: %+v %v", preflight, err)
	}
	for _, item := range preflight.ItemSamples {
		name := strings.TrimPrefix(item.Name, prefix)
		body, exists := originals[name]
		route := "rclone"
		if native {
			route = "native"
		}
		if !exists || item.RelPath != name || item.Dir || item.Size != int64(len(body)) || item.Route != route {
			t.Fatalf("unexpected captured download identity or route: %+v", item)
		}
	}
	var trace *uploadDeleteTrace
	var refused *downloadRecordsTrace
	var release func()
	expected := map[string][]byte{"selected/" + successful: originals[successful]}
	if native {
		refused = downloadRecordsProxy(t, h, source, prefix, selected)
		trace = &refused.traffic
		release = func() {}
	} else {
		heldName := successful
		if count > 0 {
			heldName = "f-0000-étude�.bin"
		}
		barrier, ended, hits, traffic := interruptionProxy(t, h, source, http.MethodGet, prefix+heldName, nil)
		trace = traffic
		release = func() {
			if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
				t.Fatalf("download placement barrier: %+v %v", event, err)
			}
			for name := range selected {
				body := []byte("owned collision: " + name)
				seedListingLocal(t, root, "selected/"+name, body)
				expected["selected/"+name] = body
			}
			if err := barrier.Release(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-ended:
			case <-time.After(10 * time.Second):
				t.Fatal("released download response did not return")
			}
			if hits.Load() != 1 || barrier.Err() != nil {
				t.Fatalf("unexpected barrier retry or failure: %d %v", hits.Load(), barrier.Err())
			}
		}
	}
	// The fixture proxies above republish the bookmark, which moves its
	// generation and retires the selection taken for the report. Choose
	// again so the start carries a selection bound to the account as it
	// now stands (ADR 0041).
	restarted, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{
		BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix,
	})
	if err != nil || restarted == nil || restarted.Token == "" {
		t.Fatalf("owned picker failed after the fixture proxy: %+v %v", restarted, err)
	}
	req.DestDirToken = restarted.Token
	id, err := h.Transfer.StartDownload(t.Context(), req)
	if err != nil || id == "" {
		t.Fatalf("download was not admitted: %q %v", id, err)
	}
	release()
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h, h.Primary)
	saved := uploadRecoveryRead(t, h.database, id)
	visible, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
	if err != nil || !reflect.DeepEqual(visible, saved.Job) {
		t.Fatalf("public and durable download differ: %+v %v", visible, err)
	}
	want := jobs.Counters{ObjectsPlanned: int64(count + 1), BytesPlanned: totalBytes, ObjectsCopied: 1, ObjectsVerified: 1, BytesCopied: int64(len(originals[successful])), ObjectsFailed: int64(count)}
	if native {
		want.NativeRouted = int64(count + 1)
	} else {
		want.ObjectsFailed = 0
	}
	state := "complete"
	if count != 0 {
		state = "partial"
	}
	if saved.Job.State != state || saved.Job.Counters != want {
		t.Errorf("download loses exact failed-name totals: got=%+v want=%s %+v", saved.Job, state, want)
	}
	assertDownloadRecords(t, h, id, source, prefix, destination, selected, originals, native, saved)
	// Cleanup removes the files this job staged, so an engine download with
	// failed items leaves neither the staged copies nor the stage (ADR 0048).
	var dirs []string
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), expected, dirs...)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	if writes := trace.writeSnapshot(); len(writes) != 0 {
		t.Fatalf("download mutated remote storage: %v", writes)
	}
	if native {
		refused.mu.Lock()
		defer refused.mu.Unlock()
		if len(refused.refused) != count {
			t.Fatalf("wrong refused source-name set: %v", refused.refused)
		}
		for name, attempts := range refused.refused {
			if !selected[name] || attempts != 1 {
				t.Fatalf("unexpected refused source or retry: %q %d", name, attempts)
			}
		}
	}
	encoded, err := json.Marshal(map[string]any{"failed": count, "native": native, "job": saved.Job, "captured": saved.ItemsCaptured, "items": len(saved.Items), "facets": saved.ItemTotals})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("checked download records: %s", encoded)
	requests := trace.requestCount()
	if next, err := h.Transfer.RerunJob(t.Context(), id); next != "" || err == nil || !strings.Contains(err.Error(), "own dialog") || trace.requestCount() != requests {
		t.Fatalf("download history gained rerun authority: %q %v", next, err)
	}
	uploadRecoveryReopen(t, h, id, saved)
	if trace.requestCount() != requests {
		t.Fatal("orderly download reopen generated storage traffic")
	}
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), expected, dirs...)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}

func downloadRecordsMatrix(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, native := range []bool{false, true} {
		for _, count := range []int{0, 1, 201, 401} {
			t.Run(fmt.Sprintf("native_%v_failed_%d", native, count), func(t *testing.T) {
				downloadRecordsRun(t, makeHarness(t), count, native)
			})
		}
	}
}

func TestDownloadRecordsMock(t *testing.T)      { t.Parallel(); downloadRecordsMatrix(t, findMockHarness) }
func TestDownloadRecordsRealSwift(t *testing.T) { t.Parallel(); downloadRecordsMatrix(t, New) }
