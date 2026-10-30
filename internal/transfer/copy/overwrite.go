package copy

import (
	"context"
	"fmt"
	"strings"

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
// from empty facts. Unlike upload's predecessor it carries no fragment
// inventory: copy retains the replaced object's segments rather than reclaiming
// them (decision 6), because they live on the destination account, which the
// operator was not shown, so nothing here is this operation's to remove.
type ObservedPredecessor struct {
	Present bool
	Class   string
	Length  int64
	ETag    string
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
		Length: detail.ContentLength, ETag: strings.Trim(detail.ETag, `"`)}
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
func (e *Executor) overwriteRefused(name, detail string) executionResult {
	result := executionResult{Observation: Observation{Result: ResultRefused, Effect: EffectNotAttempted,
		Diagnostic: ClassifyDiagnostic(StageCommit, CodeConflict, nil)}, detail: detail}
	result.addProblem(StageRecording, e.refusals.append(name))
	return result
}

// routeOverwrite dispatches an item whose live destination the operator
// acknowledged replacing. Only the staged route carries the fixed-point
// comparison so far; an unsafe-named object copies through the native route,
// which still publishes create-only, so an acknowledged overwrite of one keeps
// refusing until that route learns to compare. An item that is not acknowledged
// refuses exactly as it did before any acknowledgement existed.
func (e *Executor) routeOverwrite(ctx context.Context, item plan.Item) error {
	_, ok, err := e.acknowledged(item.Name)
	if err != nil {
		return e.observe(item.Name, failed(EffectNotAttempted, StagePlanning, CodeUnknown, "ack-read", err))
	}
	if !ok || item.Route == plan.RouteNative {
		return e.refuseOverwrite(item)
	}
	return e.runNew(ctx, item)
}
