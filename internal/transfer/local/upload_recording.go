package local

import (
	"context"
	"errors"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// UploadRecorder acknowledges intent and facts before dependent upload work.
// An observation carries the original item so its path need not be reconstructed.
type UploadRecorder struct {
	Admit   func(UploadAdmission) error
	Execute func(UploadItem, transfercopy.Observation) error
	// Verify records the item's second record, which for an upload states
	// the answer the engine or the store gave for the file (ADR 0122
	// decision 7); nothing is read to make it.
	Verify func(UploadItem, transfercopy.Observation) error
	// StaticManifests is told what the job learned about whether the store
	// returns static manifests (ADR 0111 decision 9), so the connection can
	// remember it and, when they are not returned, the job's record can say
	// the kind is now dynamic from the capability (decision 10). An error is
	// a record that could not be kept, and stops the run. A run with no hook
	// learns for itself alone.
	StaticManifests func(returned bool) error
	// Pause is asked at the head of each unit and nowhere else: before an
	// engine group is handed to a child and before a native-routed item starts
	// (ADR 0104 decision 1). It returns at once unless the job has been asked
	// to pause; then it returns when the job may carry on, or with the
	// context's error when the job was cancelled while it waited, which the
	// route treats as a cancel seen at that boundary. Any other error means
	// the job could not be paused or resumed because its records can no
	// longer be written, and stops the run as a lost record does. A route
	// with no hook never pauses.
	Pause func(ctx context.Context) error
}

// MaxManifestEntries and MaxManifestBytes bound the static manifest this
// route will read back. They are this build's own limits, not a cluster's:
// the store's per-manifest limit is a deployment setting and a reading must
// not depend on one.
const (
	MaxManifestEntries = 10000
	MaxManifestBytes   = 8 << 20
)

// UploadAdmission is one file admitted to the job before any request for it.
type UploadAdmission struct {
	Item UploadItem
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

// pause parks the run at a unit head when the job has been asked to pause
// (ADR 0104); a run with no hook never pauses.
func (u *uploadRun) pause(ctx context.Context) error {
	if u.recorder == nil || u.recorder.Pause == nil {
		return nil
	}
	err := u.recorder.Pause(ctx)
	if cancelled := ctx.Err(); err == nil || (cancelled != nil && errors.Is(err, cancelled)) {
		return err
	}
	return u.stop(err)
}

type uploadRun struct {
	recorder *UploadRecorder
	report   *Report
	failed   int64
	stopped  error
	// static is what this run knows about whether the store returns static
	// manifests: from the connection when the preflight says so, or from
	// reading its own first static manifest back (ADR 0111 decision 9).
	static staticManifests
}

// staticManifests is whether the answer is known and what it is.
type staticManifests struct{ known, returned bool }

func (u *uploadRun) stop(err error) error {
	if err != nil {
		u.stopped = errors.Join(u.stopped, err)
	}
	return u.stopped
}

func (u *uploadRun) admit(item UploadItem) error {
	if u.recorder == nil {
		return nil
	}
	return u.stop(u.recorder.Admit(UploadAdmission{Item: item}))
}

// settle finishes one item the same way on either route: its outcome is
// observed and recorded, progress is reported, and a run the recorder has
// stopped ends here.
func (u *uploadRun) settle(item UploadItem, result uploadResult, progress func()) error {
	if err := u.observe(item, result); err != nil {
		return err
	}
	progress()
	return u.stopped
}

func (u *uploadRun) observe(item UploadItem, result uploadResult) error {
	if result.Result == transfercopy.ResultSame {
		u.report.Same++
		u.report.Verified++
	}
	if result.Result == transfercopy.ResultCopied {
		u.report.Done++
		if result.verification != nil && result.verification.Result == transfercopy.ResultVerified {
			u.report.Verified++
		}
		u.report.Bytes += result.CopiedBytes
		if item.Replaces {
			// Counted where the write is answered rather than where it was
			// planned, so a replacement that was refused, or whose answer
			// was lost, is not reported as one. The name is one the preview
			// found live and the operator agreed to replace.
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
