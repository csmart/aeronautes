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

const downloadStagePrefix = ".aeronautes-stage-"

const downloadEnginePathnameRefusal = "the selected directory's pathname no longer names it and the engine can only be given a pathname; engine items were not attempted"

// downloadEngineUncontainedRefusal gates the engine route where the child
// cannot be anchored to the held stage directory: the child would write
// through a pathname during its run, which no later check can contain.
const downloadEngineUncontainedRefusal = "engine downloads are refused on this platform until the engine's destination can be anchored to the held stage directory; engine items were not attempted"

// Download opens the selected directory from its pathname for this call
// and runs DownloadInto. Direct callers keep their unrecorded mode; the
// service passes the handle it captured at the pick instead.
func Download(ctx context.Context, src *swiftcontrol.Session, engine Engine, srcSpec, container, prefix, destDir, stageID string,
	preflight *DownloadPreflight, onProgress func(done, total, bytes int64), recorders ...*DownloadRecorder) (report *Report, runErr error) {
	destination, err := OpenDestinationRoot(destDir)
	if err != nil {
		if !validDownloadRoot(destDir) {
			return nil, err
		}
		return nil, fmt.Errorf("cannot inspect the selected destination directory: %w", err)
	}
	defer func() { runErr = errors.Join(runErr, destination.Close()) }()
	return DownloadInto(ctx, src, engine, srcSpec, container, prefix, destination, stageID, preflight, onProgress, recorders...)
}

// DownloadInto keeps final placement behind checked identity and
// observations, with every local effect resolved through the held
// destination rather than its pathname. Omitted recording preserves
// direct callers' unrecorded mode.
func DownloadInto(ctx context.Context, src *swiftcontrol.Session, engine Engine, srcSpec, container, prefix string, destination *DestinationRoot, stageID string,
	preflight *DownloadPreflight, onProgress func(done, total, bytes int64), recorders ...*DownloadRecorder) (report *Report, runErr error) {
	if destination == nil {
		return nil, errors.New("download needs the held destination directory")
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
	// Both relations, exactly as the preflight applies them: files are
	// twin candidates, directories are aliases the target may merge.
	filesByLower := make(map[string]bool, len(preflight.Items))
	filesByCaseless := make(map[string]bool, len(preflight.Items))
	for _, item := range preflight.Items {
		if !validDownloadItem(prefix, item) || seen[item.Name] {
			return nil, errors.New("download item does not preserve its unique selected name and relative path")
		}
		seen[item.Name] = true
		if item.Dir {
			continue
		}
		relative := strings.TrimPrefix(item.Name, prefix)
		lower, caseless := lowercaseIdentity(relative), caselessIdentity(relative)
		if filesByLower[lower] || filesByCaseless[caseless] {
			return nil, errors.New("download selection contains names that collide on case or Unicode normalization form")
		}
		filesByLower[lower], filesByCaseless[caseless] = true, true
	}
	for _, item := range preflight.Items {
		relative := strings.TrimPrefix(item.Name, prefix)
		for separator := strings.LastIndexByte(relative, '/'); separator >= 0; separator = strings.LastIndexByte(relative, '/') {
			relative = relative[:separator]
			if filesByLower[lowercaseIdentity(relative)] || filesByCaseless[caselessIdentity(relative)] {
				return nil, errors.New("download selection needs the same path as a file and a directory")
			}
		}
	}
	if removed, err := destination.Removed(); err != nil {
		return nil, fmt.Errorf("cannot inspect the selected destination directory: %w", err)
	} else if removed {
		return nil, errors.New("the selected destination directory was deleted after it was picked; pick a directory again")
	}
	if err := checkDownloadDestination(destination.root, preflight.Items); err != nil {
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
	// Record text keeps the picker spelling; no effect resolves it.
	destDir := destination.Path()
	stageName := downloadStagePrefix + stageID
	stageDir := filepath.Join(destDir, stageName)
	d.noteDisplacement(destination, "before the first effect")
	defer d.noteDisplacement(destination, "after the last effect")
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
			outcome = materializeDownloadDirectory(ctx, destination, item.RelPath)
		} else {
			report.NativeRouted++
			outcome = nativeDownload(ctx, src, container, item, destination, staging, preflight.nativeSource[item.Name])
		}
		if err := d.observe(item, outcome); err != nil {
			return report, err
		}
		progress()
		if err := ctx.Err(); err != nil {
			return report, errors.Join(err, outcome.err(), d.confirmNativePublications(destination))
		}
	}
	if err := d.confirmNativePublications(destination); err != nil {
		return report, err
	}
	if len(engineNames) == 0 {
		return report, nil
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	// Where the platform cannot anchor the child to the held stage, the
	// engine route is refused outright (K-XFER-011 asks for containment,
	// not detection). An engine that can only be given a pathname on a
	// platform with the anchor, which is only an in-process test engine,
	// must not be given a pathname that no longer names the directory
	// picked; its items are refused before any stage exists. An anchored
	// engine resolves through the held stage and continues, as decision
	// 2 of the contract says.
	if _, anchored := engine.(AnchoredEngine); !anchored || !rclone.AnchorSupported {
		var err error
		if !rclone.AnchorSupported {
			err = errors.New(downloadEngineUncontainedRefusal)
		} else if displaced, displacedErr := destination.Displaced(); displacedErr != nil {
			err = displacedErr
		} else if displaced {
			err = errors.New(downloadEnginePathnameRefusal)
		}
		if err != nil {
			for _, rel := range engineNames {
				outcome := downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageEngine, transfercopy.CodeIncomplete, "engine", err)
				if observeErr := d.observe(engineItems[rel], outcome); observeErr != nil {
					return report, errors.Join(err, observeErr)
				}
			}
			progress()
			return report, nil
		}
	}
	stageAttempted = true
	stage, err := openDownloadStage(destination, stageName)
	if err != nil {
		return report, err
	}
	defer func() {
		if closeErr := stage.Close(); closeErr != nil {
			report.postCommitIssue(stageName, "stage-close", closeErr)
		}
	}()
	defer func() {
		if d.stopped != nil {
			// Recording failed, so cleanup was never attempted and the
			// stage is still there. Both facts are stated (ADR 0048).
			report.StageCleanup = &StageCleanupResult{Attempted: false, Retained: true}
			return
		}
		report.StageCleanup = cleanupDownloadStage(destination, stage, stageName, engineItems)
	}()
	source := srcSpec + "/" + strings.TrimSuffix(prefix, "/")
	result, err := d.copy(ctx, src, func(copyCtx context.Context) (*rclone.Result, error) {
		return copyIntoStage(copyCtx, engine, source, stage, engineNames)
	})
	cancelled := err == nil && downloadEngineCancelled(ctx, result)
	if err == nil {
		err = transfercopy.ResultFailure(result)
	}
	if err != nil {
		code := transfercopy.CodeUnknown
		if cancelled {
			code = transfercopy.CodeCancelled
			err = ctx.Err()
		}
		for _, rel := range engineNames {
			outcome := downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageEngine, code, "engine", err)
			if observeErr := d.observe(engineItems[rel], outcome); observeErr != nil {
				return report, errors.Join(err, observeErr)
			}
		}
		progress()
		if cancelled {
			return report, err
		}
		return report, nil
	}
	// The engine was given the stage by name. Bytes that reached a
	// stage the name no longer denotes are not placed from anywhere.
	displaced, err := stage.displacedAt(destination.root, stageName)
	if err == nil && displaced {
		err = fmt.Errorf("stage directory %q was replaced under the selected directory while the engine ran; staged bytes were not placed", stageName)
	}
	if err != nil {
		for _, rel := range engineNames {
			outcome := downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "place", err)
			if observeErr := d.observe(engineItems[rel], outcome); observeErr != nil {
				return report, errors.Join(err, observeErr)
			}
		}
		progress()
		return report, nil
	}
	for index, rel := range engineNames {
		if err := ctx.Err(); err != nil {
			// The staged bytes of every item not yet placed stay in the
			// stage; each such item gets the same record a cancel seen
			// inside placement gives, so timing never changes the ledger.
			for _, remaining := range engineNames[index:] {
				if observeErr := d.observe(engineItems[remaining], downloadUnplacedAfterCancel(err)); observeErr != nil {
					return report, errors.Join(err, observeErr)
				}
			}
			return report, err
		}
		item := engineItems[rel]
		outcome := placeDownloaded(ctx, destination, stage, stageName, item)
		if err := d.observe(item, outcome); err != nil {
			return report, err
		}
		progress()
	}
	return report, ctx.Err()
}

func downloadEngineCancelled(ctx context.Context, result *rclone.Result) bool {
	// A concurrent cancellation must not conceal an independent engine
	// failure or incomplete log when selecting the terminal job state.
	if !errors.Is(ctx.Err(), context.Canceled) || result == nil || !result.Cancelled ||
		result.LogStreamErr != "" || len(result.Errors) != 0 || result.Stats.Errors != 0 {
		return false
	}
	switch result.ExitCode {
	case 0, 130, -1:
		return true
	default:
		return false
	}
}

// downloadUnplacedAfterCancel is the one record a staged-but-unplaced
// item carries after a cancel: the placement failed at the commit stage,
// the effect is unresolved because the child wrote the stage, and the
// diagnostic names the cancellation; the staging location recorded at
// admission says where the bytes are.
func downloadUnplacedAfterCancel(err error) downloadResult {
	return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIncomplete, "place", err)
}

// noteDisplacement records when the captured pathname stops naming the
// held directory. Effects continue through the handle either way; the
// operator learns that the directory picked is no longer where the
// records say it was.
func (d *downloadRun) noteDisplacement(destination *DestinationRoot, when string) {
	displaced, err := destination.Displaced()
	note := ""
	switch {
	case err != nil:
		note = fmt.Sprintf("the selected directory could not be re-identified by its pathname %s: %v", when, err)
	case displaced:
		note = fmt.Sprintf("the selected directory was relocated or replaced at its pathname %s; effects continued into the directory picked", when)
	default:
		return
	}
	if d.report.RootDisplacement != "" {
		d.report.RootDisplacement += "; "
	}
	d.report.RootDisplacement += note
}

// openDownloadStage creates the stage beneath the held root and binds a
// handle to the directory created, refusing a name that already exists.
func openDownloadStage(destination *DestinationRoot, stageName string) (*DestinationRoot, error) {
	if err := destination.root.Mkdir(stageName, 0o700); err != nil {
		return nil, atPath(err, "mkdir", filepath.Join(destination.Path(), stageName))
	}
	return destination.openChildDirectory(destination.root, stageName)
}

// copyIntoStage hands the engine the stage. Where the platform provides
// it, the child's destination is anchored to the held stage directory so
// its own path resolution cannot be redirected; elsewhere the child gets
// the stage pathname and the identity check after the copy governs.
func copyIntoStage(ctx context.Context, engine Engine, src string, stage *DestinationRoot, names []string) (*rclone.Result, error) {
	if anchored, ok := engine.(AnchoredEngine); ok && rclone.AnchorSupported {
		dir, err := stage.root.Open(".")
		if err != nil {
			return nil, err
		}
		defer dir.Close()
		return anchored.CopyInto(ctx, src, stage.Path(), dir, names, nil)
	}
	return engine.Copy(ctx, src, stage.Path(), names, nil)
}

func downloadTemporaryPath(destDir string, item DownloadItem) (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(filepath.Join(destDir, item.RelPath)), ".aeronautes-part-"+hex.EncodeToString(token[:])), nil
}

func materializeDownloadDirectory(ctx context.Context, destination *DestinationRoot, rel string) downloadResult {
	if err := ctx.Err(); err != nil {
		return downloadNotAttempted(transfercopy.StageCommit, err)
	}
	if rel == "" || rel == "." {
		return observeDownloadDirectory(destination.root, ".")
	}
	parent, created, err := destination.openDirectory(filepath.Dir(rel), true)
	if err != nil {
		result := downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "mkdir", err)
		result.created = created
		return result
	}
	defer parent.Close()
	base := filepath.Base(rel)
	if err := parent.Mkdir(base, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		result := downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "mkdir", atPath(err, "mkdir", filepath.Join(destination.Path(), rel)))
		result.created = created
		return result
	}
	result := observeDownloadDirectory(parent, base)
	// The item's own directory is its item record; only the ancestors
	// the walk made are reported through this channel.
	result.created = created
	return result
}

func observeDownloadDirectory(parent *os.Root, base string) downloadResult {
	info, err := parent.Lstat(base)
	if err == nil && !downloadOrdinaryDirectory(info) {
		err = errors.New("materialized download marker is not an ordinary directory")
	}
	if err != nil {
		return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "directory-verify", err)
	}
	return downloadVerified(0)
}

func placeDownloaded(ctx context.Context, destination, stage *DestinationRoot, stageName string, item DownloadItem) (result downloadResult) {
	stagedDisplay := filepath.Join(stage.Path(), item.RelPath)
	targetDisplay := filepath.Join(destination.Path(), item.RelPath)
	stagedParent, _, err := stage.openDirectory(filepath.Dir(item.RelPath), false)
	if err != nil {
		return stagedFileUnavailable(err)
	}
	defer stagedParent.Close()
	base := filepath.Base(item.RelPath)
	info, err := stagedParent.Lstat(base)
	if err != nil {
		return stagedFileUnavailable(atPath(err, "stat", stagedDisplay))
	}
	if !info.Mode().IsRegular() {
		return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "place", fmt.Errorf("staged entry %q is not a regular file", item.RelPath))
	}
	if info.Size() != item.Size {
		return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "place", fmt.Errorf("size %d != object %d", info.Size(), item.Size))
	}
	if err := ctx.Err(); err != nil {
		return downloadUnplacedAfterCancel(err)
	}
	targetParent, createdTarget, err := destination.openDirectory(filepath.Dir(item.RelPath), true)
	defer func() { result.created = append(createdTarget, result.created...) }()
	if err != nil {
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "place", err)
	}
	defer targetParent.Close()
	if err := ctx.Err(); err != nil {
		return downloadUnplacedAfterCancel(err)
	}
	publish := publication{root: destination,
		stagedRoot: stagedParent, stagedBase: base, stagedRel: filepath.Join(stageName, item.RelPath), stagedDisplay: stagedDisplay,
		targetRoot: targetParent, targetBase: base, targetRel: item.RelPath, targetDisplay: targetDisplay}
	if err := publish.publishVerified(info); err != nil {
		return downloadPlacementFailed("place", err)
	}
	placed := downloadVerified(item.Size)
	placed.addIssue(transfercopy.StageCleanup, "staging-cleanup", unlinkFileAt(stagedParent, base, stagedDisplay))
	return placed
}

// stagedFileUnavailable keeps the silent-drop diagnosis for an absent
// staged file and names any other reason the stage could not be read.
func stagedFileUnavailable(err error) downloadResult {
	if downloadChildAbsent(err) {
		return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeMissing, "place", fmt.Errorf("missing after download (silent drop?): %w", err))
	}
	return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "place", fmt.Errorf("staged file cannot be inspected through the stage: %w", err))
}

func downloadPlacementFailed(label string, err error) downloadResult {
	result := downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, label, err)
	if errors.Is(err, os.ErrExist) {
		result.Result = transfercopy.ResultConflict
		result.Diagnostic.Code = transfercopy.CodeConflict
	}
	return result
}

func nativeDownload(ctx context.Context, src *swiftcontrol.Session, container string, item DownloadItem, destination *DestinationRoot, temporary string, source downloadSourceIdentity) downloadResult {
	if err := ctx.Err(); err != nil {
		return downloadNotAttempted(transfercopy.StageSourceRead, err)
	}
	body, detail, err := src.OpenObject(ctx, container, item.Name)
	if err != nil {
		return downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-get", err)
	}
	if matchErr := source.match(detail); matchErr != nil {
		return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "native-get",
			errors.Join(fmt.Errorf("source was substituted: %w", matchErr), body.Close()))
	}
	// The open already carried the source's own detail, so a plain source
	// needs no further request and only a large one costs a listing.
	ladder, ladderErr := transfercopy.SourceLadder(ctx, src, container, detail)
	if ladderErr != nil {
		return downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIncomplete, "native-get",
			errors.Join(fmt.Errorf("source states no usable content expectation: %w", ladderErr), body.Close()))
	}
	return nativeDownloadBody(ctx, body, item, destination, temporary, ladder)
}

func nativeDownloadBody(ctx context.Context, body io.ReadCloser, item DownloadItem, destination *DestinationRoot, temporary string, ladder transfercopy.Ladder) (result downloadResult) {
	if err := ctx.Err(); err != nil {
		if closeErr := body.Close(); closeErr != nil {
			result := downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-close", closeErr)
			result.failure = errors.Join(err, closeErr)
			return result
		}
		return downloadNotAttempted(transfercopy.StageSourceRead, err)
	}
	parent, created, err := destination.openDirectory(filepath.Dir(item.RelPath), true)
	// Whatever the walk made exists from here on, whichever way this
	// item ends, so every later return carries it (ADR 0043).
	defer func() { result.created = append(created, result.created...) }()
	if err != nil {
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeIO, "native-get", errors.Join(err, body.Close()))
	}
	defer parent.Close()
	// Nothing exists for this item yet: the temporary's exclusive
	// creation is below. A cancel here is certain, not uncertain, so it
	// records what the check above and the upload twin record rather
	// than sending the operator to look for residue that cannot exist
	// (ADR 0043). Ancestors the walk created are reported separately.
	if err := ctx.Err(); err != nil {
		if closeErr := body.Close(); closeErr != nil {
			result := downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-close", closeErr)
			result.failure = errors.Join(err, closeErr)
			return result
		}
		return downloadNotAttempted(transfercopy.StageSourceRead, err)
	}
	// The admitted name must be the only attempted temporary name.
	// Failed exclusive creation gives no authority to unlink that entry.
	temporaryBase := filepath.Base(temporary)
	temp, err := parent.OpenFile(temporaryBase, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		err = atPath(err, "open", temporary)
		result := downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeIO, "native-get", errors.Join(err, body.Close()))
		if errors.Is(err, os.ErrExist) {
			result.Result = transfercopy.ResultConflict
			result.Diagnostic.Code = transfercopy.CodeConflict
		}
		return result
	}
	base := filepath.Base(item.RelPath)
	publish := publication{root: destination,
		stagedRoot: parent, stagedBase: temporaryBase, stagedRel: filepath.Join(filepath.Dir(item.RelPath), temporaryBase), stagedDisplay: temporary,
		targetRoot: parent, targetBase: base, targetRel: item.RelPath, targetDisplay: filepath.Join(destination.Path(), item.RelPath)}
	return finishNativeDownload(ctx, body, temp, parent, publish, item.Size, ladder)
}

func (d *downloadRun) confirmNativePublications(destination *DestinationRoot) error {
	if d == nil || destination == nil {
		return nil
	}
	pending := d.pendingNative
	d.pendingNative = nil
	for _, published := range pending {
		current, err := destination.root.Lstat(published.copied.publishedRel)
		if err == nil && published.copied.published != nil && os.SameFile(current, published.copied.published) {
			if d.recorder != nil && published.copied.verification != nil {
				if err := d.recorder.Verify(published.item, *published.copied.verification); err != nil {
					return d.stop(err)
				}
			}
			continue
		}
		reason := errors.New("published file no longer names the verified download")
		if err != nil {
			reason = fmt.Errorf("published file no longer names the verified download: %w", err)
		}
		if d.report.Done > 0 {
			d.report.Done--
		}
		if published.copied.CopiedBytes > 0 && d.report.Bytes >= published.copied.CopiedBytes {
			d.report.Bytes -= published.copied.CopiedBytes
		}
		failed := downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "native-get", reason)
		if len(d.report.Failures) < maxFailures {
			d.report.Failures = append(d.report.Failures, Failure{Name: published.item.Name, Stage: failed.label, Err: failed.err().Error()})
		}
		if d.recorder != nil && failed.verification != nil {
			if err := d.recorder.Verify(published.item, *failed.verification); err != nil {
				return d.stop(err)
			}
		}
	}
	return d.stopped
}

func (d *downloadRun) copy(ctx context.Context, session *swiftcontrol.Session, run func(context.Context) (*rclone.Result, error)) (*rclone.Result, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if d.stopped != nil {
			return nil, d.stopped
		}
		result, err := run(ctx)
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
