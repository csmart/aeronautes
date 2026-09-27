package local

import (
	"errors"
	"time"

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
	ETag         string
	Length       int64
	LastModified time.Time
	// Class is the live object's shape, so a replaced manifest is
	// distinguishable in the ledger from a replaced plain object.
	Class string
}

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
			// reported as one.
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
