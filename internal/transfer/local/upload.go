package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// Upload keeps the engine on staging names and commits live names conditionally.
// An omitted recorder preserves direct callers' unrecorded execution mode.
func Upload(ctx context.Context, dst *swiftcontrol.Session, engine Engine, dstSpec, container, prefix, stageID string,
	preflight *UploadPreflight, onProgress func(done, total, bytes int64),
	onRecovery func(kind, container, prefix, object string) error, recorders ...*UploadRecorder) (report *Report, runErr error) {
	recorder, err := uploadRecorder(recorders)
	if err != nil {
		return nil, err
	}
	if preflight == nil || preflight.Rejected() {
		return nil, errors.New("preflight rejected the upload; nothing was written")
	}
	if stageID == "" {
		return nil, errors.New("upload needs a stage id (the job id)")
	}
	for _, item := range preflight.Items {
		if !validUploadPath(item.Path) {
			return nil, errors.New(uploadPathRefusal)
		}
	}
	u := &uploadRun{recorder: recorder, report: &Report{}}
	report = u.report
	total := int64(len(preflight.Items))
	progress := func() {
		if onProgress != nil {
			onProgress(report.Done+u.failed, total, report.Bytes)
		}
	}
	stagePrefix := ".aeronautes/stage/" + stageID + "/batch/"
	recovery := func(kind, container, prefix, object string) error {
		if u.stopped != nil {
			return u.stopped
		}
		if onRecovery != nil {
			return u.stop(onRecovery(kind, container, prefix, object))
		}
		return nil
	}
	capture := func(event rclone.Event) {
		if p, ok := rclone.PrefixFromEvent(event); ok {
			p = p.Canonical()
			report.CapturedPrefixes = append(report.CapturedPrefixes, p)
			_ = recovery("captured", p.Container, p.Prefix, "")
		}
	}
	byDir := map[string][]UploadItem{}
	var dirs []string
	defer func() {
		if runErr == nil || recorder == nil {
			return
		}
		// Buffered groups never entered an engine; a missing final record
		// must not silently suggest an attempt when storage can retain this fact.
		for _, dir := range dirs {
			for _, item := range byDir[dir] {
				observation := transfercopy.Observation{Result: transfercopy.ResultNotAttempted, Effect: transfercopy.EffectNotAttempted,
					Diagnostic: transfercopy.ClassifyDiagnostic(transfercopy.StageEngine, transfercopy.CodeIncomplete, runErr)}
				if err := recorder.Execute(item, observation); err != nil {
					runErr = errors.Join(runErr, err)
					return
				}
			}
		}
	}()
	for _, item := range preflight.Items {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		staging := ""
		if item.Route != "native" {
			staging = stagePrefix + filepath.Base(item.Path)
		}
		if err := u.admit(item, staging); err != nil {
			return report, err
		}
		if item.Route == "native" {
			if err := ctx.Err(); err != nil {
				result := uploadResult{Observation: transfercopy.Observation{
					Result: transfercopy.ResultNotAttempted, Effect: transfercopy.EffectNotAttempted,
					Diagnostic: transfercopy.ClassifyDiagnostic(transfercopy.StageSourceRead, transfercopy.CodeIncomplete, err)}}
				return report, errors.Join(err, u.observe(item, result))
			}
			report.NativeRouted++
			file, err := os.Open(item.Path)
			var result uploadResult
			if err != nil {
				result = uploadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-open", err)
			} else {
				result = nativeUpload(ctx, dst, container, item, file)
			}
			if err := u.observe(item, result); err != nil {
				return report, err
			}
			progress()
			continue
		}
		dir := filepath.Dir(item.Path)
		if _, exists := byDir[dir]; !exists {
			dirs = append(dirs, dir)
		}
		byDir[dir] = append(byDir[dir], item)
	}
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		items := byDir[dir]
		var names []string
		for _, item := range items {
			names = append(names, filepath.Base(item.Path))
		}
		delete(byDir, dir)
		result, err := u.copy(ctx, dst, engine, dir, dstSpec+"/"+strings.TrimSuffix(stagePrefix, "/"), names, capture)
		if result != nil {
			for _, p := range result.SegmentPrefixes {
				report.CapturedPrefixes = append(report.CapturedPrefixes, p.Canonical())
			}
		}
		if err == nil {
			err = transfercopy.ResultFailure(result)
		}
		if err != nil {
			for _, item := range items {
				failed := uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageEngine, transfercopy.CodeUnknown, "engine", err)
				if observeErr := u.observe(item, failed); observeErr != nil {
					return report, errors.Join(err, observeErr)
				}
			}
			progress()
			if u.stopped != nil {
				return report, u.stopped
			}
			continue
		}
		for _, item := range items {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			outcome := commitStagedUpload(ctx, dst, container, stagePrefix+filepath.Base(item.Path), item, recovery)
			if err := u.observe(item, outcome); err != nil {
				return report, err
			}
			progress()
			if u.stopped != nil {
				return report, u.stopped
			}
		}
	}
	return report, nil
}

func (u *uploadRun) copy(ctx context.Context, session *swiftcontrol.Session, engine Engine,
	src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	for attempt := 0; ; attempt++ {
		result, err := engine.Copy(ctx, src, dst, names, onEvent)
		if u.stopped != nil || err != nil {
			return result, errors.Join(err, u.stopped)
		}
		if transfercopy.ResultFailure(result) != nil && attempt == 0 && transfercopy.IsAuthFailure(result) && ctx.Err() == nil {
			session.ForceReauth()
			continue
		}
		return result, nil
	}
}

func verifyUpload(ctx context.Context, dst *swiftcontrol.Session, container string, item UploadItem) uploadResult {
	live, err := dst.ObjectDetail(ctx, container, item.Name)
	if err == nil && live.ContentLength != item.Size {
		err = fmt.Errorf("size %d != local %d", live.ContentLength, item.Size)
	}
	if err != nil {
		result := uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageVerification, transfercopy.CodeUnverified, "verify", err)
		result.verification = &transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectNotAttempted, Diagnostic: result.Diagnostic}
		return result
	}
	return uploadResult{Observation: transfercopy.Observation{
		Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: live.ContentLength},
		verification: &transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}}
}

func nativeUpload(ctx context.Context, dst *swiftcontrol.Session, container string, item UploadItem, file io.ReadCloser) (result uploadResult) {
	defer func() { result.addIssue(transfercopy.StageSourceRead, "native-close", file.Close()) }()
	if _, err := dst.PutObject(ctx, container, item.Name, file, "application/octet-stream", map[string]string{"If-None-Match": "*"}); err != nil {
		if swiftcontrol.IsPreconditionFailed(err) {
			return uploadConflict("native-put", err)
		}
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "native-put", err)
	}
	return verifyUpload(ctx, dst, container, item)
}

func commitStagedUpload(ctx context.Context, dst *swiftcontrol.Session, container, stagedName string,
	item UploadItem, recovery func(kind, container, prefix, object string) error) uploadResult {
	staged, err := dst.ObjectDetail(ctx, container, stagedName)
	if err != nil {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeMissing, "stage-verify", fmt.Errorf("staged object missing: %w", err))
	}
	if staged.ContentLength != item.Size {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeUnverified, "stage-verify", fmt.Errorf("staged size %d != local %d", staged.ContentLength, item.Size))
	}
	if _, err := dst.ObjectDetail(ctx, container, item.Name); err == nil {
		return uploadConflict("commit-conflict", errors.New("destination appeared since preflight"))
	} else if !swiftcontrol.IsNotFound(err) {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeUnknown, "commit-rehead", err)
	}
	var recordErr, closeErr error
	if staged.Class == swiftcontrol.ClassDLO {
		segmentContainer, segmentPrefix, hasIntent := strings.Cut(staged.Manifest, "/")
		hasIntent = hasIntent && segmentContainer != "" && recovery != nil
		intent := rclone.SegmentPrefix{Container: segmentContainer, Prefix: segmentPrefix}.Canonical()
		if hasIntent {
			if err := recovery("committing", intent.Container, intent.Prefix, container+"/"+item.Name); err != nil {
				return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageRecording, transfercopy.CodeIO, "commit-intent", err)
			}
		}
		headers := map[string]string{"X-Object-Manifest": staged.Manifest, "If-None-Match": "*"}
		if _, err := dst.PutObject(ctx, container, item.Name, strings.NewReader(""), staged.ContentType, headers); err != nil {
			if swiftcontrol.IsPreconditionFailed(err) {
				return uploadConflict("commit-conflict", err)
			}
			return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "commit-manifest", err)
		}
		if hasIntent {
			recordErr = recovery("committed", intent.Container, intent.Prefix, "")
		}
	} else {
		body, _, err := dst.OpenObject(ctx, container, stagedName)
		if err != nil {
			return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeIO, "commit-open-staged", err)
		}
		_, putErr := dst.PutObject(ctx, container, item.Name, body, staged.ContentType, map[string]string{"If-None-Match": "*"})
		closeErr = body.Close()
		if putErr != nil {
			result := uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "commit-copy", putErr)
			if swiftcontrol.IsPreconditionFailed(putErr) {
				result = uploadConflict("commit-conflict", putErr)
			}
			result.addIssue(transfercopy.StageStaging, "staged-close", closeErr)
			return result
		}
	}
	result := verifyUpload(ctx, dst, container, item)
	result.addIssue(transfercopy.StageRecording, "commit-record", recordErr)
	result.addIssue(transfercopy.StageStaging, "staged-close", closeErr)
	if result.Result == transfercopy.ResultCopied && recordErr == nil {
		if err := dst.DeleteObject(ctx, container, stagedName); err != nil && !swiftcontrol.IsNotFound(err) {
			result.addIssue(transfercopy.StageCleanup, "staging-cleanup", fmt.Errorf("delete staging object %q: %w", stagedName, err))
		}
	}
	return result
}
