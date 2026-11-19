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

// downloadPartPrefix names the temporary file of a download the application
// writes itself, beside the file it will become.
const downloadPartPrefix = ".aeronautes-part-"

const downloadEnginePathnameRefusal = "the selected directory's pathname no longer names it and the engine can only be given a pathname; engine items were not attempted"

// downloadEngineUncontainedRefusal gates the engine route where the child
// cannot be anchored to the held directory: the child would write through a
// pathname during its run, which no later check can contain.
const downloadEngineUncontainedRefusal = "engine downloads are refused on this platform until the engine's destination can be anchored to the held directory; engine items were not attempted"

// Download opens the selected directory from its pathname for this call
// and runs DownloadInto. Direct callers keep their unrecorded mode; the
// service passes the handle it captured at the pick instead.
func Download(ctx context.Context, src *swiftcontrol.Session, engine Engine, srcSpec, container, prefix, destDir string,
	preflight *DownloadPreflight, onProgress func(done, same, total, bytes int64), recorders ...*DownloadRecorder) (report *Report, runErr error) {
	destination, err := OpenDestinationRoot(destDir)
	if err != nil {
		if !validDownloadRoot(destDir) {
			return nil, err
		}
		return nil, fmt.Errorf("cannot inspect the selected destination directory: %w", err)
	}
	defer func() { runErr = errors.Join(runErr, destination.Close()) }()
	return DownloadInto(ctx, src, engine, srcSpec, container, prefix, destination, preflight, onProgress, recorders...)
}

// checkDownloadRequest refuses a download whose plan, names or destination
// are not what a download may act on: a missing or rejected preflight, a
// prefix that is not clean, names that would collide or alias under the
// destination's own rules, and a destination that is gone. The first refusal
// is the answer; nothing is written before every check passes. A planned
// path that has changed since the preview is not refused here for the whole
// run: each file meets it on its own (ADR 0122 decision 5), a file that has
// appeared as a conflict and a folder that is no longer a folder as a
// failure of the files under it.
func checkDownloadRequest(prefix string, destination *DestinationRoot, preflight *DownloadPreflight) error {
	if preflight == nil {
		return errors.New("download needs a preflight plan")
	}
	if preflight.Rejected() {
		return errors.New("preflight rejected the download; nothing was written")
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
	return nil
}

// DownloadInto writes each file in the folder the operator chose (ADR 0122
// decision 5). The engine writes there itself, anchored to the held folder
// and told to leave alone a file that exists; a name it cannot carry is read
// by the application and given its name by a link that does not replace.
// There is no stage folder and no placing step, and nothing is read back or
// compared: what the engine or the store answered for a file is that file's
// outcome. Every local effect goes through the held destination, never its
// pathname. Omitted recording preserves direct callers' unrecorded mode.
func DownloadInto(ctx context.Context, src *swiftcontrol.Session, engine Engine, srcSpec, container, prefix string, destination *DestinationRoot,
	preflight *DownloadPreflight, onProgress func(done, same, total, bytes int64), recorders ...*DownloadRecorder) (report *Report, runErr error) {
	if destination == nil {
		return nil, errors.New("download needs the held destination directory")
	}
	recorder, err := downloadRecorder(recorders)
	if err != nil {
		return nil, err
	}
	if err := checkDownloadRequest(prefix, destination, preflight); err != nil {
		return nil, err
	}
	d := &downloadRun{recorder: recorder, report: &Report{}}
	report = d.report
	total := int64(len(preflight.Items))
	progress := func() {
		if onProgress != nil {
			onProgress(report.Done, report.Same, total, report.Bytes)
		}
	}
	d.noteDisplacement(destination, "before the first effect")
	defer d.noteDisplacement(destination, "after the last effect")
	var engineNames []string
	engineItems := map[string]DownloadItem{}
	// What has been admitted and not yet given to an engine gets a record
	// when the run ends early, so no admitted item is left without one.
	pending := 0
	defer func() {
		if runErr == nil || recorder == nil {
			return
		}
		for _, rel := range engineNames[pending:] {
			observation := downloadNotAttempted(transfercopy.StageEngine, runErr)
			if err := recorder.Execute(engineItems[rel], observation.Observation); err != nil {
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
		if err := d.admit(item); err != nil {
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
			outcome = nativeDownload(ctx, src, container, item, destination, preflight.Replacing)
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
	// Where the platform cannot anchor the child to the held folder, the
	// engine route is refused outright (K-XFER-011 asks for containment,
	// not detection). An engine that can only be given a pathname on a
	// platform with the anchor, which is only an in-process test engine,
	// must not be given a pathname that no longer names the directory
	// picked; its items are refused before anything is written.
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
			pending = len(engineNames)
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
	source := srcSpec + "/" + strings.TrimSuffix(prefix, "/")
	// The engine-routed names go to the child in bounded batches (ADR 0074
	// decision 3), and each batch's files have their outcome before the
	// next child starts.
	for _, batch := range downloadBatches(engineNames, engineItems) {
		// The head of a batch is the only place this loop parks.
		if err := d.pause(ctx); err != nil {
			return report, err
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		pending += len(batch)
		if err := d.engineBatch(ctx, src, engine, source, destination, batch, engineItems, progress); err != nil {
			return report, err
		}
		progress()
		if d.stopped != nil {
			return report, d.stopped
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
	}
	return report, nil
}

// engineBatch makes the folders one batch's files need, gives the engine the
// held folder and the names, and settles each file from what the engine said
// about it (ADR 0122 decisions 5 and 7). The engine's line for a file means
// downloaded. Its line that the destination exists means the file was left
// alone, which is a conflict. Its error line means failed with that error.
// A file it said nothing about is unknown when the engine itself failed, and
// was not downloaded when it ended cleanly. A file written or left as the same
// is recorded and its progress reported when its line arrives; the rest wait
// for the end of the run.
//
// The application makes the folders, one component at a time through the
// held folder, so that a link or a mount point on the way is refused and the
// folders made are reported as before (ADR 0043); the engine then writes
// files only. A file whose folder cannot be made is not given to the engine.
//
// The engine is held to the chosen folder, not to the folders below it: it
// resolves a file's path itself, as rclone does when used alone, so a folder
// another program replaces with a link while it runs is followed. By the
// operator's decision a download works as rclone does there, and nothing is
// checked after the engine has written (ADR 0122 decision 5).
func (d *downloadRun) engineBatch(ctx context.Context, src *swiftcontrol.Session, engine Engine, source string,
	destination *DestinationRoot, batch []string, items map[string]DownloadItem, progress func()) error {
	created := map[string][]string{}
	var names []string
	for _, rel := range batch {
		item := items[rel]
		parent, made, err := destination.openDirectory(filepath.Dir(item.RelPath), true)
		created[rel] = made
		if err == nil {
			err = parent.Close()
		}
		if err != nil {
			outcome := downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageCommit, transfercopy.CodeIO, "mkdir", err)
			outcome.created = made
			if observeErr := d.observe(item, outcome); observeErr != nil {
				return observeErr
			}
			continue
		}
		names = append(names, rel)
	}
	if len(names) == 0 {
		return nil
	}
	answers := rclone.NewAnswers()
	given := make(map[string]bool, len(names))
	for _, rel := range names {
		given[rel] = true
	}
	recorded := map[string]bool{}
	heard := func(event rclone.Event) {
		answers.Hear(event)
		// A written or unchanged file is final when its line arrives (ADR
		// 0122 decision 14), so the job shows it during the run.
		rel, final := rclone.WrittenOrSame(event)
		if !final || !given[rel] || recorded[rel] || d.stopped != nil {
			return
		}
		item := items[rel]
		recorded[rel] = true
		outcome := engineDownloadOutcome(answers, rel, item, nil, nil)
		outcome.created = created[rel]
		if d.observe(item, outcome) == nil {
			progress()
		}
	}
	result, err := d.copy(ctx, src, func(copyCtx context.Context) (*rclone.Result, error) {
		return copyIntoFolder(copyCtx, engine, source, destination, names, heard)
	})
	if result == nil && err != nil {
		// The last child did not start, so nothing more was written.
		for _, rel := range names {
			if recorded[rel] {
				continue
			}
			outcome := downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageEngine, transfercopy.CodeIncomplete, "engine", err)
			outcome.created = created[rel]
			if observeErr := d.observe(items[rel], outcome); observeErr != nil {
				return errors.Join(err, observeErr)
			}
		}
		return ctx.Err()
	}
	if d.stopped != nil {
		// A record that could not be kept while the engine ran ends the
		// recording here, as one kept after the run would.
		return d.stopped
	}
	var cancelled error
	if err == nil && engineCancelled(ctx, result) {
		cancelled = ctx.Err()
	}
	if err == nil {
		err = transfercopy.ResultFailure(result)
	}
	if err != nil {
		// Interrupted, the engine removes the file it was writing; killed,
		// it leaves it under its own temporary name (measured).
		d.report.EngineStopped = true
	}
	// The engine has had the whole batch, so every file of it gets its
	// outcome, also when the run was stopped while the engine ran. Only an
	// item record that cannot itself be kept ends the recording.
	for _, rel := range names {
		if recorded[rel] {
			continue
		}
		item := items[rel]
		outcome := engineDownloadOutcome(answers, rel, item, err, cancelled)
		outcome.created = created[rel]
		if observeErr := d.observe(item, outcome); observeErr != nil {
			return errors.Join(err, observeErr)
		}
	}
	return cancelled
}

// engineDownloadOutcome is one file's outcome from what the engine said.
// engineErr is why the engine did not end normally, and cancelled is the
// cancellation when that was the whole of the reason.
func engineDownloadOutcome(answers rclone.Answers, rel string, item DownloadItem, engineErr, cancelled error) downloadResult {
	switch {
	case answers.Copied[rel]:
		return downloadVerified(item.Size)
	case answers.Same[rel]:
		return downloadSame()
	case answers.Skipped[rel]:
		result := downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageCommit, transfercopy.CodeConflict, "exists",
			errors.New("not downloaded: a file of that name is in the folder, and a download does not replace a file"))
		result.Result = transfercopy.ResultConflict
		return result
	case answers.Refused[rel] != "":
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageEngine, transfercopy.CodeIO, "engine", errors.New(answers.Refused[rel]))
	case cancelled != nil:
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageEngine, transfercopy.CodeCancelled, "engine", cancelled)
	case engineErr != nil:
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageEngine, transfercopy.CodeUnknown, "engine",
			fmt.Errorf("the engine stopped without reporting this file: %w", engineErr))
	}
	return downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageEngine, transfercopy.CodeIncomplete, "engine",
		errors.New("the engine finished without reporting this file as written; it may no longer be at the source"))
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

func engineCancelled(ctx context.Context, result *rclone.Result) bool {
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

// copyIntoFolder hands the engine the chosen folder. Where the platform
// provides it, the child's destination is anchored to the held directory so
// its own path resolution cannot be redirected; an engine that cannot be
// anchored, which is only an in-process test engine, gets the pathname.
func copyIntoFolder(ctx context.Context, engine Engine, src string, destination *DestinationRoot, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	if anchored, ok := engine.(AnchoredEngine); ok && rclone.AnchorSupported {
		dir, err := destination.root.Open(".")
		if err != nil {
			return nil, err
		}
		defer dir.Close()
		return anchored.CopyInto(ctx, src, destination.Path(), dir, names, onEvent)
	}
	return engine.Copy(ctx, src, destination.Path(), names, onEvent)
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

// nativeDownload carries a name the engine cannot: the application reads the
// object in one GET into a temporary file beside the final name, and gives
// it that name by a link, which does not replace a file that is there. A
// file that appeared since the preview is a conflict and is left as it is.
// The store's own tag for a plain object is checked against the bytes as
// they arrive, at no extra request, as the engine checks it on its route;
// nothing else is compared and nothing is read back.
func nativeDownload(ctx context.Context, src *swiftcontrol.Session, container string, item DownloadItem, destination *DestinationRoot, replacing bool) (result downloadResult) {
	if err := ctx.Err(); err != nil {
		return downloadNotAttempted(transfercopy.StageSourceRead, err)
	}
	body, _, err := src.OpenObjectChecked(ctx, container, item.Name)
	if err != nil {
		return downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-get", err)
	}
	parent, created, err := destination.openDirectory(filepath.Dir(item.RelPath), true)
	// Whatever the walk made exists from here on, whichever way this item
	// ends, so every later return carries it (ADR 0043).
	defer func() { result.created = append(created, result.created...) }()
	if err != nil {
		return downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageCommit, transfercopy.CodeIO, "native-get", errors.Join(err, body.Close()))
	}
	defer parent.Close()
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageCommit, transfercopy.CodeIO, "native-get", errors.Join(err, body.Close()))
	}
	temporary := downloadPartPrefix + hex.EncodeToString(token[:])
	display := filepath.Join(destination.Path(), filepath.Dir(item.RelPath), temporary)
	file, err := parent.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageCommit, transfercopy.CodeIO, "native-get", errors.Join(atPath(err, "open", display), body.Close()))
	}
	// The temporary is removed however the item ends: once the file has its
	// name by a link it is a second name for it, and before that it is a
	// part. Renamed, it is gone already. One that cannot be removed is still
	// in the folder, and the item says so.
	renamed := false
	defer func() {
		if renamed {
			return
		}
		err := unlinkFileAt(parent, temporary, display)
		if err == nil {
			return
		}
		err = fmt.Errorf("remove download temporary file: %w", err)
		if result.Result == transfercopy.ResultCopied {
			result.addIssue(transfercopy.StageCleanup, "temporary-cleanup", err)
			return
		}
		result.Effect = transfercopy.EffectUnresolved
		result.failure = errors.Join(result.failure, err)
	}()
	written, err := io.Copy(file, body)
	if err == nil {
		err = file.Sync()
	}
	// A body shorter or longer than the store said, or not matching its
	// tag, is reported while reading or closing, so both ends close before the
	// file gets its name.
	if err = errors.Join(err, file.Close(), body.Close()); err != nil {
		return downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-get", err)
	}
	if err := ctx.Err(); err != nil {
		return downloadNotAttempted(transfercopy.StageCommit, err)
	}
	base := filepath.Base(item.RelPath)
	if replacing {
		// Confirmed, the application's own route does not compare: it
		// replaces what is there, as a rename does (ADR 0122 decision 12).
		if err := parent.Rename(temporary, base); err != nil {
			return downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageCommit, transfercopy.CodeIO, "native-get",
				atPath(err, "rename", filepath.Join(destination.Path(), item.RelPath)))
		}
		renamed = true
		return downloadVerified(written)
	}
	if err := parent.Link(temporary, base); err != nil {
		result := downloadFailed(transfercopy.EffectNotAttempted, transfercopy.StageCommit, transfercopy.CodeIO, "native-get",
			atPath(err, "link", filepath.Join(destination.Path(), item.RelPath)))
		if errors.Is(err, os.ErrExist) {
			result.Result = transfercopy.ResultConflict
			result.Diagnostic.Code = transfercopy.CodeConflict
		}
		return result
	}
	return downloadVerified(written)
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
