package local

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// DownloadRecorder is the durable record of one download run: each item
// admitted before any work for it, and what the engine or the store answered
// for it (ADR 0122 decision 7). The original item preserves identity
// without a second selection map.
type DownloadRecorder struct {
	Admit   func(DownloadAdmission) error
	Execute func(DownloadItem, transfercopy.Observation) error
	// Verify records the item's second record, which states the answer that
	// was taken; nothing is read to make it.
	Verify func(DownloadItem, transfercopy.Observation) error
	// Pause is asked at the head of each unit and nowhere else: before an
	// engine batch is handed to a child and before a native-routed item starts
	// (ADR 0104 decision 1). It returns at once unless the job has been asked
	// to pause; then it returns when the job may carry on, or with the
	// context's error when the job was cancelled while it waited, which the
	// route treats as a cancel seen at that boundary. Any other error means
	// the job could not be paused or resumed because its records can no
	// longer be written, and stops the run as a lost record does. A route
	// with no hook never pauses.
	Pause func(ctx context.Context) error
}

// DownloadAdmission is one item admitted to the job before any work for it.
type DownloadAdmission struct {
	Item DownloadItem
}

type downloadIssue struct {
	label string
	err   error
}

type downloadResult struct {
	transfercopy.Observation
	verification *transfercopy.Observation
	label        string
	failure      error
	issues       []downloadIssue
	// created names the directories the ancestor walk made for this
	// item, relative to the held root, observed rather than inferred
	// (ADR 0043). It is reported whatever the item's own outcome is,
	// because the directories exist either way.
	created []string
}

func downloadFailed(effect transfercopy.Effect, stage transfercopy.Stage, code transfercopy.Code, label string, err error) downloadResult {
	return downloadResult{Observation: transfercopy.Observation{
		Result: transfercopy.ResultFailed, Effect: effect,
		Diagnostic: transfercopy.ClassifyDiagnostic(stage, code, err)}, label: label, failure: err}
}

func downloadNotAttempted(stage transfercopy.Stage, err error) downloadResult {
	result := downloadFailed(transfercopy.EffectNotAttempted, stage, transfercopy.CodeIncomplete, "not-attempted", err)
	result.Result = transfercopy.ResultNotAttempted
	return result
}

// downloadSame is a file a confirmed download left because the engine found
// it the same as the source (ADR 0122 decision 12): verified, not copied.
func downloadSame() downloadResult {
	return downloadResult{Observation: transfercopy.Observation{Result: transfercopy.ResultSame, Effect: transfercopy.EffectNotAttempted},
		verification: &transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}}
}

func downloadVerified(bytes int64) downloadResult {
	return downloadResult{Observation: transfercopy.Observation{
		Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: bytes},
		verification: &transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}}
}

func downloadVerificationFailed(stage transfercopy.Stage, code transfercopy.Code, label string, err error) downloadResult {
	result := downloadFailed(transfercopy.EffectUnresolved, stage, code, label, err)
	result.verification = &transfercopy.Observation{Result: transfercopy.ResultFailed,
		Effect: transfercopy.EffectNotAttempted, Diagnostic: result.Diagnostic}
	return result
}

func (r *downloadResult) addIssue(stage transfercopy.Stage, label string, err error) {
	if err == nil {
		return
	}
	if r.Diagnostic == (transfercopy.Diagnostic{}) {
		r.Diagnostic = transfercopy.ClassifyDiagnostic(stage, transfercopy.CodeIO, err)
	}
	if r.Effect != transfercopy.EffectNotAttempted {
		r.Effect = transfercopy.EffectUnresolved
	}
	r.issues = append(r.issues, downloadIssue{label: label, err: err})
}

func (r downloadResult) err() error {
	err := r.failure
	for _, issue := range r.issues {
		err = errors.Join(err, issue.err)
	}
	return err
}

func downloadRecorder(recorders []*DownloadRecorder) (*DownloadRecorder, error) {
	if len(recorders) == 0 {
		return nil, nil
	}
	if len(recorders) != 1 || recorders[0] == nil || recorders[0].Admit == nil || recorders[0].Execute == nil || recorders[0].Verify == nil {
		return nil, errors.New("recorded download requires admission, execution and verification callbacks")
	}
	return recorders[0], nil
}

func validDownloadItem(prefix string, item DownloadItem) bool {
	if item.Name == "" || !utf8.ValidString(item.Name) || !utf8.ValidString(item.RelPath) || item.Size < 0 || !strings.HasPrefix(item.Name, prefix) {
		return false
	}
	rel := strings.TrimPrefix(item.Name, prefix)
	trimmed := strings.TrimSuffix(rel, "/")
	if strings.HasPrefix(rel, "/") || hasDotSegment(trimmed) || strings.ContainsRune(rel, '\\') || strings.Contains(rel, "//") ||
		strings.ContainsFunc(rel, func(c rune) bool { return c < 0x20 || c == 0x7f }) || portableNameRefusal(trimmed) != "" || componentLengthRefusal(trimmed) != "" {
		return false
	}
	if item.Dir {
		if item.Size != 0 || rel != "" && !strings.HasSuffix(rel, "/") {
			return false
		}
	} else if rel == "" || strings.HasSuffix(rel, "/") {
		return false
	}
	// The child selects by Name while placement selects by RelPath.
	// Individually valid fields must not address different files.
	return item.RelPath == filepath.FromSlash(trimmed) &&
		!filepath.IsAbs(item.RelPath) && filepath.VolumeName(item.RelPath) == ""
}

func (item DownloadItem) LocalPath(root, prefix string) (string, error) {
	if !validDownloadRoot(root) || !utf8.ValidString(prefix) || prefix != "" && !strings.HasSuffix(prefix, "/") || !validDownloadItem(prefix, item) {
		return "", errors.New("download item does not preserve its selected name and local path")
	}
	return filepath.Join(root, item.RelPath), nil
}

// pause parks the run at a unit head when the job has been asked to pause
// (ADR 0104); a run with no hook never pauses.
func (d *downloadRun) pause(ctx context.Context) error {
	if d.recorder == nil || d.recorder.Pause == nil {
		return nil
	}
	err := d.recorder.Pause(ctx)
	if cancelled := ctx.Err(); err == nil || (cancelled != nil && errors.Is(err, cancelled)) {
		return err
	}
	// Not a cancel: the records can no longer be written, so the run stops as
	// a lost record stops it.
	return d.stop(err)
}

type downloadRun struct {
	recorder *DownloadRecorder
	report   *Report
	stopped  error
}

func (d *downloadRun) stop(err error) error {
	if err != nil {
		d.stopped = errors.Join(d.stopped, err)
	}
	return d.stopped
}

func (d *downloadRun) admit(item DownloadItem) error {
	if d.recorder == nil {
		return nil
	}
	return d.stop(d.recorder.Admit(DownloadAdmission{Item: item}))
}

func (d *downloadRun) observe(item DownloadItem, result downloadResult) error {
	// Only residue is reported. An item that copied got the directories
	// it needed and its own record already shows the placed file; an
	// item that failed or was never attempted leaves ancestors nobody
	// asked for, and that is what the operator has to be told (ADR
	// 0043).
	if result.Result != transfercopy.ResultCopied {
		d.report.createdDirectories(result.created)
	}
	if result.Result == transfercopy.ResultSame {
		d.report.Same++
		d.report.Verified++
	}
	if result.Result == transfercopy.ResultCopied {
		d.report.Done++
		if item.Replaces {
			// As for an upload: a name the preview found holding a file,
			// which the operator agreed may be replaced, and whose write
			// was answered.
			d.report.Replaced++
		}
		if result.verification != nil && result.verification.Result == transfercopy.ResultVerified {
			d.report.Verified++
		}
		d.report.Bytes += result.CopiedBytes
		for _, issue := range result.issues {
			d.report.postCommitIssue(item.Name, issue.label, issue.err)
		}
	}
	if result.Result == transfercopy.ResultFailed || result.Result == transfercopy.ResultConflict {
		if len(d.report.Failures) < maxFailures {
			d.report.Failures = append(d.report.Failures, Failure{Name: item.Name, Stage: result.label, Err: result.err().Error(),
				Cancelled: result.Diagnostic.Code == transfercopy.CodeCancelled})
		}
	}
	if d.recorder != nil {
		if err := d.recorder.Execute(item, result.Observation); err != nil {
			return d.stop(err)
		}
	}
	if d.recorder != nil && result.verification != nil {
		if err := d.recorder.Verify(item, *result.verification); err != nil {
			return d.stop(err)
		}
	}
	return nil
}
