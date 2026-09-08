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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
)

type copyInterruptionCase struct {
	name, interruption                  string
	copied, failed, refused, beforeWork bool
}

func copyInterruptionProxy(t *testing.T, h *Harness, container string, tc copyInterruptionCase) (*swifttest.HTTPResponseBarrier, <-chan struct{}, *atomic.Int32, *uploadDeleteTrace) {
	t.Helper()
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	// This page follows y's acknowledged admission but precedes the engine
	// flush, so a failed read must leave y recorded as never dispatched.
	marker := "scope/y-engine.bin"
	if tc.beforeWork {
		marker = ""
	}
	query := url.Values{"format": {"json"}, "limit": {"1"}, "prefix": {"scope/"}}
	if marker != "" {
		query.Set("marker", marker)
	}
	selectedURL := &url.URL{Path: storage.Path + "/" + container}
	selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: http.MethodGet, EscapedPath: selectedURL.EscapedPath(), RawQuery: query.Encode(), Occurrence: 1}
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
		t.Errorf("copy interruption proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	held := barrier.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tc.failed && r.Method == http.MethodGet && r.URL.Path == storage.Path+"/"+container+"/scope/b-native\nfailed" {
			http.Error(w, "fixture earlier source read refusal", http.StatusForbidden)
			return
		}
		if tc.interruption == "error" && r.Method == selector.Method && r.URL.EscapedPath() == selector.EscapedPath && r.URL.RawQuery == selector.RawQuery {
			http.Error(w, "fixture later listing refusal", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	ended := make(chan struct{})
	var once sync.Once
	selected := &atomic.Int32{}
	trace := &uploadDeleteTrace{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.mu.Lock()
		trace.requests++
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			trace.writes = append(trace.writes, r.Method+" "+r.URL.Path)
		}
		trace.mu.Unlock()
		if r.Method == http.MethodGet && r.URL.Path == selectedURL.Path && r.URL.Query().Get("prefix") == "scope/" {
			// A small server page creates a real listing boundary after the
			// earlier native outcome without substituting the transfer report.
			r = r.Clone(r.Context())
			q := r.URL.Query()
			q.Set("limit", "1")
			r.URL.RawQuery = q.Encode()
		}
		if r.Method == selector.Method && r.URL.EscapedPath() == selector.EscapedPath && r.URL.RawQuery == selector.RawQuery {
			if selected.Add(1) == 1 {
				defer once.Do(func() { close(ended) })
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
		t.Fatal("owned source bookmark missing")
	}
	return barrier, ended, selected, trace
}

func copyInterruptionRun(t *testing.T, h *Harness, tc copyInterruptionCase) {
	t.Helper()
	src := h.Container(h.Primary, "interrupted-copy-src")
	dst := h.Container(h.Secondary, "interrupted-copy-dst")
	segments := h.Container(h.Secondary, "interrupted-copy-segments")
	outside := h.Container(h.Secondary, "interrupted-copy-outside")
	h.Seed(h.Primary, src, "unselected", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, dst, "scope/.aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	shared := []byte("shared bytes")
	h.Seed(h.Secondary, segments, "shared/0", shared, nil)
	h.Seed(h.Secondary, outside, "alias", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	slo := fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(shared), len(shared))
	h.SeedSLO(h.Secondary, outside, "slo-alias", slo)
	body := []byte("copy bytes")
	for _, name := range []string{"y-engine.bin", "z-engine.bin"} {
		h.Seed(h.Primary, src, "scope/"+name, body, nil)
	}
	if tc.copied {
		h.Seed(h.Primary, src, "scope/a-native\ncopied", body, nil)
	}
	if tc.failed {
		h.Seed(h.Primary, src, "scope/b-native\nfailed", body, nil)
	}
	if tc.refused {
		h.Seed(h.Primary, src, "scope/c-existing", []byte("replacement bytes"), nil)
		h.Seed(h.Secondary, dst, "scope/c-existing", []byte("retained bytes"), nil)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	barrier, ended, selected, srcTrace := copyInterruptionProxy(t, h, src, tc)
	dstTrace := copyCompletionProxy(t, h, h.Secondary, "")
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/", DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/"})
	if err != nil {
		t.Fatal(err)
	}
	event, err := barrier.Wait(t.Context())
	wantHTTP := http.StatusOK
	if tc.interruption == "error" {
		wantHTTP = http.StatusForbidden
	}
	if err != nil || event.StatusCode != wantHTTP || selected.Load() != 1 {
		t.Fatalf("Copy listing boundary: %+v %v selected=%d", event, err, selected.Load())
	}
	pending, err := h.Jobs.Get(t.Context(), id)
	if err != nil || pending.State != "running" {
		t.Fatalf("Copy did not reach running boundary: %+v %v", pending, err)
	}
	if tc.interruption == "cancel" {
		if err := h.Transfer.CancelJob(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	} else if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("selected Copy listing request did not end")
	}
	if tc.interruption == "cancel" {
		if barrier.Err() == nil || !strings.Contains(barrier.Err().Error(), "context canceled") {
			t.Fatalf("Copy cancellation was not observed at boundary: %v", barrier.Err())
		}
	} else if barrier.Err() != nil {
		t.Fatal(barrier.Err())
	}
	copyCompletionAwaitPins(t, h)
	saved := copyCompletionRead(t, h.database, id)
	visible, err := h.Jobs.Get(t.Context(), id)
	if err != nil || !reflect.DeepEqual(visible, saved.Job) {
		t.Errorf("Copy visible and saved result differ: %+v %v %+v", visible, err, saved)
	}
	want := jobs.Counters{}
	var live []string
	if !tc.beforeWork {
		if tc.copied {
			want.ObjectsPlanned++
			want.BytesPlanned += 10
			want.ObjectsCopied++
			want.BytesCopied += 10
			want.NativeRouted++
			live = append(live, "a-native\ncopied")
		}
		if tc.failed {
			want.ObjectsPlanned++
			want.BytesPlanned += 10
			want.ObjectsFailed++
			want.NativeRouted++
		}
		if tc.refused {
			want.ObjectsPlanned++
			want.BytesPlanned += 17
			want.OverwritesRefused++
		}
	}
	wantState := "partial"
	if tc.interruption == "none" {
		wantState = "complete"
		want.ObjectsPlanned += 2
		want.BytesPlanned += 20
		want.ObjectsCopied += 2
		want.BytesCopied += 20
		want.ObjectsVerified = want.ObjectsCopied
		live = append(live, "y-engine.bin", "z-engine.bin")
	} else if tc.beforeWork {
		wantState = "failed"
		if tc.interruption == "cancel" {
			wantState = "cancelled"
		}
	} else {
		want.ObjectsPlanned++
		want.BytesPlanned += 10
	}
	if saved.Job.State != wantState || saved.Job.Counters != want || saved.Job.StartedAt == "" || saved.Job.FinishedAt == "" {
		t.Errorf("Copy interruption lost reported outcomes: got=%+v want=%s %+v", saved.Job, wantState, want)
	}
	wantTotals := jobs.ItemTotals{Items: want.ObjectsPlanned, Planned: want.ObjectsPlanned,
		Admitted: want.ObjectsPlanned - want.OverwritesRefused, Failed: want.ObjectsFailed,
		Refused: want.OverwritesRefused, Problems: want.ObjectsFailed + want.OverwritesRefused}
	if tc.interruption != "none" {
		wantTotals.Unverified = want.ObjectsCopied
		if !tc.beforeWork {
			wantTotals.Problems++
		}
	}
	if !saved.ItemsCaptured || saved.ItemTotals != wantTotals || int64(len(saved.Items)) != wantTotals.Items {
		t.Fatalf("Copy interruption lost acknowledged facts: captured=%v totals=%+v rows=%d want=%+v", saved.ItemsCaptured, saved.ItemTotals, len(saved.Items), wantTotals)
	}
	if tc.interruption != "none" && !tc.beforeWork {
		buffered := saved.Items[len(saved.Items)-1]
		if buffered.Key != "y-engine.bin" || !buffered.Admitted || buffered.Execution == nil || buffered.Verification != nil ||
			buffered.Execution.Result != jobs.ItemNotAttempted || buffered.Execution.Effect != jobs.EffectNotAttempted || buffered.Staging == nil ||
			buffered.Staging.Object != "scope/.aeronautes/stage/"+id+"/batch/y-engine.bin" {
			t.Fatalf("planning interruption dispatched or lost acknowledged buffer: %+v", buffered)
		}
	}
	var fragments []string
	if tc.failed {
		fragments = append(fragments, "execution failed (example: b-native\nfailed:")
	}
	if tc.refused {
		fragments = append(fragments, "1 overwrite refused")
	}
	if tc.interruption != "none" && !(tc.beforeWork && tc.interruption == "cancel") {
		fragments = append(fragments, "source listing:")
		if tc.interruption == "cancel" {
			fragments = append(fragments, "source listing: Get \"", "/"+src+"?", "marker=scope%2Fy-engine.bin", "context canceled")
		} else {
			fragments = append(fragments, "HTTP 403")
		}
	}
	for _, fragment := range fragments {
		if !strings.Contains(saved.Job.Error, fragment) {
			t.Errorf("Copy interruption lost reason %q: %q", fragment, saved.Job.Error)
		}
	}
	if len(fragments) == 0 && saved.Job.Error != "" {
		t.Errorf("Copy control acquired error: %q", saved.Job.Error)
	}
	result := audit.ResultVerifyMismatch
	if wantState == "complete" {
		result = audit.ResultApplied
	} else if wantState == "failed" || wantState == "cancelled" {
		result = audit.ResultFailed
	}
	summary := "job " + id
	if tc.refused {
		summary += " · 1 overwrite refused"
	}
	summary += fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, %d refusals, 0 unresolved, %d unverified, %d with problems",
		wantTotals.Failed, wantTotals.Refused, wantTotals.Unverified, wantTotals.Problems)
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.copy."+wantState || saved.Audit[0].Result != result || saved.Audit[0].Summary != summary || saved.Audit[0].BookmarkID != h.Primary.BookmarkID || saved.Audit[0].Resource != src+"/scope/ → "+dst+"/scope/" {
		t.Errorf("Copy interruption audit lost outcome or authority: %+v", saved.Audit)
	}
	wantRows := []jobs.RecoveryRecord{{JobID: id, BookmarkID: h.Secondary.BookmarkID, Kind: jobs.RecoveryStaging, Container: dst, Prefix: "scope/.aeronautes/stage/" + id + "/"}}
	if !reflect.DeepEqual(saved.Rows, wantRows) || saved.Job.RecoveryRecords != 1 || saved.Status != (app.RecoveryStatusDTO{Jobs: 1, Records: 1}) {
		t.Errorf("Copy interruption recovery differs: %+v", saved)
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err == nil {
		t.Error("finished Copy still accepts cancellation")
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeA, afterA)
	var allowed []string
	for _, name := range live {
		identity := "scope/" + name
		allowed = append(allowed, dst+"/"+identity)
		got, ok := afterB.Containers[dst].Objects[identity]
		if !ok || got.Size != 10 || got.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) || got.Class != "plain" {
			t.Errorf("Copy selected bytes differ: %q %+v", identity, got)
		}
	}
	if stray := collateral(beforeB, afterB, allowed); len(stray) != 0 {
		t.Errorf("Copy interruption changed destination collateral: %v", stray)
	}
	if len(srcTrace.writeSnapshot()) != 0 {
		t.Errorf("Copy interruption wrote source: %v", srcTrace.writeSnapshot())
	}
	if len(live) == 0 && len(dstTrace.writeSnapshot()) != 0 {
		t.Errorf("Copy without completed work made writes: %v", dstTrace.writeSnapshot())
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("copy interruption observation: %s", encoded)
	requests := srcTrace.requestCount() + dstTrace.requestCount()
	copyCompletionReopen(t, h, id, saved)
	if requests != srcTrace.requestCount()+dstTrace.requestCount() {
		t.Error("Copy interruption reopen contacted remote")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
}

func copyInterruptionCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, tc := range []copyInterruptionCase{
		{name: "cancel_after_copy", interruption: "cancel", copied: true},
		{name: "error_after_copy", interruption: "error", copied: true},
		{name: "cancel_after_failure", interruption: "cancel", failed: true},
		{name: "error_after_failure", interruption: "error", failed: true},
		{name: "cancel_after_both", interruption: "cancel", copied: true, failed: true},
		{name: "error_after_both", interruption: "error", copied: true, failed: true},
		{name: "cancel_after_refusal_failure", interruption: "cancel", refused: true, failed: true},
		{name: "error_after_refusal_failure", interruption: "error", refused: true, failed: true},
		{name: "cancel_refusal_control", interruption: "cancel", refused: true},
		{name: "error_refusal_control", interruption: "error", refused: true},
		{name: "cancel_before_work_control", interruption: "cancel", beforeWork: true},
		{name: "error_before_work_control", interruption: "error", beforeWork: true},
		{name: "complete_control", interruption: "none", copied: true},
	} {
		t.Run(tc.name, func(t *testing.T) { copyInterruptionRun(t, makeHarness(t), tc) })
	}
}

func TestCopyInterruptionMock(t *testing.T)      { copyInterruptionCases(t, findMockHarness) }
func TestCopyInterruptionRealSwift(t *testing.T) { copyInterruptionCases(t, New) }
