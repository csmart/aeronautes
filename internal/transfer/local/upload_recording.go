package local

import (
	"context"
	"errors"
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
	// Replacing reports what the commit saw at a name immediately before
	// publishing over it (ADR 0081 decision 7). It is optional because
	// only an acknowledged overwrite reaches it, and every caller that
	// never replaces would otherwise have to supply a hook it cannot
	// reach. A replacing item with no hook is refused rather than
	// published unrecorded, which uploadRun.replacing enforces.
	Replacing func(UploadItem, ObservedPredecessor) error
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
	// recovered (ADR 0081 decision 5). Nothing deletes any of it: the
	// inventory exists so the space is visible to the operator, and
	// clearing it stays the explicit, force-gated cleanup's work.
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
