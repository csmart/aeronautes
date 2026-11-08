package copy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

// ShownObject is what the operator was shown at a destination name when they
// accepted replacing it (ADR 0085). It is HEAD-grade on purpose: Length is the
// logical size a manifest HEAD reports, not its stored bytes, so a DLO whose
// backing segments changed re-reads as a different object even when its listing
// row did not move. The three fields are the whole identity the comparison
// compares, so the type is compared by value.
type ShownObject struct {
	Class  string
	Length int64
	ETag   string
}

// ObservedPredecessor is what the commit saw at a name immediately before
// publishing over it (ADR 0087 decision 6). Present is false when the name held
// nothing at that moment although it was live when the acknowledgement was
// taken, which is the race this contract admits, stated rather than inferred
// from empty facts. Its entity tag, length and last-modified are the observed
// facts ADR 0081 decision 7 keeps with the item. Unlike upload's predecessor it
// carries no fragment inventory: copy retains the replaced object's segments
// rather than reclaiming them (decision 6), because they live on the destination
// account, which the operator was not shown, so nothing here is this operation's
// to remove.
type ObservedPredecessor struct {
	Present      bool
	Class        string
	Length       int64
	ETag         string
	LastModified time.Time
}

// shownFrom reads a HEAD into the identity the fixed-point comparison needs, or
// reports that the object cannot be identified. A HEAD stating no entity tag or
// no length identifies nothing: comparing a zero against a genuine empty
// object's baseline would otherwise pass, and the replacement would proceed
// against an identity nobody established. It mirrors the upload route's reading
// so a copy and an upload describe the same object the same way.
func shownFrom(detail *swiftcontrol.ObjectDetail) (ShownObject, bool) {
	tag := strings.Trim(detail.ETag, `"`)
	if tag == "" {
		return ShownObject{}, false
	}
	if _, stated := detail.RawHeaders["Content-Length"]; !stated {
		return ShownObject{}, false
	}
	return ShownObject{Class: string(detail.Class), Length: detail.ContentLength, ETag: tag}, true
}

// observedFrom records what was live immediately before a publication replaced
// it, for the audit trail decision 6 requires. A HEAD with no usable identity
// is still recorded as present with whatever it stated, because the fact being
// kept is that something was there, not that it could be named.
func observedFrom(detail *swiftcontrol.ObjectDetail) ObservedPredecessor {
	return ObservedPredecessor{Present: true, Class: string(detail.Class),
		Length: detail.ContentLength, ETag: strings.Trim(detail.ETag, `"`),
		LastModified: detail.LastModified}
}

// acknowledged asks what the operator was shown at a name, so a replacement can
// be compared against it at the fixed point. A recorder with no hook reads as
// not acknowledged, which keeps an overwrite refused rather than letting one
// through against nothing — the safe direction, and the one a build with no
// acknowledgement surface wired stays on.
func (e *Executor) acknowledged(name string) (ShownObject, bool, error) {
	if e.cfg.Recorder == nil || e.cfg.Recorder.Acknowledged == nil {
		return ShownObject{}, false, nil
	}
	return e.cfg.Recorder.Acknowledged(name)
}

// replacing records what was live before publishing over it, and refuses when a
// replacement has nowhere to record it: publishing over an object without
// saying what was there is the one outcome decision 6's trail exists to
// prevent. Only an acknowledged overwrite reaches it, so a run that never
// replaces needs no hook.
func (e *Executor) replacing(name string, observed ObservedPredecessor) error {
	if e.cfg.Recorder == nil || e.cfg.Recorder.Replacing == nil {
		return fmt.Errorf("replacing %s has nowhere to record what was there; nothing was published", name)
	}
	return e.cfg.Recorder.Replacing(name, observed)
}

// recordRefused persists both identities for a replacement stopped at the fixed
// point: the one the operator accepted and the one found there. The run's own
// failure list cannot carry them — it is bounded and in memory, and only the
// first failure's message reaches the job record — so a hook that cannot save
// them stops the run rather than losing which name now holds what.
func (e *Executor) recordRefused(name string, acknowledged, found ShownObject) {
	if e.cfg.Recorder == nil || e.cfg.Recorder.Refused == nil {
		e.stop(fmt.Errorf("replacing %s was refused and the refusal has nowhere to be recorded", name))
		return
	}
	if err := e.cfg.Recorder.Refused(name, acknowledged, found); err != nil {
		e.stop(fmt.Errorf("replacing %s was refused and the refusal could not be recorded: %w", name, err))
	}
}

// overwriteRefused builds the result for a replacement stopped at the fixed
// point and appends the private refusal record the report closes over. Unlike
// the planning-time refusal it is a commit-stage conflict: the name changed
// under an acknowledgement that was valid when it was given.
func (e *Executor) overwriteRefused(name string, effect Effect, detail string) executionResult {
	result := executionResult{Observation: Observation{Result: ResultRefused, Effect: effect,
		Diagnostic: ClassifyDiagnostic(StageCommit, CodeConflict, nil)}, detail: detail}
	// A staged refusal is recorded at flush, out of the ascending name order the
	// immediate (native) refusals append in, and keeps the unresolved effect the
	// engine's staging bytes carry (ADR 0030). It goes to a separate ordered spool
	// so the verify pass merges the two name-ordered streams rather than stopping
	// on an out-of-order append, which is ADR 0085's continued execution.
	spool := &e.refusals
	if effect == EffectUnresolved {
		spool = &e.refusalsDeferred
	}
	result.addProblem(StageRecording, spool.append(name))
	return result
}

// replaceAtFixedPoint is the comparison ADR 0085 keeps immediately before a
// publishing request, for a name the operator acknowledged replacing, given
// the fresh reading of its destination. Live and still what was shown: what
// is about to be replaced is recorded first, because the publication
// destroys the ability to read it, and the publication then replaces
// unconditionally, since a create condition would reject the very name the
// operator asked to replace. Live and different, or unidentifiable: refused
// with both identities kept and the live object untouched. Gone: the name is
// free again and publishes create-only, but its observed absence is recorded
// first, as the predecessor this contract's race admits (decision 7). It
// reports whether the publication replaces, or the result that stops it; a
// route adds its own cleanup to that result.
func (e *Executor) replaceAtFixedPoint(name string, shown ShownObject, dst *swiftcontrol.ObjectDetail, live bool, effect Effect, route string) (replaces bool, stop *executionResult) {
	if !live {
		if recErr := e.replacing(name, ObservedPredecessor{Present: false}); recErr != nil {
			e.stop(recErr)
			result := failed(effect, StageRecording, CodeUnknown, route+"-absent-record", recErr)
			return false, &result
		}
		return false, nil
	}
	// The fresh read answers the same completeness rule as the capture: a
	// HEAD stating no entity tag or no length identifies nothing, and a zero
	// compared against a genuine empty object's baseline would otherwise let
	// a replacement proceed against an identity nobody established.
	found, identifiable := shownFrom(dst)
	if !identifiable {
		result := e.overwriteRefused(name, effect, "the destination could not be identified before replacing it: its HEAD stated no entity tag or no length")
		return false, &result
	}
	if found != shown {
		e.recordRefused(name, shown, found)
		result := e.overwriteRefused(name, effect, fmt.Sprintf(
			"the destination no longer holds what was acknowledged: it was a %s of %d bytes with entity tag %s, and it is now a %s of %d bytes with entity tag %s",
			shown.Class, shown.Length, shown.ETag, found.Class, found.Length, found.ETag))
		return false, &result
	}
	if recErr := e.replacing(name, observedFrom(dst)); recErr != nil {
		e.stop(recErr)
		result := failed(effect, StageRecording, CodeUnknown, route+"-replace-record", recErr)
		return false, &result
	}
	return true, nil
}

// routeOverwrite dispatches an item whose live destination the operator
// acknowledged replacing. Both the staged and the native route now carry the
// fixed-point comparison, each immediately before its own publishing request,
// so an acknowledged overwrite routes through runNew exactly as a first copy
// does and the route it lands on compares and replaces. An item that is not
// acknowledged refuses exactly as it did before any acknowledgement existed: a
// build with no acknowledgement surface wired reaches this with ok false for
// every item and replaces nothing.
func (e *Executor) routeOverwrite(ctx context.Context, item plan.Item) error {
	_, ok, err := e.acknowledged(item.Name)
	if err != nil {
		return e.observe(item.Name, failed(EffectNotAttempted, StagePlanning, CodeUnknown, "ack-read", err))
	}
	if !ok {
		return e.refuseOverwrite(item)
	}
	return e.runNew(ctx, item)
}
