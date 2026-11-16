package systemtest

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

func uploadItemRecordsProxy(t *testing.T, h *Harness, container string, names map[string]bool) *copyItemRecordsTrace {
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
	trace := &copyItemRecordsTrace{refused: map[string]int{}}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.traffic.mu.Lock()
		trace.traffic.requests++
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			trace.traffic.writes = append(trace.traffic.writes, r.Method+" "+r.URL.Path)
		}
		trace.traffic.mu.Unlock()
		prefix := storage.Path + "/" + container + "/scope/"
		name, selected := strings.CutPrefix(r.URL.Path, prefix)
		if r.Method == http.MethodPut && selected && r.Header.Get("If-None-Match") != "" {
			t.Errorf("an upload PUT carries a create-only condition: %q", name)
		}
		if r.Method == http.MethodPut && selected && names[name] {
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

func uploadItemRecordsRun(t *testing.T, h *Harness, count int, native bool) {
	t.Helper()
	if native && runtime.GOOS == "windows" {
		t.Skip("native upload fixture needs newline local names")
	}
	dst := h.Container(h.Primary, "upload-identity-dst")
	segments := h.Container(h.Primary, "upload-identity-segments")
	aliases := h.Container(h.Primary, "upload-identity-aliases")
	other := h.Container(h.Secondary, "untouched")
	h.Seed(h.Primary, dst, "unselected", []byte("destination sentinel"), nil)
	h.Seed(h.Primary, dst, ".aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
	shared := []byte("shared bytes")
	h.Seed(h.Primary, segments, "shared/0", shared, nil)
	h.Seed(h.Primary, aliases, "dlo", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	slo := fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(shared), len(shared))
	h.SeedSLO(h.Primary, aliases, "slo", slo)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	localRoot := t.TempDir()
	body := []byte("upload bytes")
	selected := map[string]bool{}
	var paths []string
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("f-%04d-étude�.bin", i)
		if native {
			name = fmt.Sprintf("f-%04d\nfailed", i)
		}
		selected[name] = true
		paths = append(paths, seedListingLocal(t, localRoot, name, body))
	}
	paths = append(paths, seedListingLocal(t, localRoot, "z-engine.bin", body))
	seedListingLocal(t, localRoot, "unselected/keep", []byte("local sentinel"))
	localBefore := snapshotListingLocal(t, localRoot)
	trace := uploadItemRecordsProxy(t, h, dst, selected)
	app.WireFilePicker(h.Transfer, listingFilePicker{files: paths})
	picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: dst, Prefix: "scope/"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	id, err := h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: dst, Prefix: "scope/", SelectionToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	// Hundreds of refused files may outlast the separate cleanup wait.
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h, h.Primary)
	serviceTime := time.Since(started)
	saved := uploadRecoveryRead(t, h.database, id)
	visible, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
	if err != nil || !reflect.DeepEqual(visible, saved.Job) {
		t.Fatalf("public/durable mismatch: %+v %v %+v", visible, err, saved)
	}
	state := "partial"
	if count == 0 {
		state = "complete"
	}
	want := jobs.Counters{ObjectsPlanned: int64(count + 1), BytesPlanned: int64((count + 1) * len(body)), ObjectsCopied: 1, BytesCopied: int64(len(body)), ObjectsVerified: 1, ObjectsFailed: int64(count), NativeRouted: 0}
	if native {
		want.NativeRouted = int64(count)
	}
	if saved.Job.State != state || saved.Job.Counters != want || saved.Job.StartedAt == "" || saved.Job.FinishedAt == "" {
		t.Errorf("Upload does not count each failed identity once: got=%+v want=%s %+v", saved.Job, state, want)
	}
	if count == 0 && saved.Job.Error != "" {
		t.Errorf("successful upload control acquired error: %q", saved.Job.Error)
	}
	trace.mu.Lock()
	refused := map[string]int{}
	for name, n := range trace.refused {
		refused[name] = n
	}
	trace.mu.Unlock()
	if len(refused) != count {
		t.Errorf("failed PUT identity set incomplete: %d want=%d", len(refused), count)
	}
	for name := range selected {
		if refused[name] != 1 {
			t.Errorf("selected PUT refusal for %q occurred %d times", name, refused[name])
		}
	}
	result := "verify_mismatch"
	if count == 0 {
		result = "applied"
	}
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.local."+state || saved.Audit[0].Resource != "job "+id || saved.Audit[0].Result != result {
		t.Errorf("upload audit differs: %+v", saved.Audit)
	}
	var bookmark string
	if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT bookmark_id FROM audit_events WHERE operation=? AND resource=?", "transfer.local."+state, "job "+id).Scan(&bookmark); err != nil || bookmark != h.Primary.BookmarkID {
		t.Errorf("audit authority differs: %q %v", bookmark, err)
	}
	// No file here is large enough for the engine to report a segment
	// path, which is the only recovery record an upload still makes.
	if len(saved.Rows) != 0 || saved.Job.RecoveryRecords != 0 || saved.Status != (app.RecoveryStatusDTO{}) {
		t.Errorf("an upload of plain files left recovery facts: %+v", saved)
	}
	page, err := h.Jobs.Items(t.Context(), h.Primary.BookmarkID, id, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	totals, err := h.Jobs.ItemTotals(t.Context(), h.Primary.BookmarkID, id)
	if err != nil {
		t.Fatal(err)
	}
	var version, records int
	if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT item_records_version FROM jobs WHERE id=?", id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM job_items WHERE job_id=?", id).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err == nil {
		t.Error("terminal upload accepts cancellation")
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	got, ok := afterA.Containers[dst].Objects["scope/z-engine.bin"]
	if !ok || got.Size != int64(len(body)) || got.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) || got.Class != "plain" {
		t.Errorf("engine control bytes differ: %+v", got)
	}

	// A refused file leaves nothing anywhere: the only name that changed
	// is the one the store accepted.
	h.AssertNoCollateral(beforeA, afterA, dst+"/scope/z-engine.bin")
	h.AssertNoCollateral(beforeB, afterB)
	if !reflect.DeepEqual(localBefore, snapshotListingLocal(t, localRoot)) {
		t.Error("upload changed selected or unselected local bytes")
	}

	assertUploadItemRecords(t, h, id, dst, count, len(body), native, paths, saved)
	assertUploadItemWrites(t, trace, h.Primary.storageURL, dst, native, paths)
	if version != 1 || records != count+1 || page.Status != "available" || totals.Status != "available" {
		t.Fatalf("actual upload caller did not opt in: version=%d rows=%d page=%+v totals=%+v", version, records, page, totals)
	}
	encoded, err := json.Marshal(map[string]any{"failed": count, "native": native, "retained": saved.ItemTotals, "counters": saved.Job.Counters, "service_ms": float64(serviceTime) / float64(time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("upload item recording: %s", encoded)
	requests := trace.traffic.requestCount()
	var jobsBefore, jobsAfter int
	if err := h.database.Reader().QueryRow("SELECT COUNT(*) FROM jobs").Scan(&jobsBefore); err != nil {
		t.Fatal(err)
	}
	if next, err := h.Transfer.RerunJob(t.Context(), id); next != "" || err == nil || !strings.Contains(err.Error(), "own dialog") {
		t.Fatalf("retained local path became rerun authority: %q %v", next, err)
	}
	if err := h.database.Reader().QueryRow("SELECT COUNT(*) FROM jobs").Scan(&jobsAfter); err != nil || jobsAfter != jobsBefore || requests != trace.traffic.requestCount() {
		t.Fatalf("refused local rerun started work: jobs=%d/%d requests=%d/%d error=%v", jobsBefore, jobsAfter, requests, trace.traffic.requestCount(), err)
	}
	uploadRecoveryReopen(t, h, id, saved)
	if requests != trace.traffic.requestCount() {
		t.Error("upload reopen contacted remote")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
	if !reflect.DeepEqual(localBefore, snapshotListingLocal(t, localRoot)) {
		t.Error("reopen changed local bytes")
	}
}

func uploadItemRecordsCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, native := range []bool{false, true} {
		for _, count := range []int{0, 1, 201, 401} {
			t.Run(fmt.Sprintf("native_%t_failed_%d", native, count), func(t *testing.T) {
				uploadItemRecordsRun(t, makeHarness(t), count, native)
			})
		}
	}
}

func TestUploadItemRecordsMock(t *testing.T) {
	t.Parallel()
	uploadItemRecordsCases(t, findMockHarness)
}
func TestUploadItemRecordsRealSwift(t *testing.T) {
	t.Parallel()
	uploadItemRecordsCases(t, New)
}
