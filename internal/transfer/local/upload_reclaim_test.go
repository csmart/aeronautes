package local

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// replacingItem is the item a replacing preflight admits for one live
// name, routed natively so the publication is this package's own.
func replacingItem(t *testing.T, s *swiftcontrol.Session, path string) UploadItem {
	t.Helper()
	preflight, err := PreflightUploadReplacing(t.Context(), s, "media", "docs/", []string{path})
	if err != nil || preflight.Rejected() || len(preflight.Items) != 1 || !preflight.Items[0].Replaces {
		t.Fatalf("the replacing preflight did not admit the live name: %+v %v", preflight, err)
	}
	item := preflight.Items[0]
	item.Route = "native"
	return item
}

// publishReplacement is the native route's own order: record what is
// there, publish, then reclaim. It returns what reclamation reported so a
// case can assert on the error the route would have propagated.
func publishReplacement(t *testing.T, s *swiftcontrol.Session, run *uploadRun, item UploadItem, path string) error {
	t.Helper()
	present, err := run.observePredecessor(t.Context(), s, "media", item)
	if err != nil {
		t.Fatalf("observing the predecessor: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	result := nativeUpload(t.Context(), s, "media", item, file, present)
	return run.reclaim(t.Context(), s, item, result)
}

// reclaimRecorder is a recorder that answers with a fixed plan and keeps
// what it was told about the outcome.
type reclaimRecorder struct {
	plan      ReclaimPlan
	planErr   error
	asked     int
	outcomes  []ReclaimOutcome
	observed  []ObservedPredecessor
	recordErr error
}

func (r *reclaimRecorder) recorder() *UploadRecorder {
	return &UploadRecorder{
		Replacing: func(_ UploadItem, observed ObservedPredecessor) error {
			r.observed = append(r.observed, observed)
			return nil
		},
		Reclaiming: func(UploadItem) (ReclaimPlan, error) {
			r.asked++
			return r.plan, r.planErr
		},
		Reclaimed: func(_ UploadItem, outcome ReclaimOutcome) error {
			r.outcomes = append(r.outcomes, outcome)
			return r.recordErr
		},
	}
}

func liveNames(t *testing.T, s *swiftcontrol.Session, container, prefix string) []string {
	t.Helper()
	var names []string
	for entry, err := range s.Objects(t.Context(), container, swiftcontrol.ObjectListOptions{Prefix: prefix, Deep: true}) {
		if err != nil {
			t.Fatalf("listing %s/%s: %v", container, prefix, err)
		}
		object, ok := entry.(swiftcontrol.ObjectEntry)
		if !ok {
			continue
		}
		names = append(names, object.Name)
	}
	slices.Sort(names)
	return names
}

func bulkDeletes(log []string) int {
	count := 0
	for _, line := range log {
		if strings.Contains(line, "bulk-delete") {
			count++
		}
	}
	return count
}

func singleDeletes(log []string) int {
	count := 0
	for _, line := range log {
		if strings.HasPrefix(line, "DELETE ") {
			count++
		}
	}
	return count
}

// ADR 0081 decision 5: reclamation removes exactly the fragments the plan
// names, in one bulk request for the container that holds them, and
// nothing else in the account moves.
func TestReclaimingRemovesExactlyThePlannedFragments(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")
	seed(mock, "media_segments", "parts/0002", "89abcdef")
	// A third object under the same prefix that the plan does not name.
	// Reclamation works from recorded identities, never from a prefix, so
	// this one has to survive.
	seed(mock, "media_segments", "parts/0003", "unnamed")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	keeper := &reclaimRecorder{plan: ReclaimPlan{Fragments: []Fragment{
		{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole},
		{Container: "media_segments", Name: "parts/0002", Shape: FragmentWhole},
	}}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	before := len(mock.RequestLog())
	if err := publishReplacement(t, s, run, item, path); err != nil {
		t.Fatalf("the replacement did not complete: %v", err)
	}

	if keeper.asked != 1 || len(keeper.outcomes) != 1 {
		t.Fatalf("reclamation was not asked and recorded exactly once: %+v", keeper)
	}
	outcome := keeper.outcomes[0]
	if outcome.Declined != "" || len(outcome.Failed) != 0 || outcome.Untrusted != "" || len(outcome.Removed) != 2 {
		t.Fatalf("the outcome does not say both fragments went: %+v", outcome)
	}
	if got := liveNames(t, s, "media_segments", "parts/"); !slices.Equal(got, []string{"parts/0003"}) {
		t.Fatalf("reclamation removed the wrong objects: %v", got)
	}
	// One request for the one container, and no single-object DELETE: the
	// per-object path is a different contract with different accounting.
	log := mock.RequestLog()[before:]
	if bulkDeletes(log) != 1 || singleDeletes(log) != 0 {
		t.Fatalf("reclamation did not issue one bulk delete: %v", log)
	}
}

// Rule 1: the replacement is published and verified BEFORE anything is
// removed. A publication whose verification failed reclaims nothing,
// because the bytes that would replace the removed ones are not known to
// be there.
func TestReclaimingWaitsForAVerifiedReplacement(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	keeper := &reclaimRecorder{plan: ReclaimPlan{Fragments: []Fragment{
		{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole}}}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	present, err := run.observePredecessor(t.Context(), s, "media", item)
	if err != nil {
		t.Fatalf("observing the predecessor: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	// The PUT lands and the verify read fails: the publication's own
	// outcome is unresolved, which is exactly when reclaiming would be a
	// guess about which bytes survive.
	mock.FailNextObjectHEADs(1)
	result := nativeUpload(t.Context(), s, "media", item, file, present)
	if result.Result == transfercopy.ResultCopied {
		t.Fatalf("the fixture did not fail the verification: %+v", result.Observation)
	}
	before := len(mock.RequestLog())
	if err := run.reclaim(t.Context(), s, item, result); err != nil {
		t.Fatalf("reclamation returned an error rather than declining: %v", err)
	}
	if keeper.asked != 0 || len(keeper.outcomes) != 0 {
		t.Fatalf("an unverified replacement consulted the reclamation policy: %+v", keeper)
	}
	if got := liveNames(t, s, "media_segments", "parts/"); !slices.Equal(got, []string{"parts/0001"}) {
		t.Fatalf("an unverified replacement removed a fragment: %v", got)
	}
	if log := mock.RequestLog()[before:]; bulkDeletes(log) != 0 || singleDeletes(log) != 0 {
		t.Fatalf("an unverified replacement sent a deletion: %v", log)
	}
}

// Rule 5: a route that can reclaim but has nowhere to record what it
// removed refuses BEFORE it deletes anything, so the refusal's own
// sentence is true when it is written.
func TestReclaimingRefusesWithNowhereToRecord(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	asked := 0
	run := &uploadRun{report: &Report{}, recorder: &UploadRecorder{
		Replacing: func(UploadItem, ObservedPredecessor) error { return nil },
		Reclaiming: func(UploadItem) (ReclaimPlan, error) {
			asked++
			return ReclaimPlan{Fragments: []Fragment{
				{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole}}}, nil
		},
	}}
	err := publishReplacement(t, s, run, item, path)
	if err == nil || !strings.Contains(err.Error(), "nowhere to record") {
		t.Fatalf("the route did not refuse: %v", err)
	}
	if !strings.Contains(err.Error(), "nothing was removed") {
		t.Fatalf("the refusal does not say what it did: %v", err)
	}
	// And the sentence is true: the plan was never even asked for, and the
	// fragment is still there.
	if asked != 0 {
		t.Errorf("the policy was consulted although nothing could be recorded")
	}
	if got := liveNames(t, s, "media_segments", "parts/"); !slices.Equal(got, []string{"parts/0001"}) {
		t.Fatalf("the refusal removed something: %v", got)
	}
}

// Rule 4: the store's reply is read per object. A refusal for one name is
// recorded against that name, and the names it removed are not claimed as
// a whole success.
func TestReclaimingReportsWhatTheStoreRefused(t *testing.T) {
	for _, fixture := range []struct {
		name          string
		failAfter     int
		wantRemoved   []string
		wantFailed    []string
		wantRemaining []string
	}{
		{name: "all refused", failAfter: 0,
			wantFailed:    []string{"parts/0001", "parts/0002"},
			wantRemaining: []string{"parts/0001", "parts/0002"}},
		{name: "one of two", failAfter: 1,
			wantRemoved:   []string{"parts/0001"},
			wantFailed:    []string{"parts/0002"},
			wantRemaining: []string{"parts/0002"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			s, mock := session(t)
			mock.AddContainer("media_segments", "standard")
			seed(mock, "media_segments", "parts/0001", "01234567")
			seed(mock, "media_segments", "parts/0002", "89abcdef")
			mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})
			if fixture.failAfter == 0 {
				mock.FailDeletes = true
			} else {
				mock.FailDeletesAfter = fixture.failAfter
			}

			path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
			item := replacingItem(t, s, path)
			keeper := &reclaimRecorder{plan: ReclaimPlan{Fragments: []Fragment{
				{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole},
				{Container: "media_segments", Name: "parts/0002", Shape: FragmentWhole},
			}}}
			run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
			if err := publishReplacement(t, s, run, item, path); err != nil {
				t.Fatalf("a refused deletion failed the replacement: %v", err)
			}
			if len(keeper.outcomes) != 1 {
				t.Fatalf("the outcome was not recorded once: %+v", keeper.outcomes)
			}
			outcome := keeper.outcomes[0]
			var removed, failed []string
			for _, fragment := range outcome.Removed {
				removed = append(removed, fragment.Name)
			}
			for _, failure := range outcome.Failed {
				failed = append(failed, failure.Fragment.Name)
				if failure.Detail == "" {
					t.Errorf("a failure does not say what the store said: %+v", failure)
				}
				// A validated reply that names this failure IS knowledge:
				// the store kept the object and said so.
				if !failure.Resolved {
					t.Errorf("a named refusal was recorded as an unknown effect: %+v", failure)
				}
			}
			if !slices.Equal(removed, fixture.wantRemoved) || !slices.Equal(failed, fixture.wantFailed) {
				t.Fatalf("the outcome misreports the reply: removed %v failed %v", removed, failed)
			}
			if outcome.Untrusted != "" {
				t.Errorf("a validated reply was treated as untrusted: %q", outcome.Untrusted)
			}
			if got := liveNames(t, s, "media_segments", "parts/"); !slices.Equal(got, fixture.wantRemaining) {
				t.Fatalf("the store and the record disagree: %v", got)
			}
		})
	}
}

// Rule 4 again, from the other side: a reply that does not validate says
// nothing dependable about any name in it, so no name is recorded as
// removed even though the store may well have removed it. The record says
// the effect is unknown, which is the only true thing available.
func TestReclaimingDoesNotTrustAnUnvalidatedReply(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})
	// An error row for a path this request never sent: the reply is not
	// wholly about this request.
	mock.BulkDeleteExtraErrorRow = "/media_segments/somebody/elses"

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	keeper := &reclaimRecorder{plan: ReclaimPlan{Fragments: []Fragment{
		{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole}}}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	if err := publishReplacement(t, s, run, item, path); err != nil {
		t.Fatalf("an unvalidated reply failed the replacement: %v", err)
	}
	outcome := keeper.outcomes[0]
	if outcome.Untrusted == "" {
		t.Fatalf("an unvalidated reply was credited: %+v", outcome)
	}
	if len(outcome.Removed) != 0 || len(outcome.Failed) != 1 {
		t.Fatalf("an unvalidated reply was read as a disposition: %+v", outcome)
	}
	if !strings.Contains(outcome.Failed[0].Detail, "unknown") || outcome.Failed[0].Resolved {
		t.Errorf("an unreadable reply was recorded as a disposition for this name: %+v", outcome.Failed[0])
	}
	// The store's spelling of the unmatched path is preserved in the
	// reason, so the evidence of the bad reply survives the boundary.
	if !strings.Contains(outcome.Untrusted, "somebody/elses") {
		t.Errorf("the reason does not carry what was wrong with the reply: %q", outcome.Untrusted)
	}
}

// Fragments in more than one container are one request per container: the
// bulk body is a single container's paths, so "one bulk delete" is one per
// container, which is one request for the ordinary case.
func TestReclaimingSendsOneRequestPerContainer(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	mock.AddContainer("other_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")
	seed(mock, "other_segments", "parts/0002", "89abcdef")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	keeper := &reclaimRecorder{plan: ReclaimPlan{Fragments: []Fragment{
		{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole},
		{Container: "other_segments", Name: "parts/0002", Shape: FragmentWhole},
	}}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	before := len(mock.RequestLog())
	if err := publishReplacement(t, s, run, item, path); err != nil {
		t.Fatalf("the replacement did not complete: %v", err)
	}
	if log := mock.RequestLog()[before:]; bulkDeletes(log) != 2 {
		t.Fatalf("segments in two containers did not take one request each: %v", log)
	}
	if len(keeper.outcomes[0].Removed) != 2 {
		t.Fatalf("both containers' fragments were not reported gone: %+v", keeper.outcomes[0])
	}
	for _, container := range []string{"media_segments", "other_segments"} {
		if got := liveNames(t, s, container, "parts/"); len(got) != 0 {
			t.Errorf("%s kept a fragment: %v", container, got)
		}
	}
}

// A declined plan sends nothing at all, and the reason reaches the record
// rather than being replaced by a count of zero.
func TestReclaimingHonoursADeclinedPlan(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	// A plan that both declines and carries fragments: the decline wins,
	// because a route that removes what a policy declined is the defect.
	keeper := &reclaimRecorder{plan: ReclaimPlan{Declined: "the container keeps versions",
		Fragments: []Fragment{{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole}}}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	before := len(mock.RequestLog())
	if err := publishReplacement(t, s, run, item, path); err != nil {
		t.Fatalf("the replacement did not complete: %v", err)
	}
	if got := keeper.outcomes[0]; got.Declined != "the container keeps versions" || len(got.Removed) != 0 {
		t.Fatalf("the decline did not reach the record: %+v", got)
	}
	if log := mock.RequestLog()[before:]; bulkDeletes(log) != 0 || singleDeletes(log) != 0 {
		t.Fatalf("a declined plan sent a deletion: %v", log)
	}
	if got := liveNames(t, s, "media_segments", "parts/"); !slices.Equal(got, []string{"parts/0001"}) {
		t.Fatalf("a declined plan removed a fragment: %v", got)
	}
}

// An item that replaced nothing has no predecessor to reclaim, so the
// policy is not even consulted for it.
func TestReclaimingSkipsAnItemThatReplacedNothing(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")

	path := writeFile(t, t.TempDir(), "fresh", "brand new bytes")
	preflight, err := PreflightUpload(t.Context(), s, "media", "docs/", []string{path})
	if err != nil || preflight.Rejected() || len(preflight.Items) != 1 || preflight.Items[0].Replaces {
		t.Fatalf("the creating preflight did not admit a fresh name: %+v %v", preflight, err)
	}
	item := preflight.Items[0]
	item.Route = "native"
	keeper := &reclaimRecorder{plan: ReclaimPlan{Fragments: []Fragment{
		{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole}}}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	result := nativeUpload(t.Context(), s, "media", item, file, item.Replaces)
	if result.Result != transfercopy.ResultCopied {
		t.Fatalf("the creation was refused: %+v", result.Observation)
	}
	if err := run.reclaim(t.Context(), s, item, result); err != nil {
		t.Fatalf("a creation's reclamation returned an error: %v", err)
	}
	if keeper.asked != 0 || len(keeper.outcomes) != 0 {
		t.Fatalf("a creation consulted the reclamation policy: %+v", keeper)
	}
	if got := liveNames(t, s, "media_segments", "parts/"); !slices.Equal(got, []string{"parts/0001"}) {
		t.Fatalf("a creation removed something: %v", got)
	}
}

// ADR 0081 decision 5's accepted consequence, as a case rather than a
// sentence: an outside object over the predecessor's own prefix loses its
// backing data when the recorded segments are reclaimed. rclone's
// replacement does the same thing, and the option exists for the operator
// who cannot accept it.
func TestReclaimingTakesTheDocumentedExposure(t *testing.T) {
	for _, reclaiming := range []bool{true, false} {
		name := "reclaiming"
		if !reclaiming {
			name = "retaining"
		}
		t.Run(name, func(t *testing.T) {
			s, mock := session(t)
			mock.AddContainer("media_segments", "standard")
			seed(mock, "media_segments", "subject/run/0001", "01234567")
			seed(mock, "media_segments", "subject/run/0002", "89abcdef")
			mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/subject/run/"})
			// An outside dynamic manifest over exactly the prefix the
			// predecessor published in its own header. Anyone who read the
			// predecessor could have made it.
			mock.PutObject("media", "outside/dlo", swifttest.Object{Manifest: "media_segments/subject/run/"})

			path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
			item := replacingItem(t, s, path)
			keeper := &reclaimRecorder{}
			if reclaiming {
				keeper.plan = ReclaimPlan{Fragments: []Fragment{
					{Container: "media_segments", Name: "subject/run/0001", Shape: FragmentWhole},
					{Container: "media_segments", Name: "subject/run/0002", Shape: FragmentWhole},
				}}
			} else {
				keeper.plan = ReclaimPlan{Declined: "reclaiming replaced fragments is turned off"}
			}
			run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
			if err := publishReplacement(t, s, run, item, path); err != nil {
				t.Fatalf("the replacement did not complete: %v", err)
			}
			left := liveNames(t, s, "media_segments", "subject/run/")
			if reclaiming && len(left) != 0 {
				t.Fatalf("reclamation left the recorded segments: %v", left)
			}
			if !reclaiming && len(left) != 2 {
				t.Fatalf("retention removed a segment: %v", left)
			}
			// The outside manifest itself is untouched either way: nothing
			// here deletes an object nobody recorded.
			if _, err := s.ObjectDetail(t.Context(), "media", "outside/dlo"); err != nil {
				t.Fatalf("the outside manifest itself was removed: %v", err)
			}
		})
	}
}

// The plan's fragments are the only thing reclaimed even when the
// predecessor named more: the route does not re-derive candidates from
// what it observed, because the policy above it is what applies the
// shape and versioning rules.
func TestReclaimingNeverWidensThePlan(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")
	seed(mock, "media_segments", "parts/0002", "89abcdef")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	// The observation will name both; the plan names one.
	keeper := &reclaimRecorder{plan: ReclaimPlan{Fragments: []Fragment{
		{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole}}}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	witness := uploadWitness(t, mock)
	if err := publishReplacement(t, s, run, item, path); err != nil {
		t.Fatalf("the replacement did not complete: %v", err)
	}
	if len(keeper.observed) != 1 || len(keeper.observed[0].Fragments.Names) != 2 {
		t.Fatalf("the predecessor's observation did not name both segments: %+v", keeper.observed)
	}
	if got := liveNames(t, s, "media_segments", "parts/"); !slices.Equal(got, []string{"parts/0002"}) {
		t.Fatalf("the route reclaimed beyond its plan: %v", got)
	}
	// And nothing else in the account moved while it did that.
	after := uploadSnapshot(t, witness)
	if _, ok := after.Containers["media"].Objects["docs/object"]; !ok {
		t.Fatal("the replacement itself is missing")
	}
	if _, ok := after.Containers["media_segments"].Objects["parts/0001"]; ok {
		t.Fatal("the reclaimed fragment is still listed")
	}
}

// One request should stay small enough to fail cheaply, so a container
// with more fragments than MaxReclaimBatch takes more than one request —
// and every one of them is accounted for per name.
func TestReclaimingBoundsOneRequestsSize(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	total := MaxReclaimBatch + 7
	fragments := make([]Fragment, 0, total)
	for i := range total {
		name := fmt.Sprintf("parts/%06d", i)
		mock.PutObject("media_segments", name, swifttest.Object{Bytes: 0})
		fragments = append(fragments, Fragment{Container: "media_segments", Name: name, Shape: FragmentWhole})
	}
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	keeper := &reclaimRecorder{plan: ReclaimPlan{Fragments: fragments}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	before := len(mock.RequestLog())
	if err := publishReplacement(t, s, run, item, path); err != nil {
		t.Fatalf("the replacement did not complete: %v", err)
	}
	if got := bulkDeletes(mock.RequestLog()[before:]); got != 2 {
		t.Fatalf("%d fragments took %d requests, not 2", total, got)
	}
	if got := len(keeper.outcomes[0].Removed); got != total {
		t.Fatalf("the outcome accounts for %d of %d names", got, total)
	}
	if left := liveNames(t, s, "media_segments", "parts/"); len(left) != 0 {
		t.Fatalf("%d fragments were left behind by the batching: %v", len(left), left[:min(len(left), 5)])
	}
}

// A cluster that says it accepts fewer names than the house bound is
// believed, because it is the one that would refuse the request.
func TestReclaimingBelievesASmallerAdvertisedLimit(t *testing.T) {
	s, mock := sessionWithInfo(t, `{"swift":{"max_file_size":5368709120},"bulk_delete":{"max_deletes_per_request":2},"slo":{}}`)
	mock.AddContainer("media_segments", "standard")
	var fragments []Fragment
	for i := range 5 {
		name := fmt.Sprintf("parts/%06d", i)
		mock.PutObject("media_segments", name, swifttest.Object{Bytes: 0})
		fragments = append(fragments, Fragment{Container: "media_segments", Name: name, Shape: FragmentWhole})
	}
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	keeper := &reclaimRecorder{plan: ReclaimPlan{Fragments: fragments}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	before := len(mock.RequestLog())
	if err := publishReplacement(t, s, run, item, path); err != nil {
		t.Fatalf("the replacement did not complete: %v", err)
	}
	// Five names at two per request: three requests, and the //info read
	// that learned the limit happens once for the run.
	if got := bulkDeletes(mock.RequestLog()[before:]); got != 3 {
		t.Fatalf("an advertised limit of 2 produced %d requests for 5 names", got)
	}
	if got := len(keeper.outcomes[0].Removed); got != 5 {
		t.Fatalf("the outcome accounts for %d of 5 names", got)
	}
}

// The plan's own statement of what it could not name reaches the record,
// because a removal that left some of a manifest's parts behind is not a
// complete one.
func TestReclaimingCarriesWhatThePlanLeftBehind(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	keeper := &reclaimRecorder{plan: ReclaimPlan{
		Fragments:  []Fragment{{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole}},
		LeftBehind: "the predecessor named 5000 segments and this build recorded 1 of them",
	}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	if err := publishReplacement(t, s, run, item, path); err != nil {
		t.Fatalf("the replacement did not complete: %v", err)
	}
	if got := keeper.outcomes[0]; got.LeftBehind != keeper.plan.LeftBehind || len(got.Removed) != 1 {
		t.Fatalf("the record does not carry what was left: %+v", got)
	}
}

// A dynamic manifest's members are listed, and a listing can name an object
// that is already gone (DESIGN §15.3). The store answers such a name as not
// found, which is not a failure: either way the space is no longer held by
// it, and recording it as a failure would report a defect where there is
// none.
func TestReclaimingTreatsAnAbsentFragmentAsReclaimed(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	// The plan names one live fragment and one the listing could have named
	// a moment before it went.
	keeper := &reclaimRecorder{plan: ReclaimPlan{Fragments: []Fragment{
		{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole},
		{Container: "media_segments", Name: "parts/gone", Shape: FragmentWhole},
	}}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	if err := publishReplacement(t, s, run, item, path); err != nil {
		t.Fatalf("the replacement did not complete: %v", err)
	}
	outcome := keeper.outcomes[0]
	if len(outcome.Removed) != 2 || len(outcome.Failed) != 0 || outcome.Untrusted != "" {
		t.Fatalf("an absent fragment was not read as space no longer held: %+v", outcome)
	}
	if left := liveNames(t, s, "media_segments", "parts/"); len(left) != 0 {
		t.Fatalf("the live fragment survived: %v", left)
	}
}

// db:756 finding 5: a valid manifest may name the same object in more than
// one position, and the record keeps every position. One identity is still
// one object to delete and one result to report, so the request carries it
// once and the outcome counts it once.
func TestReclaimingCountsOneIdentityOnce(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "parts/0001", "01234567")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/parts/"})

	path := writeFile(t, t.TempDir(), "object", "the replacement bytes")
	item := replacingItem(t, s, path)
	// The same identity in two positions, as a two-entry manifest over one
	// segment would record it.
	repeated := Fragment{Container: "media_segments", Name: "parts/0001", Shape: FragmentWhole}
	keeper := &reclaimRecorder{plan: ReclaimPlan{Fragments: []Fragment{repeated, repeated}}}
	run := &uploadRun{report: &Report{}, recorder: keeper.recorder()}
	before := len(mock.RequestLog())
	if err := publishReplacement(t, s, run, item, path); err != nil {
		t.Fatalf("the replacement did not complete: %v", err)
	}
	outcome := keeper.outcomes[0]
	if len(outcome.Removed) != 1 || len(outcome.Failed) != 0 {
		t.Fatalf("one object was reported as %d: %+v", len(outcome.Removed), outcome)
	}
	if got := bulkDeletes(mock.RequestLog()[before:]); got != 1 {
		t.Fatalf("one identity took %d requests", got)
	}
	if left := liveNames(t, s, "media_segments", "parts/"); len(left) != 0 {
		t.Fatalf("the object was not removed: %v", left)
	}
}
