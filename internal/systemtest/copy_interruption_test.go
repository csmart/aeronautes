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
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
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
	pending, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
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
	visible, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
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
	// An object's second record is written with its outcome (ADR 0122
	// decision 7), so what was copied before the interruption is settled.
	want.ObjectsVerified = want.ObjectsCopied
	if saved.Job.State != wantState || saved.Job.Counters != want || saved.Job.StartedAt == "" || saved.Job.FinishedAt == "" {
		t.Errorf("Copy interruption lost reported outcomes: got=%+v want=%s %+v", saved.Job, wantState, want)
	}
	wantTotals := jobs.ItemTotals{Items: want.ObjectsPlanned, Planned: want.ObjectsPlanned,
		Admitted: want.ObjectsPlanned - want.OverwritesRefused, Failed: want.ObjectsFailed,
		Refused: want.OverwritesRefused, Problems: want.ObjectsFailed + want.OverwritesRefused}
	if tc.interruption != "none" && !tc.beforeWork {
		wantTotals.Problems++
	}
	if !saved.ItemsCaptured || saved.ItemTotals != wantTotals || int64(len(saved.Items)) != wantTotals.Items {
		t.Fatalf("Copy interruption lost acknowledged facts: captured=%v totals=%+v rows=%d want=%+v", saved.ItemsCaptured, saved.ItemTotals, len(saved.Items), wantTotals)
	}
	if tc.interruption != "none" && !tc.beforeWork {
		buffered := saved.Items[len(saved.Items)-1]
		if buffered.Key != "y-engine.bin" || !buffered.Admitted || buffered.Execution == nil || buffered.Verification != nil ||
			buffered.Execution.Result != jobs.ItemNotAttempted || buffered.Execution.Effect != jobs.EffectNotAttempted {
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
	summary += fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, %d refusals, 0 unresolved, 0 unverified, %d with problems",
		wantTotals.Failed, wantTotals.Refused, wantTotals.Problems)
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.copy."+wantState || saved.Audit[0].Result != result || saved.Audit[0].Summary != summary || saved.Audit[0].BookmarkID != h.Primary.BookmarkID || saved.Audit[0].Resource != src+"/scope/ → "+dst+"/scope/" {
		t.Errorf("Copy interruption audit lost outcome or authority: %+v", saved.Audit)
	}
	if len(saved.Rows) != 0 || saved.Job.RecoveryRecords != 0 || saved.Status != (app.RecoveryStatusDTO{}) {
		t.Errorf("an interrupted copy of plain objects left recovery records: %+v", saved)
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

// copyCancelledWhileTheEngineRuns cancels a copy while the engine holds two
// names: it has reported the first, and the store has accepted the second
// but its answer has not come back. An interrupted job leaves what it had
// written (ADR 0122 decision 8): the reported object is whole at its name
// with the headers the application carries, and the other is recorded as
// unknown, because the engine stopped without saying anything about it.
func copyCancelledWhileTheEngineRuns(t *testing.T, h *Harness) {
	t.Helper()
	src := h.Container(h.Primary, "cancelled-engine-src")
	dst := h.Container(h.Secondary, "cancelled-engine-dst")
	body := []byte("copy bytes")
	h.Seed(h.Primary, src, "scope/y-reported.bin", body, map[string]string{"X-Object-Meta-Team": "ops"})
	h.Seed(h.Primary, src, "scope/z-unanswered.bin", body, nil)
	h.Seed(h.Secondary, dst, "unselected", []byte("destination sentinel"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	storage, err := url.Parse(h.Secondary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	held := &url.URL{Path: storage.Path + "/" + dst + "/scope/z-unanswered.bin"}
	barrier, err := swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
		Operation: t.Name(), Method: http.MethodPut, EscapedPath: held.EscapedPath(), Occurrence: 1}, false, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	srcTrace := copyCompletionProxy(t, h, h.Primary, "")
	copyCompletionProxy(t, h, h.Secondary, "", barrier)
	// One transfer at a time, so the engine has finished and reported the
	// first name before it sends the second.
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/", Settings: app.TransferSettingsDTO{Transfers: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusCreated {
		t.Fatalf("the second name's write did not reach the store: %+v %v", event, err)
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	copyCompletionAwaitPins(t, h)
	saved := copyCompletionRead(t, h.database, id)
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)

	// What the store holds, read apart from the job.
	reported, unanswered := afterB.Containers[dst].Objects["scope/y-reported.bin"], afterB.Containers[dst].Objects["scope/z-unanswered.bin"]
	for name, object := range map[string]oracle.ObjectState{"y-reported.bin": reported, "z-unanswered.bin": unanswered} {
		if object.Class != "plain" || object.Size != int64(len(body)) || object.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) {
			t.Fatalf("scope/%s is not whole at its name after the cancel: %+v", name, object)
		}
	}
	if reported.Meta["Team"] != "ops" {
		t.Fatalf("the cancel left a copied object without its metadata: %+v", reported.Meta)
	}
	h.AssertNoCollateral(beforeA, afterA)
	h.AssertNoCollateral(beforeB, afterB, dst+"/scope/y-reported.bin", dst+"/scope/z-unanswered.bin")
	if len(srcTrace.writeSnapshot()) != 0 {
		t.Fatalf("the copy wrote to its source: %v", srcTrace.writeSnapshot())
	}

	// What the job says: one copied, one unknown, and the job partial.
	wantCounters := jobs.Counters{ObjectsPlanned: 2, BytesPlanned: 20, ObjectsCopied: 1, BytesCopied: 10, ObjectsVerified: 1, ObjectsFailed: 1}
	wantTotals := jobs.ItemTotals{Items: 2, Planned: 2, Admitted: 2, Failed: 1, Unresolved: 1, Problems: 1}
	if saved.Job.State != "partial" || saved.Job.Counters != wantCounters || !saved.ItemsCaptured || saved.ItemTotals != wantTotals || len(saved.Items) != 2 ||
		!strings.Contains(saved.Job.Error, "the engine stopped without reporting this object") {
		t.Fatalf("a copy cancelled while the engine ran reported: job=%+v totals=%+v", saved.Job, saved.ItemTotals)
	}
	first, second := saved.Items[0], saved.Items[1]
	if first.Key != "y-reported.bin" || first.Execution == nil ||
		first.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: 10}) ||
		first.Verification == nil || first.Verification.Result != jobs.ItemVerified {
		t.Fatalf("the object the engine reported was not recorded as written: %+v", first)
	}
	if second.Key != "z-unanswered.bin" || second.Execution == nil || second.Verification != nil ||
		second.Execution.Result != jobs.ItemFailed || second.Execution.Effect != jobs.EffectUnresolved || second.Execution.Diagnostic.Stage != jobs.StageEngine {
		t.Fatalf("the object the engine said nothing about was not recorded as unknown: %+v", second)
	}
	wantSummary := "job " + id + " · item outcomes: 1 failed names, 0 conflicts, 0 refusals, 1 unresolved, 0 unverified, 1 with problems"
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.copy.partial" || saved.Audit[0].Summary != wantSummary {
		t.Fatalf("the terminal audit differs: %+v", saved.Audit)
	}
}

// copyOfANameThatLeftTheSource removes a source object after the copy listed
// and read it and before the engine is given it. Measured on the pinned
// engine, a listed name that is no longer at the source gets no line and no
// error, and the engine ends cleanly. An object's outcome is what the engine
// said (ADR 0122 decision 4): nothing said by an engine that did not fail is
// an object that was not copied, and the job says so.
func copyOfANameThatLeftTheSource(t *testing.T, h *Harness) {
	t.Helper()
	src := h.Container(h.Primary, "left-source-src")
	dst := h.Container(h.Secondary, "left-source-dst")
	body := []byte("copy bytes")
	for _, name := range []string{"y-engine.bin", "z-engine.bin"} {
		h.Seed(h.Primary, src, "scope/"+name, body, nil)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	// The listing page after y is held: y has been read and waits for the
	// engine, and z has not been listed yet.
	barrier, _, _, srcTrace := copyInterruptionProxy(t, h, src, copyInterruptionCase{interruption: "none"})
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/", DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/"})
	if err != nil {
		t.Fatal(err)
	}
	if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
		t.Fatalf("the listing page after the first name: %+v %v", event, err)
	}
	if status := h.rawDeleteChecked(h.Primary, "/"+src+"/scope/y-engine.bin"); status/100 != 2 {
		t.Fatalf("removing the source object answered %d", status)
	}
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h)
	saved := copyCompletionRead(t, h.database, id)

	wantCounters := jobs.Counters{ObjectsPlanned: 2, BytesPlanned: 20, ObjectsCopied: 1, BytesCopied: 10, ObjectsVerified: 1, ObjectsFailed: 1}
	wantTotals := jobs.ItemTotals{Items: 2, Planned: 2, Admitted: 2, Failed: 1, Problems: 1}
	if saved.Job.State != "partial" || saved.Job.Counters != wantCounters || saved.ItemTotals != wantTotals || len(saved.Items) != 2 ||
		!strings.Contains(saved.Job.Error, "y-engine.bin") || !strings.Contains(saved.Job.Error, "it may no longer be at the source") {
		t.Fatalf("a copy whose source lost an object reported: job=%+v totals=%+v", saved.Job, saved.ItemTotals)
	}
	gone, copied := saved.Items[0], saved.Items[1]
	if gone.Key != "y-engine.bin" || gone.Execution == nil || gone.Verification != nil ||
		gone.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeIncomplete}}) {
		t.Fatalf("the object that left the source was recorded as %+v", gone)
	}
	if copied.Key != "z-engine.bin" || copied.Execution == nil || copied.Execution.Result != jobs.ItemCopied || copied.Verification == nil {
		t.Fatalf("the object still at the source was recorded as %+v", copied)
	}
	// Only this test's own delete changed the source, and the destination
	// gained the one object that was copied.
	afterB := h.SnapshotNamespace(h.Secondary)
	if got := afterB.Containers[dst].Objects["scope/z-engine.bin"]; got.Size != 10 || got.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) {
		t.Fatalf("the copied object differs: %+v", got)
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary), src+"/scope/y-engine.bin")
	h.AssertNoCollateral(beforeB, afterB, dst+"/scope/z-engine.bin")
	if len(srcTrace.writeSnapshot()) != 0 {
		t.Fatalf("the copy wrote to its source: %v", srcTrace.writeSnapshot())
	}
}

func copyInterruptionCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Run("cancel_while_the_engine_runs", func(t *testing.T) { copyCancelledWhileTheEngineRuns(t, makeHarness(t)) })
	t.Run("source_object_removed_before_the_engine_runs", func(t *testing.T) { copyOfANameThatLeftTheSource(t, makeHarness(t)) })
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

func TestCopyInterruptionMock(t *testing.T) {
	t.Parallel()
	copyInterruptionCases(t, findMockHarness)
}
func TestCopyInterruptionRealSwift(t *testing.T) {
	t.Parallel()
	copyInterruptionCases(t, New)
}
