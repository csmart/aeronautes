package copy

import (
	"context"
	"errors"
	"net"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

// Recorder acknowledgements precede dependent work; OnItem remains advisory.
// All three callbacks are required when a caller opts into recorded execution.
type Recorder struct {
	Admit   func(Admission) error
	Execute func(string, Observation) error
	Verify  func(string, Observation) error
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
	if item.Route != plan.RouteNative && (item.Verdict == plan.VerdictNew || item.Verdict == plan.VerdictUnknown) {
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
