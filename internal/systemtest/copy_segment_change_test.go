package systemtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

// segmentChangeBody is larger than the admitted one-mebibyte chunk
// setting used below, so the engine publishes the destination as a
// dynamic large object through the normal service.
var segmentChangeBody = []byte(strings.Repeat("abcdefg", 160000))

const (
	segmentChangeStampKey       = "Aeronautes-Staged-Aggregate"
	segmentChangeFingerprintKey = "Aeronautes-Fingerprint"
)

// segmentChangeWrite is one observed mutation with the headers this
// slice depends on, which a method and path alone cannot establish.
type segmentChangeWrite struct {
	Method      string
	Path        string
	IfNoneMatch string
	Stamp       string
	MetaEntries int
}

type segmentChangeTrace struct {
	mu     sync.Mutex
	writes []segmentChangeWrite
}

func (trace *segmentChangeTrace) snapshot() []segmentChangeWrite {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]segmentChangeWrite(nil), trace.writes...)
}

func (trace *segmentChangeTrace) writesTo(suffix string) []segmentChangeWrite {
	var out []segmentChangeWrite
	for _, write := range trace.snapshot() {
		if strings.HasSuffix(write.Path, suffix) {
			out = append(out, write)
		}
	}
	return out
}

// segmentChangeProxy records every mutation an account receives with the
// conditional header, the publication stamp and the user metadata count
// carried on it.
func segmentChangeProxy(t *testing.T, h *Harness, account *Account) *segmentChangeTrace {
	t.Helper()
	storage, err := url.Parse(account.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 30 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("segment change proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	trace := &segmentChangeTrace{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			entries := 0
			for name := range r.Header {
				if strings.HasPrefix(http.CanonicalHeaderKey(name), "X-Object-Meta-") {
					entries++
				}
			}
			trace.mu.Lock()
			trace.writes = append(trace.writes, segmentChangeWrite{
				Method: r.Method, Path: r.URL.Path,
				IfNoneMatch: r.Header.Get("If-None-Match"),
				Stamp:       r.Header.Get("X-Object-Meta-" + segmentChangeStampKey),
				MetaEntries: entries,
			})
			trace.mu.Unlock()
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	list, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range list {
		if bookmark.ID != account.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, account.key); err != nil {
			t.Fatal(err)
		}
		return trace
	}
	t.Fatal("owned segment-change bookmark missing")
	return nil
}

// segmentChangeMaxMeta reads the destination's advertised user metadata
// allowance rather than assuming any particular deployment's value.
func segmentChangeMaxMeta(t *testing.T, h *Harness) int {
	t.Helper()
	root, err := url.Parse(h.Secondary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	root.Path, root.RawPath, root.RawQuery = "/info", "", ""
	response, err := http.Get(root.String())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Swift struct {
			MaxMetaCount int `json:"max_meta_count"`
		} `json:"swift"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatal(err)
	}
	if info.Swift.MaxMetaCount <= 0 {
		t.Fatalf("destination advertises no metadata allowance: %s", body)
	}
	return info.Swift.MaxMetaCount
}

type segmentChangeScope struct {
	src, dst, segments string
}

func segmentChangeScope0(t *testing.T, h *Harness, label string) segmentChangeScope {
	t.Helper()
	scope := segmentChangeScope{
		src: h.Container(h.Primary, label+"-src"),
		dst: h.Container(h.Secondary, label+"-dst"),
	}
	scope.segments = scope.dst + "_segments"
	h.rawPut(h.Secondary, "/"+scope.segments, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, scope.segments) })
	h.Seed(h.Primary, scope.src, "unselected", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, scope.dst, "unselected", []byte("destination sentinel"), nil)
	return scope
}

func segmentChangeRun(t *testing.T, h *Harness, scope segmentChangeScope) (string, copyCompletionState) {
	t.Helper()
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: scope.src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: scope.dst, DestPrefix: "scope/",
		Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	copyCompletionAwaitPins(t, h)
	return id, copyCompletionRead(t, h.database, id)
}

func segmentChangeLive(t *testing.T, h *Harness, scope segmentChangeScope) oracle.ObjectState {
	t.Helper()
	live, ok := h.SnapshotNamespace(h.Secondary).Containers[scope.dst].Objects["scope/asset"]
	if !ok {
		t.Fatalf("destination %s/scope/asset is absent", scope.dst)
	}
	return live
}

// segmentChangeAudit asserts the one terminal audit event a copy must
// persist, which a missing or renamed operation has to fail.
func segmentChangeAudit(t *testing.T, h *Harness, saved copyCompletionState, scope segmentChangeScope, id, operation string, result audit.Result, summary string) {
	t.Helper()
	if len(saved.Audit) != 1 {
		t.Fatalf("want exactly one terminal audit event, got %d: %+v", len(saved.Audit), saved.Audit)
	}
	event := saved.Audit[0]
	if event.Operation != operation || event.Result != result ||
		event.BookmarkID != h.Primary.BookmarkID ||
		event.Resource != scope.src+"/scope/ → "+scope.dst+"/scope/" ||
		event.Summary != "job "+id+summary {
		t.Fatalf("terminal audit differs: %+v", event)
	}
}

func segmentChangeItem(t *testing.T, h *Harness, saved copyCompletionState, scope segmentChangeScope) jobs.ItemRecord {
	t.Helper()
	if len(saved.Items) != 1 {
		t.Fatalf("want exactly one recorded item, got %d: %+v", len(saved.Items), saved.Items)
	}
	item := saved.Items[0]
	if item.Key != "asset" ||
		item.Source.Object != "scope/asset" || item.Source.BookmarkID != h.Primary.BookmarkID || item.Source.Container != scope.src ||
		item.Destination.Object != "scope/asset" || item.Destination.BookmarkID != h.Secondary.BookmarkID || item.Destination.Container != scope.dst {
		t.Fatalf("recorded item identity differs: %+v", item)
	}
	return item
}

// segmentChangePublish publishes one segmented destination and returns
// the trace of the destination's mutations.
func segmentChangePublish(t *testing.T, h *Harness, scope segmentChangeScope, meta map[string]string) (*segmentChangeTrace, oracle.ObjectState, *oracle.AccountSnapshot) {
	t.Helper()
	h.Seed(h.Primary, scope.src, "scope/asset", segmentChangeBody, meta)
	sourceBefore := h.SnapshotNamespace(h.Primary)
	destinationBefore := h.SnapshotNamespace(h.Secondary)
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	trace := segmentChangeProxy(t, h, h.Secondary)
	id, saved := segmentChangeRun(t, h, scope)

	// The whole publication outcome is asserted, not a subset: a job that
	// silently stopped recording its terminal audit or its item facets
	// must fail here.
	want := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: int64(len(segmentChangeBody)),
		ObjectsCopied: 1, BytesCopied: int64(len(segmentChangeBody)), ObjectsVerified: 1}
	if saved.Job.State != "complete" || saved.Job.Counters != want || saved.Job.Error != "" {
		t.Fatalf("publication job differs: state=%q error=%q counters=%+v", saved.Job.State, saved.Job.Error, saved.Job.Counters)
	}
	item := segmentChangeItem(t, h, saved, scope)
	if item.Execution == nil || item.Verification == nil || !item.Planned || !item.Admitted || item.Staging == nil ||
		item.Execution.ItemObservation.Result != jobs.ItemCopied ||
		item.Execution.ItemObservation.CopiedBytes != int64(len(segmentChangeBody)) ||
		item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
		t.Fatalf("publication item observation differs: execution=%+v verification=%+v", item.Execution, item.Verification)
	}
	segmentChangeAudit(t, h, saved, scope, id, "transfer.copy.complete", audit.ResultApplied,
		" · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems")

	live := segmentChangeLive(t, h, scope)
	if live.Class != "dlo" || len(live.Segments) < 2 || live.Size != int64(len(segmentChangeBody)) {
		t.Fatalf("destination was not published as a segmented object: class=%s segments=%d size=%d", live.Class, len(live.Segments), live.Size)
	}
	if live.Digest != sourceBefore.Containers[scope.src].Objects["scope/asset"].Digest {
		t.Fatal("published body differs from the source")
	}
	// Every preserved value arrives unchanged, and only this executor's
	// own keys are added.
	for key, value := range meta {
		name := strings.TrimPrefix(key, "X-Object-Meta-")
		if live.Meta[name] != value {
			t.Fatalf("preserved metadata %q = %q, want %q", name, live.Meta[name], value)
		}
	}
	for name := range live.Meta {
		if name == segmentChangeFingerprintKey || name == segmentChangeStampKey {
			continue
		}
		if _, seeded := meta["X-Object-Meta-"+name]; !seeded {
			t.Fatalf("publication invented metadata %q", name)
		}
	}
	if live.Meta[segmentChangeFingerprintKey] == "" {
		t.Fatal("publication carries no fingerprint")
	}
	// Destination collateral: only the published name and its segments
	// may be new, and the sentinel must be untouched.
	allowed := []string{scope.dst + "/scope/asset"}
	for _, segment := range live.Segments {
		allowed = append(allowed, segment.Name)
	}
	h.AssertNoCollateral(destinationBefore, h.SnapshotNamespace(h.Secondary), allowed...)
	h.AssertNoCollateral(sourceBefore, h.SnapshotNamespace(h.Primary))
	return trace, live, sourceBefore
}

func copySegmentChangePublication(t *testing.T, h *Harness) {
	t.Helper()
	scope := segmentChangeScope0(t, h, "segchange-pub")
	trace, live, beforeA := segmentChangePublish(t, h, scope, map[string]string{"X-Object-Meta-Kept": "value"})

	// The stamp rides the one conditional publishing write, and no
	// metadata update follows it.
	writes := trace.writesTo("/" + scope.dst + "/scope/asset")
	if len(writes) != 1 {
		t.Fatalf("the live name took %d writes, want exactly one: %+v", len(writes), writes)
	}
	publish := writes[0]
	if publish.Method != http.MethodPut || publish.IfNoneMatch != "*" {
		t.Fatalf("publication was not the conditional write: %+v", publish)
	}
	if !strings.HasPrefix(publish.Stamp, "v1:") {
		t.Fatalf("the publishing request carried no stamp: %+v", publish)
	}
	if publish.Stamp != live.Meta[segmentChangeStampKey] {
		t.Fatalf("published stamp %q differs from the request's %q", live.Meta[segmentChangeStampKey], publish.Stamp)
	}
	if live.Meta["Kept"] != "value" || live.Meta[segmentChangeFingerprintKey] == "" {
		t.Fatalf("publication lost preserved metadata or the fingerprint: %+v", live.Meta)
	}
	if live.Digest != beforeA.Containers[scope.src].Objects["scope/asset"].Digest {
		t.Fatal("published body differs from the source")
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
}

func copySegmentChangeUnchanged(t *testing.T, h *Harness) {
	t.Helper()
	scope := segmentChangeScope0(t, h, "segchange-same")
	segmentChangePublish(t, h, scope, nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	trace := segmentChangeProxy(t, h, h.Secondary)
	id, saved := segmentChangeRun(t, h, scope)

	if saved.Job.State != "complete" ||
		saved.Job.Counters != (jobs.Counters{ObjectsPlanned: 1, BytesPlanned: int64(len(segmentChangeBody)), ObjectsVerified: 1}) {
		t.Fatalf("unchanged job differs: state=%q counters=%+v", saved.Job.State, saved.Job.Counters)
	}
	item := segmentChangeItem(t, h, saved, scope)
	// The listing cannot resolve a manifest destination, so the item is
	// planned and admitted and holds a staging intent; the executor then
	// resolves it to the same by HEAD without writing anything.
	if item.Execution == nil || item.Verification == nil || !item.Planned || !item.Admitted || item.Staging == nil ||
		item.ListedBytes != int64(len(segmentChangeBody)) ||
		item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemSame, Effect: jobs.EffectNotAttempted}) ||
		item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
		t.Fatalf("unchanged item observation differs: %+v", item)
	}
	segmentChangeAudit(t, h, saved, scope, id, "transfer.copy.complete", audit.ResultApplied,
		" · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems")
	if writes := trace.snapshot(); len(writes) != 0 {
		t.Fatalf("unchanged run mutated the destination: %+v", writes)
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}

// segmentChangeRefusal runs one already-published scope whose
// destination has been made unfit for a skip, and asserts the complete
// refusal outcome with no mutation anywhere.
func segmentChangeRefusal(t *testing.T, h *Harness, scope segmentChangeScope, why string) {
	t.Helper()
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	trace := segmentChangeProxy(t, h, h.Secondary)
	id, saved := segmentChangeRun(t, h, scope)

	if saved.Job.State != "partial" || saved.Job.Counters.OverwritesRefused != 1 ||
		saved.Job.Counters.Overwrites != 0 || saved.Job.Counters.ObjectsCopied != 0 ||
		saved.Job.Counters.ObjectsVerified != 0 || saved.Job.Counters.ObjectsFailed != 0 {
		t.Fatalf("%s: refusal job differs: state=%q counters=%+v", why, saved.Job.State, saved.Job.Counters)
	}
	item := segmentChangeItem(t, h, saved, scope)
	if item.Execution == nil || item.Execution.ItemObservation.Result != jobs.ItemRefused ||
		item.Execution.ItemObservation.Effect != jobs.EffectNotAttempted {
		t.Fatalf("%s: refusal observation differs: %+v", why, item.Execution)
	}
	segmentChangeAudit(t, h, saved, scope, id, "transfer.copy.partial", audit.ResultVerifyMismatch,
		" · 1 overwrite refused · item outcomes: 0 failed names, 0 conflicts, 1 refusals, 0 unresolved, 0 unverified, 1 with problems")
	// A refusal writes nothing anywhere in the destination account, so
	// even a staging object created and removed before the final
	// snapshot is caught here.
	if writes := trace.snapshot(); len(writes) != 0 {
		t.Fatalf("%s: refusal mutated the destination: %+v", why, writes)
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}

func copySegmentChangeSubstituted(t *testing.T, h *Harness) {
	t.Helper()
	scope := segmentChangeScope0(t, h, "segchange-swap")
	_, live, _ := segmentChangePublish(t, h, scope, nil)
	target := live.Segments[0]
	h.Seed(h.Secondary, scope.segments, strings.TrimPrefix(target.Name, scope.segments+"/"),
		[]byte(strings.Repeat("z", int(target.Size))), nil)
	if changed := segmentChangeLive(t, h, scope); changed.Digest == live.Digest {
		t.Fatal("the substitution did not change the assembled bytes")
	}
	segmentChangeRefusal(t, h, scope, "substituted segment")
}

// segmentChangeRepublish rewrites the published manifest with exactly
// the metadata given, which is how a destination is made unfit for a
// skip without disturbing anything else.
func segmentChangeRepublish(t *testing.T, h *Harness, scope segmentChangeScope, live oracle.ObjectState, meta map[string]string) {
	t.Helper()
	headers := map[string]string{"X-Object-Manifest": live.Manifest}
	for key, value := range meta {
		headers["X-Object-Meta-"+key] = value
	}
	h.Seed(h.Secondary, scope.dst, "scope/asset", nil, headers)
	after := segmentChangeLive(t, h, scope)
	if after.Class != "dlo" || after.Digest != live.Digest {
		t.Fatalf("republishing changed the destination: %+v", after)
	}
}

func copySegmentChangeStrippedStamp(t *testing.T, h *Harness) {
	t.Helper()
	scope := segmentChangeScope0(t, h, "segchange-nostamp")
	_, live, _ := segmentChangePublish(t, h, scope, nil)
	// Everything else that admits a skip stays exactly as published; only
	// the aggregate stamp is removed, so the run reaches this guard and
	// not the older fingerprint one.
	segmentChangeRepublish(t, h, scope, live, map[string]string{
		segmentChangeFingerprintKey: live.Meta[segmentChangeFingerprintKey],
	})
	if stripped := segmentChangeLive(t, h, scope); stripped.Meta[segmentChangeStampKey] != "" ||
		stripped.Meta[segmentChangeFingerprintKey] != live.Meta[segmentChangeFingerprintKey] {
		t.Fatalf("the stripped destination is not otherwise identical: %+v", stripped.Meta)
	}
	segmentChangeRefusal(t, h, scope, "stamp removed")
}

func copySegmentChangeMalformedStamp(t *testing.T, h *Harness) {
	t.Helper()
	scope := segmentChangeScope0(t, h, "segchange-badstamp")
	_, live, _ := segmentChangePublish(t, h, scope, nil)
	segmentChangeRepublish(t, h, scope, live, map[string]string{
		segmentChangeFingerprintKey: live.Meta[segmentChangeFingerprintKey],
		segmentChangeStampKey:       "v1:" + fmt.Sprint(live.Size) + ":not-a-digest",
	})
	segmentChangeRefusal(t, h, scope, "malformed stamp")
}

func copySegmentChangeStaticLargeObject(t *testing.T, h *Harness) {
	t.Helper()
	scope := segmentChangeScope0(t, h, "segchange-slo")
	_, live, _ := segmentChangePublish(t, h, scope, nil)
	// Republish the same bytes as a static large object over the same
	// segments, carrying the same fingerprint and stamp, so the only
	// reason a skip can be refused is the destination class.
	var entries []string
	for _, segment := range live.Segments {
		entries = append(entries, fmt.Sprintf(`{"path":"/%s","size_bytes":%d}`, segment.Name, segment.Size))
	}
	h.rawPutQuery(h.Secondary, "/"+scope.dst+"/"+oracle.Escape("scope/asset"), "multipart-manifest=put",
		[]byte("["+strings.Join(entries, ",")+"]"), map[string]string{
			"X-Object-Meta-" + segmentChangeFingerprintKey: live.Meta[segmentChangeFingerprintKey],
			"X-Object-Meta-" + segmentChangeStampKey:       live.Meta[segmentChangeStampKey],
		})
	slo := segmentChangeLive(t, h, scope)
	if slo.Class != "slo" || slo.Size != live.Size || slo.Digest != live.Digest {
		t.Fatalf("the static large object does not hold the same bytes: %+v", slo)
	}
	if slo.Meta[segmentChangeFingerprintKey] != live.Meta[segmentChangeFingerprintKey] ||
		slo.Meta[segmentChangeStampKey] != live.Meta[segmentChangeStampKey] {
		t.Fatalf("the static large object lost a skip precondition: %+v", slo.Meta)
	}
	segmentChangeRefusal(t, h, scope, "static large object")
}

func copySegmentChangeMetadataAllowance(t *testing.T, h *Harness) {
	t.Helper()
	allowance := segmentChangeMaxMeta(t, h)

	// Below the allowance: the stamp fits beside the fingerprint and
	// every preserved user value, and the copy carries it.
	below := segmentChangeScope0(t, h, "segchange-metafit")
	fits := map[string]string{}
	for i := range allowance - 2 {
		fits[fmt.Sprintf("X-Object-Meta-Pad%03d", i)] = "v"
	}
	_, live, _ := segmentChangePublish(t, h, below, fits)
	if live.Meta[segmentChangeStampKey] == "" {
		t.Fatalf("the stamp was dropped although it fits within %d entries: %d preserved", allowance, len(live.Meta))
	}

	// At the allowance: the stamp cannot fit without displacing a
	// preserved value, so the copy still succeeds and simply carries no
	// stamp. The destination is then refused rather than skipped.
	full := segmentChangeScope0(t, h, "segchange-metafull")
	crowded := map[string]string{}
	for i := range allowance - 1 {
		crowded[fmt.Sprintf("X-Object-Meta-Pad%03d", i)] = "v"
	}
	_, crowdedLive, _ := segmentChangePublish(t, h, full, crowded)
	if crowdedLive.Meta[segmentChangeStampKey] != "" {
		t.Fatalf("the stamp was added past the advertised allowance of %d", allowance)
	}
	if crowdedLive.Meta[segmentChangeFingerprintKey] == "" {
		t.Fatal("the fingerprint was displaced to make room")
	}
	for key, value := range crowded {
		name := strings.TrimPrefix(key, "X-Object-Meta-")
		if crowdedLive.Meta[name] != value {
			t.Fatalf("preserved metadata %q = %q, want %q", name, crowdedLive.Meta[name], value)
		}
	}
	segmentChangeRefusal(t, h, full, "unstamped because the allowance was full")
}

// copySegmentChangeScenarios runs every scenario the fixture can
// express. staticLargeObjects is false for the repository mock, which
// does not model creating a static large object over HTTP, so that
// destination class is covered against the owned service only and at
// the predicate in the copy package.
func copySegmentChangeScenarios(t *testing.T, h *Harness, staticLargeObjects bool) {
	t.Helper()
	copySegmentChangePublication(t, h)
	copySegmentChangeUnchanged(t, h)
	copySegmentChangeSubstituted(t, h)
	copySegmentChangeStrippedStamp(t, h)
	copySegmentChangeMalformedStamp(t, h)
	if staticLargeObjects {
		copySegmentChangeStaticLargeObject(t, h)
	}
	copySegmentChangeMetadataAllowance(t, h)
}

func TestCopySegmentChangeDetectionMock(t *testing.T) {
	t.Parallel()
	copySegmentChangeScenarios(t, findMockHarness(t), false)
}

func TestCopySegmentChangeDetectionRealSwift(t *testing.T) {
	t.Parallel()
	copySegmentChangeScenarios(t, New(t), true)
}
