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
// execution.
type Recorder struct {
	Admit   func(Admission) error
	Execute func(string, Observation) error
	// Verify records the item's second record, which states the answer the
	// engine or the store gave for the object, or that the listings showed
	// it already there (ADR 0122 decision 7); nothing is read to make it.
	Verify func(string, Observation) error
	// Pause is asked at the head of each unit and nowhere else (ADR 0104
	// decision 1): before anything is done for an item the plan hands over,
	// and before an engine batch is dispatched, whether the batch filled or
	// the plan ended. Names buffered for the engine are admitted and not yet
	// sent, so nothing is in flight at either place. It returns at once
	// unless the job has been asked to pause; then it returns when the job
	// may carry on, or with the context's error when the job was cancelled
	// while it waited, which the executor treats as a cancel seen at that
	// boundary. Any other error means the job could not be paused or resumed
	// because its records can no longer be written, and stops the run as a
	// lost record does. A run with no hook never pauses.
	Pause func(ctx context.Context) error
}

// Admission is one object admitted to the job before any request for it.
type Admission struct {
	Item plan.Item
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

// copied is an object the engine or the store answered for: it is written
// at its name.
func copied(bytes int64, detail string) executionResult {
	return executionResult{Observation: Observation{Result: ResultCopied, Effect: EffectReportedApplied, CopiedBytes: bytes}, detail: detail}
}

// same is an object the destination already holds, by the two listings or by
// the store's description of both names: nothing was sent for it.
func same() executionResult {
	return executionResult{Observation: Observation{Result: ResultSame, Effect: EffectNotAttempted}}
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

// pause parks the run at a unit head when the job has been asked to pause;
// see Recorder.Pause for what its errors mean.
func (e *Executor) pause(ctx context.Context) error {
	if e.cfg.Recorder == nil || e.cfg.Recorder.Pause == nil {
		return nil
	}
	err := e.cfg.Recorder.Pause(ctx)
	if cancelled := ctx.Err(); err == nil || (cancelled != nil && errors.Is(err, cancelled)) {
		return err
	}
	return e.stop(err)
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
	return e.stop(e.cfg.Recorder.Admit(Admission{Item: item}))
}

// observe settles one object's outcome and says whether the run may go on.
func (e *Executor) observe(name string, result executionResult) error {
	e.settle(name, result)
	return e.stopped
}

// settle counts one object's outcome and records it, and reports whether its
// record could be kept. An object that was written, or that the destination
// already held, gets its second record at once: it states that answer, and is
// not a read of the name (ADR 0122 decision 7).
func (e *Executor) settle(name string, result executionResult) (kept bool) {
	outcome, detail := string(result.Result), result.detail
	settled := false
	switch result.Result {
	case ResultCopied:
		settled = true
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
	case ResultRefused:
		e.report.OverwritesRefused++
		outcome = "overwrite-refused"
	case ResultSame:
		settled = true
		e.report.SkippedEqual++
		outcome = "skipped-equal"
	}
	if settled {
		e.report.Verified++
	}
	kept = true
	if e.cfg.Recorder != nil {
		err := e.cfg.Recorder.Execute(name, result.Observation)
		if err == nil && settled {
			err = e.cfg.Recorder.Verify(name, Observation{Result: ResultVerified, Effect: EffectNotAttempted})
		}
		if err != nil {
			e.stop(err)
			kept = false
		}
	}
	// Post-copy problems cannot vanish behind a successful item callback.
	e.stop(result.problem)
	e.emit(name, outcome, detail)
	return kept
}

func (e *Executor) abandonBatch(batch []queued, cause error) error {
	for _, item := range batch {
		if e.cfg.Recorder == nil {
			break
		}
		observation := Observation{Result: ResultNotAttempted, Effect: EffectNotAttempted,
			Diagnostic: ClassifyDiagnostic(StageEngine, CodeIncomplete, cause)}
		if err := e.cfg.Recorder.Execute(item.name, observation); err != nil {
			e.stop(err)
			break
		}
	}
	return errors.Join(cause, e.stopped)
}
