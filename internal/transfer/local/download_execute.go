package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// Download keeps final placement behind checked identity and observations.
// Omitted recording preserves direct callers' unrecorded mode.
func Download(ctx context.Context, src *swiftcontrol.Session, engine Engine, srcSpec, container, prefix, destDir, stageID string,
	preflight *DownloadPreflight, onProgress func(done, total, bytes int64), recorders ...*DownloadRecorder) (report *Report, runErr error) {
	if !validDownloadRoot(destDir) {
		return nil, errors.New(downloadPathRefusal)
	}
	recorder, err := downloadRecorder(recorders)
	if err != nil {
		return nil, err
	}
	if preflight == nil {
		return nil, errors.New("download needs a preflight plan")
	}
	if preflight.Rejected() {
		return nil, errors.New("preflight rejected the download; nothing was written")
	}
	if stageID == "" || !utf8.ValidString(stageID) || strings.ContainsAny(stageID, "/\\\x00") || stageID == "." || stageID == ".." {
		return nil, errors.New("download needs a single-component stage id (the job id)")
	}
	if !utf8.ValidString(prefix) || prefix != "" && !strings.HasSuffix(prefix, "/") {
		return nil, errors.New("download needs an unchanged UTF-8 prefix ending in '/'")
	}
	seen := make(map[string]bool, len(preflight.Items))
	folded := make(map[string]bool, len(preflight.Items))
	files := make(map[string]bool, len(preflight.Items))
	for _, item := range preflight.Items {
		if !validDownloadItem(prefix, item) || seen[item.Name] {
			return nil, errors.New("download item does not preserve its unique selected name and relative path")
		}
		seen[item.Name] = true
		relative := strings.TrimPrefix(item.Name, prefix)
		fold := strings.ToLower(relative)
		if folded[fold] {
			return nil, errors.New("download selection contains case-fold collisions")
		}
		folded[fold] = true
		if !item.Dir {
			files[fold] = true
		}
	}
	for _, item := range preflight.Items {
		relative := strings.TrimPrefix(item.Name, prefix)
		for separator := strings.LastIndexByte(relative, '/'); separator >= 0; separator = strings.LastIndexByte(relative, '/') {
			relative = relative[:separator]
			if files[strings.ToLower(relative)] {
				return nil, errors.New("download selection needs the same path as a file and a directory")
			}
		}
	}
	if err := checkDownloadDestination(destDir, preflight.Items); err != nil {
		return nil, err
	}
	d := &downloadRun{recorder: recorder, report: &Report{}}
	report = d.report
	total := int64(len(preflight.Items))
	progress := func() {
		if onProgress != nil {
			onProgress(report.Done, total, report.Bytes)
		}
	}
	stageDir := filepath.Join(destDir, ".aeronautes-stage-"+stageID)
	var engineNames []string
	engineItems := map[string]DownloadItem{}
	stageAttempted := false
	defer func() {
		if runErr == nil || recorder == nil || stageAttempted {
			return
		}
		// A buffered batch has no staged effects yet. After a stage call
		// begins, absent observations must retain uncertainty instead.
		for _, rel := range engineNames {
			item := engineItems[rel]
			observation := downloadNotAttempted(transfercopy.StageEngine, runErr)
			if err := recorder.Execute(item, observation.Observation); err != nil {
				runErr = errors.Join(runErr, d.stop(err))
				return
			}
		}
	}()
	for _, item := range preflight.Items {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		staging := ""
		if !item.Dir {
			staging = filepath.Join(stageDir, item.RelPath)
			if item.Route == "native" {
				staging, err = downloadTemporaryPath(destDir, item)
				if err != nil {
					return report, err
				}
			}
		}
		if err := d.admit(item, staging); err != nil {
			return report, err
		}
		if !item.Dir && item.Route != "native" {
			rel := strings.TrimPrefix(item.Name, prefix)
			engineNames = append(engineNames, rel)
			engineItems[rel] = item
			continue
		}
		var outcome downloadResult
		if item.Dir {
			outcome = materializeDownloadDirectory(ctx, filepath.Join(destDir, item.RelPath))
		} else {
			report.NativeRouted++
			outcome = nativeDownload(ctx, src, container, item, destDir, staging)
		}
		if err := d.observe(item, outcome); err != nil {
			return report, err
		}
		progress()
		if err := ctx.Err(); err != nil {
			return report, errors.Join(err, outcome.err())
		}
	}
	if len(engineNames) == 0 {
		return report, nil
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	stageAttempted = true
	if err := os.Mkdir(stageDir, 0o700); err != nil {
		return report, err
	}
	defer func() {
		if d.stopped != nil {
			report.StageCleanup = &StageCleanupResult{Retained: true}
			return
		}
		report.StageCleanup = cleanupDownloadStage(stageDir, engineItems)
	}()
	result, err := d.copy(ctx, src, engine, srcSpec+"/"+strings.TrimSuffix(prefix, "/"), stageDir, engineNames)
	if err == nil {
		err = transfercopy.ResultFailure(result)
	}
	if err != nil {
		for _, rel := range engineNames {
			outcome := downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageEngine, transfercopy.CodeUnknown, "engine", err)
			if observeErr := d.observe(engineItems[rel], outcome); observeErr != nil {
				return report, errors.Join(err, observeErr)
			}
		}
		progress()
		return report, nil
	}
	for _, rel := range engineNames {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		item := engineItems[rel]
		outcome := placeDownloaded(ctx, filepath.Join(stageDir, item.RelPath), filepath.Join(destDir, item.RelPath), item.Size)
		if err := d.observe(item, outcome); err != nil {
			return report, err
		}
		progress()
	}
	return report, ctx.Err()
}

func downloadTemporaryPath(destDir string, item DownloadItem) (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(filepath.Join(destDir, item.RelPath)), ".aeronautes-part-"+hex.EncodeToString(token[:])), nil
}

func materializeDownloadDirectory(ctx context.Context, target string) downloadResult {
	if err := ctx.Err(); err != nil {
		return downloadNotAttempted(transfercopy.StageCommit, err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "mkdir", err)
	}
	return observeDownloadDirectory(target)
}

func observeDownloadDirectory(target string) downloadResult {
	info, err := os.Stat(target)
	if err == nil && !info.IsDir() {
		err = errors.New("materialized download marker is not a directory")
	}
	if err != nil {
		return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "directory-verify", err)
	}
	return downloadVerified(0)
}

func placeDownloaded(ctx context.Context, staged, target string, wantSize int64) downloadResult {
	info, err := os.Stat(staged)
	if err != nil {
		return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeMissing, "place", fmt.Errorf("missing after download (silent drop?): %w", err))
	}
	if info.Size() != wantSize {
		return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "place", fmt.Errorf("size %d != object %d", info.Size(), wantSize))
	}
	if err := ctx.Err(); err != nil {
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIncomplete, "place", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "place", err)
	}
	if err := ctx.Err(); err != nil {
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIncomplete, "place", err)
	}
	if err := os.Link(staged, target); err != nil {
		return downloadPlacementFailed("place", err)
	}
	result := downloadVerified(wantSize)
	result.addIssue(transfercopy.StageCleanup, "staging-cleanup", os.Remove(staged))
	return result
}

func downloadPlacementFailed(label string, err error) downloadResult {
	result := downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, label, err)
	if errors.Is(err, os.ErrExist) {
		result.Result = transfercopy.ResultConflict
		result.Diagnostic.Code = transfercopy.CodeConflict
	}
	return result
}

func nativeDownload(ctx context.Context, src *swiftcontrol.Session, container string, item DownloadItem, destDir, temporary string) downloadResult {
	if err := ctx.Err(); err != nil {
		return downloadNotAttempted(transfercopy.StageSourceRead, err)
	}
	body, _, err := src.OpenObject(ctx, container, item.Name)
	if err != nil {
		return downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-get", err)
	}
	return nativeDownloadBody(ctx, body, item, destDir, temporary)
}

func nativeDownloadBody(ctx context.Context, body io.ReadCloser, item DownloadItem, destDir, temporary string) downloadResult {
	if err := ctx.Err(); err != nil {
		if closeErr := body.Close(); closeErr != nil {
			result := downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-close", closeErr)
			result.failure = errors.Join(err, closeErr)
			return result
		}
		return downloadNotAttempted(transfercopy.StageSourceRead, err)
	}
	target := filepath.Join(destDir, item.RelPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeIO, "native-get", errors.Join(err, body.Close()))
	}
	if err := ctx.Err(); err != nil {
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeIncomplete, "native-get", errors.Join(err, body.Close()))
	}
	// The admitted pathname must be the only attempted temporary name.
	// Failed exclusive creation gives no authority to unlink that entry.
	temp, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		result := downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeIO, "native-get", errors.Join(err, body.Close()))
		if errors.Is(err, os.ErrExist) {
			result.Result = transfercopy.ResultConflict
			result.Diagnostic.Code = transfercopy.CodeConflict
		}
		return result
	}
	return finishNativeDownload(ctx, body, temp, temporary, target, item.Size)
}

func (d *downloadRun) copy(ctx context.Context, session *swiftcontrol.Session, engine Engine,
	src, dst string, names []string) (*rclone.Result, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if d.stopped != nil {
			return nil, d.stopped
		}
		result, err := engine.Copy(ctx, src, dst, names, nil)
		if err != nil || d.stopped != nil {
			return result, errors.Join(err, d.stopped)
		}
		if transfercopy.ResultFailure(result) != nil && attempt == 0 && transfercopy.IsAuthFailure(result) && ctx.Err() == nil {
			session.ForceReauth()
			continue
		}
		return result, nil
	}
}
