package local

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// UploadRecorder acknowledges intent and facts before dependent upload work.
// An observation carries the original item so its path need not be reconstructed.
type UploadRecorder struct {
	Admit   func(UploadAdmission) error
	Execute func(UploadItem, transfercopy.Observation) error
	Verify  func(UploadItem, transfercopy.Observation) error
	// Reclaiming asks what may be reclaimed for an item that has just been
	// published AND verified over a live name, and why nothing may be
	// (ADR 0081 decision 5). It is asked rather than decided here because
	// the answer depends on the operator's setting, on what the container
	// says about versions at that moment, and on the record taken before
	// the publication — none of which this package owns. A route with no
	// hook reclaims nothing, which is the safe direction.
	Reclaiming func(UploadItem) (ReclaimPlan, error)
	// Reclaimed records what the store said about each name, including a
	// plan that declined and a reply that could not be trusted.
	Reclaimed func(UploadItem, ReclaimOutcome) error
	// Replacing reports what the commit saw at a name immediately before
	// publishing over it (ADR 0081 decision 7). It is optional because
	// only an acknowledged overwrite reaches it, and every caller that
	// never replaces would otherwise have to supply a hook it cannot
	// reach. A replacing item with no hook is refused rather than
	// published unrecorded, which uploadRun.replacing enforces.
	Replacing func(UploadItem, ObservedPredecessor) error
}

// ReclaimPlan is what may be reclaimed for one replaced item, answered by
// whoever holds the operator's setting and the record.
//
// Fragments are the identities recorded BEFORE the publication, and only the
// shapes that name a private whole object: a ranged entry references a
// containing object and a nested entry names another manifest, so neither is
// this operation's to remove.
type ReclaimPlan struct {
	Fragments []Fragment
	// Declined is why nothing will be reclaimed, empty when something will.
	Declined string
	// LeftBehind is what this plan could not name and therefore leaves,
	// empty when it names everything. A bounded inventory holds a prefix of
	// what a dynamic manifest listed, so reclaiming what it holds is a
	// partial removal and has to be recorded as one.
	LeftBehind string
}

// ReclaimOutcome is what happened, per name, so a partial success is
// reported as exactly that (ADR 0081 decision 5).
type ReclaimOutcome struct {
	// Declined is why nothing was attempted, empty when something was.
	Declined string
	// Removed are the fragments the store reported gone, including the ones
	// it reported already absent: either way the space is not held by them.
	Removed []Fragment
	// Failed are the fragments the store reported a failure for, with what
	// it said, and the fragments a reply said nothing dependable about.
	Failed []ReclaimFailure
	// Untrusted is why a reply was not believed at all, empty when it was.
	// A reply with an anomaly says nothing about any name in it, so every
	// name in that request is reported as not reclaimed rather than
	// silently counted.
	Untrusted string
	// LeftBehind is the plan's own statement of what it could not name,
	// carried through so the record of a partial removal says it was one.
	LeftBehind string
}

// ReclaimFailure is one fragment the store did not remove, and its reason.
type ReclaimFailure struct {
	Fragment Fragment
	Detail   string
}

// ObservedPredecessor is what was at a name just before a replacement was
// published over it, as the commit read it. In the race this contract
// admits, the object actually replaced may not be this one, so it is named
// as what was seen and never as what was replaced (ADR 0081 decision 7).
type ObservedPredecessor struct {
	// Present says something was read at the name. It is false when the
	// name held nothing at that moment although the preflight found it
	// live, which is what the race this contract admits looks like from
	// here, and it is stated rather than inferred from empty facts.
	Present      bool
	ETag         string
	Length       int64
	LastModified time.Time
	// Class is the live object's shape, so a replaced manifest is
	// distinguishable in the ledger from a replaced plain object.
	Class string
	// Fragments is the inventory of what the predecessor's manifest named,
	// read before the replacement because afterwards it cannot be
	// recovered (ADR 0081 decision 5). Reading it deletes nothing: what a
	// verified replacement may then remove is decided by the policy above
	// this route, which answers the Reclaiming hook with the names it
	// authorises and nothing wider.
	Fragments FragmentInventory
}

// MaxManifestEntries and MaxManifestBytes bound the static manifest this
// route will read for a record. They are this build's own limits, not a
// cluster's: the store's per-manifest limit is a deployment setting and a
// record must not depend on one. A manifest past either bound is reported
// as one that could not be inventoried, because the alternative is
// crediting part of a list as the whole of it (ADR 0081 decision 5).
const (
	MaxManifestEntries = 10000
	MaxManifestBytes   = 8 << 20
)

// MaxListedFragments is how many members of a LISTED prefix one inventory
// holds.
//
// A dynamic manifest names only a prefix, which can hold an arbitrary
// number of objects, and recording one replacement must not size its memory
// to a remote prefix (K-PERF-001), so members past the cap are counted and
// not named. That is sound only because a prefix stays listable after the
// replacement: the record keeps it, so what the cap left out can be found
// again.
//
// A static manifest is not capped here at all. Its list cannot be read
// again once the manifest is gone, so it is recorded whole or the inventory
// reports itself unread; MaxManifestEntries is where that bound lives.
const MaxListedFragments = 2048

// FragmentInventory is what a replaced manifest named, with the reasons it
// is not a complete account of what backs the object. A top-level list is
// not a backing-object graph: an inline entry has no remote part at all, a
// ranged entry references a containing object rather than a private one,
// and a nested manifest names a manifest rather than its descendants. A
// dynamic manifest names only a prefix, so its members have to be listed
// and a listing on an eventually consistent store may be short.
type FragmentInventory struct {
	// Prefix is the dynamic manifest's container and prefix, empty for a
	// static manifest or a plain object.
	Prefix string
	// Names are the entries the inventory holds, in the order they were
	// read. A static manifest's list is whole or the inventory is unread; a
	// listed prefix holds at most MaxListedFragments of its members.
	Names []Fragment
	// Counted is how many segment entries were read, which exceeds
	// len(Names) when the cap stopped naming them.
	Counted int
	// Inline, Ranged and Nested count the static entries that are not
	// simple whole-object remote parts.
	Inline, Ranged, Nested int
	// Complete says the inventory is the exact set the manifest named. A
	// static manifest's list is exact; a listed prefix is a lower bound;
	// and a cap that truncated the names does not make the count wrong,
	// which is why the two are separate.
	Complete bool
	// Unread is why the inventory could not be taken, empty when it was.
	Unread string
}

// Fragment is one entry a replaced manifest named, in its own position.
//
// The container and object stay apart because concatenated they cannot be
// split back when the object name contains a slash. The shape stays with
// the entry rather than only in a total, because after the replacement the
// record is the only thing that can say which name was a containing object
// and which was a manifest of its own (ADR 0081 decision 5).
type Fragment struct {
	Container string
	Name      string
	Shape     FragmentShape
}

// FragmentShape is what a manifest entry referenced.
type FragmentShape string

const (
	// FragmentWhole is a whole remote object, the only shape whose bytes
	// this entry alone accounts for.
	FragmentWhole FragmentShape = "whole"
	// FragmentRanged references part of a containing object, so the object
	// named holds more than this entry used.
	FragmentRanged FragmentShape = "ranged"
	// FragmentNested names another manifest rather than its descendants.
	FragmentNested FragmentShape = "nested"
	// FragmentRangedNested is both, which a manifest may state.
	FragmentRangedNested FragmentShape = "ranged_nested"
	// FragmentInline is data carried inside the manifest itself, with no
	// remote object at all, kept in place so the position is recorded.
	FragmentInline FragmentShape = "inline"
	// FragmentUnknown is an entry shape this build does not recognise,
	// which cannot be counted as a part it might not be.
	FragmentUnknown FragmentShape = "unknown"
)

// record keeps one entry of a manifest's own list, which is never
// truncated: it cannot be read again once the manifest is replaced.
func (i *FragmentInventory) record(fragment Fragment) {
	i.count(fragment)
	i.Names = append(i.Names, fragment)
}

// recordListed keeps one member of a listed prefix while the cap allows it,
// and counts it either way. The prefix is in the record, so a member the
// cap left out can still be found.
func (i *FragmentInventory) recordListed(fragment Fragment) {
	i.count(fragment)
	if len(i.Names) < MaxListedFragments {
		i.Names = append(i.Names, fragment)
	}
}

func (i *FragmentInventory) count(fragment Fragment) {
	i.Counted++
	switch fragment.Shape {
	case FragmentRanged:
		i.Ranged++
	case FragmentNested:
		i.Nested++
	case FragmentRangedNested:
		i.Ranged++
		i.Nested++
	case FragmentInline:
		i.Inline++
	}
}

// Truncated says entries were read that the inventory does not name.
func (i FragmentInventory) Truncated() bool { return i.Counted > len(i.Names) }

type UploadAdmission struct {
	Item          UploadItem
	StagingObject string
}

type uploadIssue struct {
	label string
	err   error
}

type uploadResult struct {
	transfercopy.Observation
	verification *transfercopy.Observation
	label        string
	failure      error
	issues       []uploadIssue
}

func uploadFailed(effect transfercopy.Effect, stage transfercopy.Stage, code transfercopy.Code, label string, err error) uploadResult {
	return uploadResult{Observation: transfercopy.Observation{
		Result: transfercopy.ResultFailed, Effect: effect,
		Diagnostic: transfercopy.ClassifyDiagnostic(stage, code, err)}, label: label, failure: err}
}

func uploadConflict(label string, err error) uploadResult {
	result := uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeConflict, label, err)
	result.Result = transfercopy.ResultConflict
	return result
}

func (r *uploadResult) addIssue(stage transfercopy.Stage, label string, err error) {
	if err == nil {
		return
	}
	if r.Diagnostic == (transfercopy.Diagnostic{}) {
		r.Diagnostic = transfercopy.ClassifyDiagnostic(stage, transfercopy.CodeIO, err)
	}
	if r.Effect != transfercopy.EffectNotAttempted {
		r.Effect = transfercopy.EffectUnresolved
	}
	r.issues = append(r.issues, uploadIssue{label: label, err: err})
}

func uploadRecorder(recorders []*UploadRecorder) (*UploadRecorder, error) {
	if len(recorders) == 0 {
		return nil, nil
	}
	if len(recorders) != 1 || recorders[0] == nil || recorders[0].Admit == nil || recorders[0].Execute == nil || recorders[0].Verify == nil {
		return nil, errors.New("recorded upload requires admission, execution and verification callbacks")
	}
	return recorders[0], nil
}

type uploadRun struct {
	recorder *UploadRecorder
	report   *Report
	failed   int64
	stopped  error
	// bulkLimit is how many names one reclamation request carries,
	// resolved once per run, 0 until then.
	bulkLimit int
}

// MaxReclaimBatch is the largest number of names one reclamation request
// carries. It matches the bound internal/deletion sets for the same verb:
// one request should stay small enough to fail cheaply, whatever a cluster
// says it would accept.
const MaxReclaimBatch = 1000

// reclaimBatch is that bound reduced to what this cluster advertises when
// it advertises less, read once per run because /info is a request too. An
// unreadable /info leaves the house bound in place: it is the smaller
// claim, and a cluster that refuses a batch this size says so per request
// rather than silently.
func (u *uploadRun) reclaimBatch(ctx context.Context, dst *swiftcontrol.Session) int {
	if u.bulkLimit != 0 {
		return u.bulkLimit
	}
	u.bulkLimit = MaxReclaimBatch
	if capabilities, err := dst.FetchCapabilities(ctx); err == nil && capabilities != nil {
		if advertised := capabilities.BulkDeleteMaxDeletes(); advertised > 0 && advertised < u.bulkLimit {
			u.bulkLimit = advertised
		}
	}
	return u.bulkLimit
}

func (u *uploadRun) stop(err error) error {
	if err != nil {
		u.stopped = errors.Join(u.stopped, err)
	}
	return u.stopped
}

func (u *uploadRun) admit(item UploadItem, staging string) error {
	if u.recorder == nil {
		return nil
	}
	return u.stop(u.recorder.Admit(UploadAdmission{Item: item, StagingObject: staging}))
}

// replacing records the predecessor before its name is published over, and
// refuses when a replacing item has nowhere to record it: publishing over
// an object without saying what was there is the one outcome this decision
// exists to prevent.
func (u *uploadRun) replacing(item UploadItem, observed ObservedPredecessor) error {
	if !item.Replaces {
		return nil
	}
	if u.recorder == nil || u.recorder.Replacing == nil {
		return u.stop(errors.New("replacing " + item.Name + " has nowhere to record what was there; nothing was published"))
	}
	return u.stop(u.recorder.Replacing(item, observed))
}

func (u *uploadRun) observe(item UploadItem, result uploadResult) error {
	if result.Result == transfercopy.ResultCopied {
		u.report.Done++
		if result.verification != nil && result.verification.Result == transfercopy.ResultVerified {
			u.report.Verified++
		}
		u.report.Bytes += result.CopiedBytes
		if item.Replaces {
			// Counted where the publication is witnessed rather than where
			// it was planned, so a replacement that never landed is not
			// reported as one. This is inside the copied branch on purpose:
			// a publication whose answer was lost, or one that landed and
			// could not then be verified, is not counted, because what it
			// displaced is not known.
			u.report.Replaced++
		}
	}
	if result.failure != nil {
		u.failed++
		if len(u.report.Failures) < maxFailures {
			u.report.Failures = append(u.report.Failures, Failure{Name: item.Name, Stage: result.label, Err: result.failure.Error(),
				Cancelled: result.Diagnostic.Code == transfercopy.CodeCancelled})
		}
	}
	for _, issue := range result.issues {
		u.report.postCommitIssue(item.Name, issue.label, issue.err)
	}
	if u.recorder == nil {
		return nil
	}
	if err := u.recorder.Execute(item, result.Observation); err != nil {
		return u.stop(err)
	}
	if result.verification != nil {
		if err := u.recorder.Verify(item, *result.verification); err != nil {
			return u.stop(err)
		}
	}
	return nil
}

// FragmentPlan counts what reclaiming these replaced names would remove, so
// the operator's confirmation can state it before anything is written (ADR
// 0081 decision 5).
//
// It is a count of WHOLE remote segments, because those are the only shapes
// reclamation touches, and a separate count of the entries it will never
// touch: ranged, nested, inline and unrecognised. A name that is not a large
// object contributes nothing.
//
// This reading is not the one reclamation acts on. The record it acts on is
// taken immediately before each publication, because that is the only moment
// a predecessor can still be read (decision 7), so the two can differ if the
// object changes in between. The question therefore states what was there
// when the operator was asked, and the record states what was removed. Saying
// a number at all is the point: a question that names only the object being
// replaced does not authorise deleting objects nobody was shown.
func FragmentPlan(ctx context.Context, dst *swiftcontrol.Session, container string, names []string) (whole, kept int) {
	for _, name := range names {
		detail, err := dst.ObjectDetail(ctx, container, name)
		if err != nil || detail == nil {
			// Unreadable here says nothing about what the commit will find,
			// and a question cannot count what it could not read.
			continue
		}
		if detail.Class == swiftcontrol.ClassPlain {
			continue
		}
		inventory := fragmentInventory(ctx, dst, container, name, detail)
		for _, fragment := range inventory.Names {
			if fragment.Shape == FragmentWhole {
				whole++
				continue
			}
			kept++
		}
		// Entries the cap counted but did not name are whole parts as far as
		// this count can say, and a listed prefix may hold more than it
		// showed; both are why the sentence says what was there rather than
		// promising an exact total.
		if extra := inventory.Counted - len(inventory.Names); extra > 0 {
			whole += extra
		}
	}
	return whole, kept
}

// reclaim removes what a replaced predecessor's record named, after the
// replacement has been published AND verified (ADR 0081 decision 5).
//
// The order is this contract's own and is a deliberate deviation from the
// engine, whose trace deletes before its final read: the old bytes are not
// discarded until the new object is confirmed to be there. So an item that
// was not copied, or was copied and not verified, reclaims nothing at all,
// and there is no case where a publication whose outcome is unknown takes
// the predecessor's segments with it.
//
// One bulk delete per container, because a manifest may name segments in
// more than one and the client's bulk delete addresses one container per
// request; for the ordinary case, where every segment lives in the same
// segments container, that is exactly one request. A reply the client could
// not validate is not believed for any name in it.
func (u *uploadRun) reclaim(ctx context.Context, dst *swiftcontrol.Session, item UploadItem, result uploadResult) error {
	if !item.Replaces || u.recorder == nil || u.recorder.Reclaiming == nil {
		return nil
	}
	verified := result.Result == transfercopy.ResultCopied &&
		result.verification != nil && result.verification.Result == transfercopy.ResultVerified
	if !verified {
		return nil
	}
	if u.recorder.Reclaimed == nil {
		// Removing objects without recording what happened to them is the
		// one outcome this decision exists to prevent, so a route that can
		// reclaim but cannot record refuses BEFORE it deletes anything.
		// Asked after the deletion, this refusal would be a false statement
		// about objects that were already gone.
		return u.stop(errors.New("reclaiming " + item.Name + " has nowhere to record what was removed; nothing was removed"))
	}
	plan, err := u.recorder.Reclaiming(item)
	if err != nil {
		return u.stop(err)
	}
	outcome := ReclaimOutcome{Declined: plan.Declined}
	if plan.Declined == "" && len(plan.Fragments) > 0 {
		outcome = reclaimFragments(ctx, dst, plan.Fragments, u.reclaimBatch(ctx, dst))
	}
	outcome.LeftBehind = plan.LeftBehind
	return u.stop(u.recorder.Reclaimed(item, outcome))
}

// reclaimFragments issues the deletes and reads the replies strictly.
// joinReason keeps every reason a reclamation could not be read, because
// one request per container means there can be more than one.
func joinReason(existing, reason string) string {
	if existing == "" {
		return reason
	}
	return existing + "; " + reason
}

func reclaimFragments(ctx context.Context, dst *swiftcontrol.Session, fragments []Fragment, batch int) ReclaimOutcome {
	if batch <= 0 {
		batch = MaxReclaimBatch
	}
	var outcome ReclaimOutcome
	byContainer := map[string][]Fragment{}
	var order []string
	for _, fragment := range fragments {
		if _, seen := byContainer[fragment.Container]; !seen {
			order = append(order, fragment.Container)
		}
		byContainer[fragment.Container] = append(byContainer[fragment.Container], fragment)
	}
	for _, container := range order {
		for _, group := range chunkFragments(byContainer[container], batch) {
			outcome.reclaimGroup(ctx, dst, container, group)
		}
	}
	return outcome
}

// chunkFragments splits one container's fragments into requests of at most
// batch names, keeping their recorded order.
func chunkFragments(group []Fragment, batch int) [][]Fragment {
	var chunks [][]Fragment
	for start := 0; start < len(group); start += batch {
		chunks = append(chunks, group[start:min(start+batch, len(group))])
	}
	return chunks
}

// reclaimGroup sends one request and reads its reply per name.
func (outcome *ReclaimOutcome) reclaimGroup(ctx context.Context, dst *swiftcontrol.Session, container string, group []Fragment) {
	names := make([]string, 0, len(group))
	for _, fragment := range group {
		names = append(names, fragment.Name)
	}
	result, err := dst.BulkDelete(ctx, container, names)
	switch {
	case result != nil && !result.Validated:
		// A reply came back and does not validate. The client says only a
		// validated reply's counts may be credited, so this one is not read
		// as a disposition for any name it mentions — including the names it
		// did not mention, which it may still have removed.
		reason := strings.Join(result.Anomalies, "; ")
		if reason == "" {
			reason = "the reply did not validate"
		}
		outcome.Untrusted = joinReason(outcome.Untrusted, reason)
		for _, fragment := range group {
			outcome.Failed = append(outcome.Failed, ReclaimFailure{Fragment: fragment,
				Detail: "the reply could not be trusted, so this name's effect is unknown"})
		}
		return
	case err != nil:
		// The request left without a usable answer, so what the store did
		// with these names is unknown rather than known to have failed
		// (ADR 0058's distinction).
		outcome.Untrusted = joinReason(outcome.Untrusted, "the store's answer could not be read: "+err.Error())
		for _, fragment := range group {
			outcome.Failed = append(outcome.Failed, ReclaimFailure{Fragment: fragment, Detail: err.Error()})
		}
		return
	}
	failed := map[string]string{}
	for _, failure := range result.Failures {
		failed[failure.Name] = failure.Status
	}
	for _, fragment := range group {
		if detail, bad := failed[fragment.Name]; bad {
			outcome.Failed = append(outcome.Failed, ReclaimFailure{Fragment: fragment, Detail: detail})
			continue
		}
		// Reported gone, or reported already absent: either way the space is
		// no longer held by this name.
		outcome.Removed = append(outcome.Removed, fragment)
	}
}
