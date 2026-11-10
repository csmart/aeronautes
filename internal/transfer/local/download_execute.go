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

// checkDownloadRequest refuses a download whose plan, names or destination
// are not what a download may act on: a missing or rejected preflight, a
// stage id or prefix that is not a single clean component, names that would
// collide or alias under the destination's own rules, a destination that is
// gone, and planned paths that no longer pass inspection through the held
// destination. The first refusal is the answer; nothing is written before
// every check passes.
func checkDownloadRequest(prefix, stageID string, destination *DestinationRoot, preflight *DownloadPreflight) error {
	if preflight == nil {
		return errors.New("download needs a preflight plan")
	}
	if preflight.Rejected() {
		return errors.New("preflight rejected the download; nothing was written")
	}
	if stageID == "" || !utf8.ValidString(stageID) || strings.ContainsAny(stageID, "/\\\x00") || stageID == "." || stageID == ".." {
		return errors.New("download needs a single-component stage id (the job id)")
	}
	if !utf8.ValidString(prefix) || prefix != "" && !strings.HasSuffix(prefix, "/") {
		return errors.New("download needs an unchanged UTF-8 prefix ending in '/'")
	}
	seen := make(map[string]bool, len(preflight.Items))
	// Both relations, exactly as the preflight applies them: files are
	// twin candidates, directories are aliases the target may merge.
	filesByLower := make(map[string]bool, len(preflight.Items))
	filesByCaseless := make(map[string]bool, len(preflight.Items))
	for _, item := range preflight.Items {
		if !validDownloadItem(prefix, item) || seen[item.Name] {
			return errors.New("download item does not preserve its unique selected name and relative path")
		}
		seen[item.Name] = true
		if item.Dir {
			continue
		}
		relative := strings.TrimPrefix(item.Name, prefix)
		lower, caseless := lowercaseIdentity(relative), caselessIdentity(relative)
		if filesByLower[lower] || filesByCaseless[caseless] {
			return errors.New("download selection contains names that collide on case or Unicode normalization form")
		}
		filesByLower[lower], filesByCaseless[caseless] = true, true
	}
	for _, item := range preflight.Items {
		relative := strings.TrimPrefix(item.Name, prefix)
		for separator := strings.LastIndexByte(relative, '/'); separator >= 0; separator = strings.LastIndexByte(relative, '/') {
			relative = relative[:separator]
			if filesByLower[lowercaseIdentity(relative)] || filesByCaseless[caselessIdentity(relative)] {
				return errors.New("download selection needs the same path as a file and a directory")
			}
		}
	}
	if removed, err := destination.Removed(); err != nil {
		return fmt.Errorf("cannot inspect the selected destination directory: %w", err)
	} else if removed {
		return errors.New("the selected destination directory was deleted after it was picked; pick a directory again")
	}
	return checkDownloadDestination(destination.root, preflight.Items)
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
	if err := checkDownloadRequest(prefix, stageID, destination, preflight); err != nil {
		return nil, err
	}
	d := &downloadRun{recorder: recorder, report: &Report{}, retained: map[string]bool{}}
	defer func() { runErr = errors.Join(runErr, d.confirmNativePublications(destination)) }()
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
		if !item.Dir && item.Route == "native" {
			// A native-routed item is a unit of its own; a directory is
			// made on the way and an engine-routed name is only buffered
			// here, pausing with its batch below.
			if err := d.pause(ctx); err != nil {
				return report, err
			}
		}
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
			return report, errors.Join(err, outcome.err())
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
		report.StageCleanup = cleanupDownloadStage(destination, stage, stageName, engineItems, d.retained)
	}()
	source := srcSpec + "/" + strings.TrimSuffix(prefix, "/")
	// The engine-routed names go to the child in bounded batches; each
	// batch is staged, verified and placed before the next child starts,
	// so what an interrupted job leaves in the stage is at most one
	// batch's files (ADR 0074 decision 3). A batch that fails or cannot
	// be verified ends the run: the batches after it were never given
	// to a child, and their records say so.
	batches := downloadBatches(engineNames, engineItems)
	notAttempted := func(from int, cause error) error {
		for _, batch := range batches[from:] {
			for _, rel := range batch {
				observation := downloadNotAttempted(transfercopy.StageEngine, cause)
				if err := recorder.Execute(engineItems[rel], observation.Observation); err != nil {
					return d.stop(err)
				}
			}
		}
		return nil
	}
	for index, batch := range batches {
		// The head of a batch is the only place this loop parks: the checks
		// further down sit inside a unit, where a delivered batch is being
		// placed.
		if err := d.pause(ctx); err != nil {
			return report, errors.Join(err, notAttempted(index, err))
		}
		if err := ctx.Err(); err != nil {
			return report, errors.Join(err, notAttempted(index, err))
		}
		result, err := d.copy(ctx, src, func(copyCtx context.Context) (*rclone.Result, error) {
			return copyIntoStage(copyCtx, engine, source, stage, batch)
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
			for _, rel := range batch {
				outcome := downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageEngine, code, "engine", err)
				if observeErr := d.observe(engineItems[rel], outcome); observeErr != nil {
					return report, errors.Join(err, observeErr)
				}
			}
			progress()
			if recordErr := notAttempted(index+1, err); recordErr != nil {
				return report, errors.Join(err, recordErr)
			}
			if cancelled {
				return report, err
			}
			return report, nil
		}
		// The engine was given the stage by name. Bytes that reached a
		// stage the name no longer denotes are not placed from anywhere,
		// and no further batch is given to a child.
		displaced, err := stage.displacedAt(destination.root, stageName)
		if err == nil && displaced {
			err = fmt.Errorf("stage directory %q was replaced under the selected directory while the engine ran; staged bytes were not placed", stageName)
		}
		if err != nil {
			for _, rel := range batch {
				outcome := downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "place", err)
				if observeErr := d.observe(engineItems[rel], outcome); observeErr != nil {
					return report, errors.Join(err, observeErr)
				}
			}
			progress()
			return report, notAttempted(index+1, err)
		}
		for position, rel := range batch {
			if err := ctx.Err(); err != nil {
				// The staged bytes of every item of this batch not yet
				// placed stay in the stage; each such item gets the same
				// record a cancel seen inside placement gives, so timing
				// never changes the ledger. Later batches were never
				// given to a child.
				for _, remaining := range batch[position:] {
					if observeErr := d.observe(engineItems[remaining], downloadUnplacedAfterCancel(err)); observeErr != nil {
						return report, errors.Join(err, observeErr)
					}
				}
				return report, errors.Join(err, notAttempted(index+1, err))
			}
			item := engineItems[rel]
			outcome := placeDownloaded(ctx, src, container, destination, stage, stageName, item, preflight.nativeSource[item.Name])
			if outcome.retainStaged {
				d.retained[item.RelPath] = true
			}
			if err := d.observe(item, outcome); err != nil {
				return report, err
			}
			progress()
		}
	}
	return report, ctx.Err()
}

// downloadBatches splits the engine-routed names, in plan order, into
// batches of at most DefaultBatchNames names or DefaultBatchBytes of
// planned bytes, the copy executor's bounds; a batch closes on the item
// that reaches either bound, so no single item is refused for its size.
func downloadBatches(names []string, items map[string]DownloadItem) [][]string {
	var batches [][]string
	var batch []string
	var bytes int64
	for _, rel := range names {
		batch = append(batch, rel)
		bytes += items[rel].Size
		if len(batch) >= transfercopy.DefaultBatchNames || bytes >= transfercopy.DefaultBatchBytes {
			batches = append(batches, batch)
			batch, bytes = nil, 0
		}
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
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

// placeDownloaded verifies one engine-downloaded entry against the source
// identity the preflight captured and publishes exactly the inode it
// verified (ADR 0070). The staged entry is opened through the held stage
// and kept open from the size check to the link, so the bytes digested
// are the bytes published whatever the staged name denotes by then. A
// mismatch, a substituted source or an expectation that cannot be built
// publishes nothing and retains the staged entry for the operator, exempt
// from the stage cleanup (ADR 0048).
func placeDownloaded(ctx context.Context, src *swiftcontrol.Session, container string, destination, stage *DestinationRoot, stageName string, item DownloadItem, source downloadSourceIdentity) (result downloadResult) {
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
	// The size check stays the first comparison; a refused entry keeps
	// its bytes in the stage like every other refusal below.
	if info.Size() != item.Size {
		return downloadRetained(transfercopy.CodeUnverified, fmt.Errorf("size %d != object %d", info.Size(), item.Size))
	}
	if err := ctx.Err(); err != nil {
		return downloadUnplacedAfterCancel(err)
	}
	staged, err := openNativePublicationPin(stagedParent, base)
	if err != nil {
		return stagedFileUnavailable(atPath(err, "open", stagedDisplay))
	}
	defer func() {
		if result.published != staged {
			if closeErr := staged.Close(); closeErr != nil {
				result.addIssue(transfercopy.StageCleanup, "staged-pin-close", closeErr)
			}
		}
	}()
	verified, err := staged.Stat()
	if err != nil {
		return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "place", fmt.Errorf("staged entry %q cannot be identified: %w", item.RelPath, err))
	}
	if !verified.Mode().IsRegular() || verified.Size() != item.Size || !os.SameFile(info, verified) {
		return downloadRetained(transfercopy.CodeUnverified, fmt.Errorf("staged entry %q changed between inspection and verification", item.RelPath))
	}
	ladder, code, err := engineExpectation(ctx, src, container, item, source)
	if err != nil {
		return downloadRetained(code, err)
	}
	// The comparison is the native route's: the same ladder, the same
	// digester, over the descriptor that will be published (ADR 0045).
	digest := transfercopy.NewLadderDigest(ladder)
	read, err := io.Copy(digest, staged)
	if err != nil {
		return downloadRetained(transfercopy.CodeUnverified, fmt.Errorf("staged bytes cannot be read: %w", err))
	}
	if read != item.Size {
		return downloadRetained(transfercopy.CodeUnverified, fmt.Errorf("read %d bytes, object says %d", read, item.Size))
	}
	if _, contentErr := digest.Result(); contentErr != nil {
		return downloadRetained(transfercopy.CodeUnverified, fmt.Errorf("staged content does not match the source: %w", contentErr))
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
	// A concurrent creator keeps its target; the link never replaces.
	if err := linkDescriptorAt(staged, targetParent, base, stagedDisplay, targetDisplay); err != nil {
		return downloadPlacementFailed("place", err)
	}
	placed, err := targetParent.Lstat(base)
	if err != nil {
		return downloadRetained(transfercopy.CodeUnverified, fmt.Errorf("published file cannot be inspected after linking: %w", err))
	}
	if !os.SameFile(placed, verified) {
		// The occupant is not this executor's link, so it is not unlinked
		// (ADR 0045); the verified bytes stay in the stage.
		return downloadRetained(transfercopy.CodeUnverified, fmt.Errorf("target %q does not name the verified file after publication; the entry was left in place", targetDisplay))
	}
	outcome := downloadVerified(item.Size)
	outcome.label = "place"
	// The descriptor keeps the inode alive until the final-publication
	// check confirms the name still denotes it (ADR 0061).
	outcome.published = staged
	outcome.publishedRel = item.RelPath
	outcome.addIssue(transfercopy.StageCleanup, "staging-cleanup", unlinkFileAt(stagedParent, base, stagedDisplay))
	return outcome
}

// engineExpectation builds the content expectation for an engine item
// from the identity captured at preflight: a plain object's captured
// length and entity tag, or a large object's live segment listing or
// manifest after a HEAD that must still match the capture (ADR 0070
// decision 2). The code says whether the item is unverified (a
// substituted source) or incomplete (no usable expectation).
func engineExpectation(ctx context.Context, src *swiftcontrol.Session, container string, item DownloadItem, source downloadSourceIdentity) (transfercopy.Ladder, transfercopy.Code, error) {
	if !source.captured {
		return transfercopy.Ladder{}, transfercopy.CodeIncomplete, errors.New("the plan captured no source identity for this item")
	}
	var ladder transfercopy.Ladder
	if source.class == swiftcontrol.ClassDLO || source.class == swiftcontrol.ClassSLO {
		if src == nil {
			return transfercopy.Ladder{}, transfercopy.CodeIncomplete, errors.New("no source session to read the large object's expectation")
		}
		detail, err := src.ObjectDetail(ctx, container, item.Name)
		if err != nil {
			return transfercopy.Ladder{}, transfercopy.CodeIncomplete, fmt.Errorf("source HEAD failed: %w", err)
		}
		if matchErr := source.match(detail); matchErr != nil {
			return transfercopy.Ladder{}, transfercopy.CodeUnverified, fmt.Errorf("source was substituted: %w", matchErr)
		}
		ladder, err = transfercopy.SourceLadder(ctx, src, container, detail)
		if err != nil {
			return transfercopy.Ladder{}, transfercopy.CodeIncomplete, fmt.Errorf("source states no usable content expectation: %w", err)
		}
	} else {
		var err error
		ladder, err = transfercopy.PlainLadder(source.length, source.etag)
		if err != nil {
			return transfercopy.Ladder{}, transfercopy.CodeIncomplete, fmt.Errorf("source states no usable content expectation: %w", err)
		}
	}
	if ladder.Bytes() != item.Size {
		return transfercopy.Ladder{}, transfercopy.CodeIncomplete, fmt.Errorf("source content expectation covers %d bytes, the plan says %d", ladder.Bytes(), item.Size)
	}
	return ladder, "", nil
}

// downloadRetained is the refusal that keeps the staged entry: nothing is
// published, the item is unverified with effect unresolved, and the stage
// cleanup leaves the entry for the operator (ADR 0070 decision 3).
func downloadRetained(code transfercopy.Code, err error) downloadResult {
	result := downloadVerificationFailed(transfercopy.StageVerification, code, "place", err)
	result.retainStaged = true
	return result
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

func (d *downloadRun) closeNativePublications() error {
	var closeErr error
	for _, published := range d.pendingNative {
		if published.copied.published != nil {
			closeErr = errors.Join(closeErr, published.copied.published.Close())
		}
	}
	d.pendingNative = nil
	return closeErr
}

func (d *downloadRun) confirmNativePublications(destination *DestinationRoot) (confirmErr error) {
	if d == nil || destination == nil {
		return nil
	}
	defer func() { confirmErr = errors.Join(confirmErr, d.closeNativePublications()) }()
	for _, published := range d.pendingNative {
		observation := transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}
		current, err := destination.root.Lstat(published.copied.publishedRel)
		var pinned os.FileInfo
		if err == nil && published.copied.published != nil {
			pinned, err = published.copied.published.Stat()
		}
		if err != nil || pinned == nil || !os.SameFile(current, pinned) {
			reason := errors.New("published file no longer names the verified download")
			if err != nil {
				reason = fmt.Errorf("published file no longer names the verified download: %w", err)
			}
			// Placement happened even when the later name is missing or
			// replaced. Retain its copied bytes and reduce only verification.
			d.report.Verified--
			observation = transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectUnresolved,
				Diagnostic: transfercopy.ClassifyDiagnostic(transfercopy.StageVerification, transfercopy.CodeUnverified, reason)}
			stage := published.copied.label
			if stage == "" {
				stage = "native-get"
			}
			if len(d.report.Failures) < maxFailures {
				d.report.Failures = append(d.report.Failures, Failure{Name: published.item.Name, Stage: stage, Err: reason.Error()})
			}
		}
		if d.recorder != nil {
			if err := d.recorder.Publication(published.item, observation); err != nil {
				d.stop(err)
				confirmErr = errors.Join(confirmErr, err)
			}
		}
	}
	return confirmErr
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
