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
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

type copyItemRecordsTrace struct {
	mu      sync.Mutex
	refused map[string]int
	traffic uploadDeleteTrace
}

func copyItemRecordsProxy(t *testing.T, h *Harness, container string, names map[string]bool) *copyItemRecordsTrace {
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

func copyItemRecordsRun(t *testing.T, h *Harness, count int) {
	t.Helper()
	src := h.Container(h.Primary, "failure-identity-src")
	dst := h.Container(h.Secondary, "failure-identity-dst")
	segments := h.Container(h.Secondary, "failure-identity-segments")
	outside := h.Container(h.Secondary, "failure-identity-aliases")
	body := []byte("copy bytes")
	selected := map[string]bool{}
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("f-%04d\nfailed", i)
		selected[name] = true
		h.Seed(h.Primary, src, "scope/"+name, body, nil)
	}
	h.Seed(h.Primary, src, "scope/z-engine.bin", body, nil)
	h.Seed(h.Primary, src, "unselected", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, dst, "scope/.aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	shared := []byte("shared bytes")
	h.Seed(h.Secondary, segments, "shared/0", shared, nil)
	h.Seed(h.Secondary, outside, "alias", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	slo := fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(shared), len(shared))
	h.SeedSLO(h.Secondary, outside, "slo-alias", slo)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	srcTrace := copyItemRecordsProxy(t, h, src, selected)
	dstTrace := copyCompletionProxy(t, h, h.Secondary, "")
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/", DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/"})
	if err != nil {
		t.Fatal(err)
	}
	copyCompletionAwaitPins(t, h)
	saved := copyCompletionRead(t, h.database, id)
	visible, err := h.Jobs.Get(t.Context(), id)
	if err != nil || !reflect.DeepEqual(visible, saved.Job) {
		t.Errorf("identity probe public/durable mismatch: %+v %v %+v", visible, err, saved)
	}
	wantState := "partial"
	if count == 0 {
		wantState = "complete"
	}
	want := jobs.Counters{ObjectsPlanned: int64(count + 1), BytesPlanned: int64((count + 1) * len(body)), ObjectsCopied: 1, BytesCopied: int64(len(body)), ObjectsVerified: 1, ObjectsFailed: int64(count), NativeRouted: int64(count)}
	if saved.Job.State != wantState || saved.Job.Counters != want || saved.Job.StartedAt == "" || saved.Job.FinishedAt == "" {
		t.Errorf("Copy does not count each failed identity once: got=%+v want=%s %+v", saved.Job, wantState, want)
	}
	if count == 0 && saved.Job.Error != "" {
		t.Errorf("successful identity control acquired error: %q", saved.Job.Error)
	}
	srcTrace.mu.Lock()
	refused := make(map[string]int, len(srcTrace.refused))
	for name, n := range srcTrace.refused {
		refused[name] = n
	}
	srcTrace.mu.Unlock()
	if len(refused) != count {
		t.Errorf("failed source identity set incomplete: got=%d want=%d", len(refused), count)
	}
	for name := range selected {
		if refused[name] != 1 {
			t.Errorf("source GET refusal for %q occurred %d times", name, refused[name])
		}
	}
	result := audit.ResultVerifyMismatch
	if count == 0 {
		result = audit.ResultApplied
	}
	wantSummary := fmt.Sprintf("job %s · item outcomes: %d failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, %d with problems", id, count, count)
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.copy."+wantState || saved.Audit[0].Result != result || saved.Audit[0].Summary != wantSummary || saved.Audit[0].BookmarkID != h.Primary.BookmarkID || saved.Audit[0].Resource != src+"/scope/ → "+dst+"/scope/" {
		t.Errorf("identity probe audit differs: %+v", saved.Audit)
	}
	assertCopyItemRecords(t, h, id, src, dst, count, len(body), selected, saved)
	wantRows := []jobs.RecoveryRecord{{JobID: id, BookmarkID: h.Secondary.BookmarkID, Kind: jobs.RecoveryStaging, Container: dst, Prefix: "scope/.aeronautes/stage/" + id + "/"}}
	if !reflect.DeepEqual(saved.Rows, wantRows) || saved.Job.RecoveryRecords != 1 || saved.Status != (app.RecoveryStatusDTO{Jobs: 1, Records: 1}) {
		t.Errorf("identity probe recovery differs: %+v", saved)
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err == nil {
		t.Error("finished identity probe still accepts cancellation")
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeA, afterA)
	got, ok := afterB.Containers[dst].Objects["scope/z-engine.bin"]
	if !ok || got.Size != int64(len(body)) || got.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) || got.Class != "plain" {
		t.Errorf("successful engine control bytes differ: %+v", got)
	}
	if stray := collateral(beforeB, afterB, []string{dst + "/scope/z-engine.bin"}); len(stray) != 0 {
		t.Errorf("identity probe changed collateral: %v", stray)
	}
	if len(srcTrace.traffic.writeSnapshot()) != 0 {
		t.Errorf("identity probe wrote source: %v", srcTrace.traffic.writeSnapshot())
	}
	var namedInPublicSummary []string
	for name := range selected {
		found := strings.Contains(saved.Job.Error, name)
		for _, event := range saved.Audit {
			found = found || strings.Contains(event.Summary, name)
		}
		if found {
			namedInPublicSummary = append(namedInPublicSummary, name)
		}
	}
	sort.Strings(namedInPublicSummary)
	observation := struct {
		ExpectedFailed                 int
		RefusedSourceGETs              map[string]int
		NamesInExistingJobAuditSummary []string
		Saved                          copyCompletionState
	}{count, refused, namedInPublicSummary, saved}
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("copy failure identity observation: %s", encoded)
	requests := srcTrace.traffic.requestCount() + dstTrace.requestCount()
	copyCompletionReopen(t, h, id, saved)
	if requests != srcTrace.traffic.requestCount()+dstTrace.requestCount() {
		t.Error("identity probe reopen contacted remote")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
}

func copyItemRecordsCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, n := range []int{0, 1, 201, 401} {
		t.Run(fmt.Sprintf("failed_%d", n), func(t *testing.T) { copyItemRecordsRun(t, makeHarness(t), n) })
	}
}

func TestCopyItemRecordsMock(t *testing.T) {
	t.Parallel()
	copyItemRecordsCases(t, findMockHarness)
}
func TestCopyItemRecordsRealSwift(t *testing.T) {
	t.Parallel()
	copyItemRecordsCases(t, New)
}
