package copy

import (
	"context"
	"errors"
	"net"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

// Recorder acknowledgements precede dependent work; OnItem remains advisory.
// Admit, Execute and Verify are required when a caller opts into recorded
// execution; the overwrite hooks below are optional, because only an
// acknowledged overwrite reaches them and a run that never replaces would
// otherwise have to supply hooks it cannot use.
type Recorder struct {
	Admit   func(Admission) error
	Execute func(string, Observation) error
	Verify  func(string, Observation) error
	// Acknowledged asks what the operator was shown at a destination name when
	// they accepted replacing it, for the comparison ADR 0085 makes immediately
	// before the publication. The second result is false when the
	// acknowledgement captured no identity, which that decision makes a refusal
	// to overwrite rather than a licence to proceed. A recorder without this hook
	// never lets an overwrite through, which is the state a build with no
	// acknowledgement surface stays in.
	Acknowledged func(string) (ShownObject, bool, error)
	// Replacing records what was live immediately before a publication replaced
	// it (ADR 0087 decision 6). A replacing item with no hook is refused rather
	// than published unrecorded, the same rule the upload route follows.
	Replacing func(string, ObservedPredecessor) error
	// Refused records both identities for a replacement stopped at the fixed
	// point: the one acknowledged and the one found there. The run's own failure
	// list cannot carry them, so a refusal that cannot be recorded stops the run.
	Refused func(name string, acknowledged, found ShownObject) error
	// VersioningUnchanged asks whether the destination container still
	// reports the versioning the question was composed from when the
	// operator authorised replacing (ADR 0102 decision 2). It is asked at
	// the fixed point with the route's live context, so a cancelled job stops
	// the read, and answered from a fresh read of the container. A non-empty
	// refusal is the name's recorded refusal, carrying both observations, and
	// the route publishes nothing for it. An error means the refusal could
	// not be recorded: the name is still refused, and the run stops as it
	// does for any lost record. A replacing name with no hook is refused
	// rather than published under versioning nobody re-read, as the other
	// hooks are.
	VersioningUnchanged func(ctx context.Context, name string) (refusal string, err error)
}

type Admission struct {
	Item          plan.Item
	StagingObject string
}

type Result string
type Effect string
type Stage string
type Code string

const (
	ResultCopied       Result = "copied"
	ResultFailed       Result = "failed"
	ResultConflict     Result = "conflict"
	ResultRefused      Result = "refused"
	ResultSame         Result = "same"
	ResultNotAttempted Result = "not_attempted"
	ResultVerified     Result = "verified"

	EffectNotAttempted    Effect = "not_attempted"
	EffectReportedApplied Effect = "reported_applied"
	EffectUnresolved      Effect = "unresolved"

	StagePlanning     Stage = "planning"
	StageSourceRead   Stage = "source_read"
	StageEngine       Stage = "engine"
	StageStaging      Stage = "staging"
	StageCommit       Stage = "commit"
	StageVerification Stage = "verification"
	StageCleanup      Stage = "cleanup"
	StageRecording    Stage = "recording"

	CodeCancelled      Code = "cancelled"
	CodeTimeout        Code = "timeout"
	CodeAuthentication Code = "authentication"
	CodeForbidden      Code = "forbidden"
	CodeMissing        Code = "missing"
	CodeConflict       Code = "conflict"
	CodeUnsupported    Code = "unsupported"
	CodeIO             Code = "io"
	CodeIncomplete     Code = "incomplete"
	CodeUnverified     Code = "unverified"
	CodeUnknown        Code = "unknown"
)

// Diagnostic has no place for provider text, engine output, URLs or headers.
type Diagnostic struct {
	Stage      Stage
	Code       Code
	HTTPStatus int
}

type Observation struct {
	Result      Result
	Effect      Effect
	Diagnostic  Diagnostic
	CopiedBytes int64
}

func ClassifyDiagnostic(stage Stage, code Code, err error) Diagnostic {
	d := Diagnostic{Stage: stage, Code: code}
	var se *swiftcontrol.SwiftError
	var ne net.Error
	switch {
	case errors.Is(err, context.Canceled):
		d.Code = CodeCancelled
	case errors.Is(err, context.DeadlineExceeded):
		d.Code = CodeTimeout
	case errors.As(err, &se):
		if se.HTTPStatus >= 100 && se.HTTPStatus <= 599 {
			d.HTTPStatus = se.HTTPStatus
		}
		switch se.HTTPStatus {
		case 401:
			d.Code = CodeAuthentication
		case 403:
			d.Code = CodeForbidden
		case 404:
			d.Code = CodeMissing
		case 409, 412:
			d.Code = CodeConflict
		case 408, 504:
			d.Code = CodeTimeout
		default:
			if se.Kind == swiftcontrol.KindTimeout {
				d.Code = CodeTimeout
			} else if se.Kind == swiftcontrol.KindNetwork {
				d.Code = CodeIO
			}
		}
	case errors.As(err, &ne):
		d.Code = CodeIO
		if ne.Timeout() {
			d.Code = CodeTimeout
		}
	}
	return d
}

// Raw errors remain in the existing bounded report, never the checked record.
type executionResult struct {
	Observation
	label   string
	detail  string
	failure error
	problem error
	// overwrote marks a copied result that replaced a live destination, so the
	// report can count replacements apart from first copies.
	overwrote bool
}

func failed(effect Effect, stage Stage, code Code, label string, err error) executionResult {
	return executionResult{
		Observation: Observation{Result: ResultFailed, Effect: effect, Diagnostic: ClassifyDiagnostic(stage, code, err)},
		label:       label, failure: err,
	}
}

func conflict(effect Effect, err error, detail string) executionResult {
	return executionResult{
		Observation: Observation{Result: ResultConflict, Effect: effect, Diagnostic: ClassifyDiagnostic(StageCommit, CodeConflict, err)},
		detail:      detail,
	}
}

func copied(bytes int64, detail string) executionResult {
	return executionResult{Observation: Observation{Result: ResultCopied, Effect: EffectReportedApplied, CopiedBytes: bytes}, detail: detail}
}

func (r *executionResult) addProblem(stage Stage, err error) {
	if err == nil {
		return
	}
	if r.Diagnostic == (Diagnostic{}) {
		r.Diagnostic = ClassifyDiagnostic(stage, CodeUnknown, err)
	}
	if r.Effect != EffectNotAttempted {
		r.Effect = EffectUnresolved
	}
	r.problem = errors.Join(r.problem, err)
}

func (e *Executor) stop(err error) error {
	if err != nil {
		e.stopped = errors.Join(e.stopped, err)
	}
	return e.stopped
}

func (e *Executor) admitItem(item plan.Item) error {
	if e.cfg.Recorder == nil {
		return nil
	}
	admission := Admission{Item: item}
	// A non-native item that is not already live (New), might be (Unknown) or is
	// a proven plain difference (Changed) can stage: New and Unknown always, and
	// Changed once the operator acknowledges replacing it. The staging location
	// is recorded for all three so an interrupted run's residue is tracked even
	// when the acknowledgement that would have staged it is not known yet here;
	// an item that never stages leaves a hint pointing at nothing, which
	// cleanup treats as already gone.
	if item.Route != plan.RouteNative &&
		(item.Verdict == plan.VerdictNew || item.Verdict == plan.VerdictUnknown || item.Verdict == plan.VerdictChanged) {
		admission.StagingObject = e.stage + "batch/" + item.Name
	}
	return e.stop(e.cfg.Recorder.Admit(admission))
}

func (e *Executor) observe(name string, result executionResult) error {
	outcome, detail := string(result.Result), result.detail
	switch result.Result {
	case ResultCopied:
		e.report.Copied++
		e.report.BytesCopied += result.CopiedBytes
		if result.overwrote {
			e.report.Overwrites++
		}
	case ResultFailed:
		if len(e.report.Failures) < maxFailures {
			e.report.Failures = append(e.report.Failures, Failure{Name: name, Stage: result.label, Err: result.failure.Error()})
		}
		detail = result.label + ": " + result.failure.Error()
	case ResultConflict:
		e.report.Conflicts = append(e.report.Conflicts, name)
	case ResultRefused:
		e.report.OverwritesRefused++
		outcome = "overwrite-refused"
	case ResultSame:
		e.report.SkippedEqual++
		outcome = "skipped-equal"
	}
	if e.cfg.Recorder != nil {
		if err := e.cfg.Recorder.Execute(name, result.Observation); err != nil {
			e.stop(err)
		}
	}
	// Post-copy problems cannot vanish behind a successful item callback.
	e.stop(result.problem)
	e.emit(name, outcome, detail)
	return e.stopped
}

func (e *Executor) abandonBatch(names []string, cause error) error {
	for _, name := range names {
		if e.cfg.Recorder == nil {
			break
		}
		observation := Observation{Result: ResultNotAttempted, Effect: EffectNotAttempted,
			Diagnostic: ClassifyDiagnostic(StageEngine, CodeIncomplete, cause)}
		if err := e.cfg.Recorder.Execute(name, observation); err != nil {
			e.stop(err)
			break
		}
	}
	return errors.Join(cause, e.stopped)
}
