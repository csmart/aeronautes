package local

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// Upload writes each file at its name (ADR 0122 decision 3): the engine for
// the names it can carry, a source directory at a time, and one PUT of the
// application's own for a name it cannot. There is no stage name, no second
// pass and no read of a destination name before or after its write; what the
// engine or the store answered for a file is that file's outcome.
// An omitted recorder preserves direct callers' unrecorded execution mode.
func Upload(ctx context.Context, dst *swiftcontrol.Session, engine Engine, dstSpec, container string,
	preflight *UploadPreflight, onProgress func(done, total, bytes int64),
	onRecovery func(kind, container, prefix, object string) error, recorders ...*UploadRecorder) (report *Report, runErr error) {
	recorder, err := uploadRecorder(recorders)
	if err != nil {
		return nil, err
	}
	if preflight == nil || preflight.Rejected() {
		return nil, errors.New("preflight rejected the upload; nothing was written")
	}
	class := swiftcontrol.ObjectClass(preflight.LargeObjects)
	switch class {
	case "", swiftcontrol.ClassDLO:
	case swiftcontrol.ClassSLO:
		if preflight.ChunkBytes <= 0 {
			return nil, errors.New("publishing static large objects needs the engine's chunk size; nothing was written")
		}
	default:
		return nil, fmt.Errorf("unknown large object kind %q; nothing was written", preflight.LargeObjects)
	}
	for _, item := range preflight.Items {
		if !validUploadPath(item.Path) {
			return nil, errors.New(uploadPathRefusal)
		}
	}
	u := &uploadRun{recorder: recorder, report: &Report{}}
	u.static = staticManifests{known: preflight.StaticManifestsReturned, returned: preflight.StaticManifestsReturned}
	report = u.report
	total := int64(len(preflight.Items))
	progress := func() {
		if onProgress != nil {
			onProgress(report.Done+u.failed, total, report.Bytes)
		}
	}
	// Engine-route items go to the engine a source directory at a time, each
	// group to its own destination directory, so the engine is given plain
	// file names and writes each one at its object's name.
	groups := map[string]*uploadGroup{}
	var order []string // group keys in first-seen order
	defer func() {
		if runErr == nil || recorder == nil {
			return
		}
		// Buffered groups never entered an engine; a missing final record
		// must not silently suggest an attempt when storage can retain this fact.
		for _, key := range order {
			group := groups[key]
			if group == nil {
				continue // this group reached its engine and was deleted
			}
			for _, item := range group.items {
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
		if item.Route == "native" {
			// A native-routed item is a unit of its own; an engine-routed
			// one is only buffered here and pauses with its group below.
			if err := u.pause(ctx); err != nil {
				return report, err
			}
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if err := u.admit(item); err != nil {
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
			if err := u.settle(item, result, progress); err != nil {
				return report, err
			}
			continue
		}
		dir, into := filepath.Dir(item.Path), destinationDir(item)
		key := dir + "\x00" + into
		group, exists := groups[key]
		if !exists {
			group = &uploadGroup{dir: dir, into: into}
			groups[key] = group
			order = append(order, key)
		}
		group.items = append(group.items, item)
	}
	for _, key := range order {
		if err := u.pause(ctx); err != nil {
			return report, err
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		group := groups[key]
		items := group.items
		var names []string
		for _, item := range items {
			names = append(names, filepath.Base(item.Path))
		}
		delete(groups, key)
		answers := rclone.NewAnswers()
		segments := map[string]rclone.SegmentPrefix{}
		heard := func(event rclone.Event) {
			if p, ok := rclone.PrefixFromEvent(event); ok {
				// The engine puts a file's segments under the file's own
				// name and a directory it makes for this upload, so the
				// path it reports says which file they are.
				p = p.Canonical()
				name := path.Dir(strings.TrimSuffix(strings.TrimPrefix(p.Prefix, group.into), "/"))
				if segments[name] != p {
					segments[name] = p
					report.CapturedPrefixes = append(report.CapturedPrefixes, p)
					if onRecovery != nil && u.stopped == nil {
						_ = u.stop(onRecovery("captured", p.Container, p.Prefix, ""))
					}
				}
			}
			answers.Hear(event)
		}
		result, engineErr := u.copy(ctx, dst, engine, group.dir, strings.TrimSuffix(dstSpec+"/"+group.into, "/"), names, heard)
		if engineErr == nil {
			engineErr = transfercopy.ResultFailure(result)
		}
		// The engine has had the whole folder, so every file of it gets its
		// outcome, also when the run was stopped while the engine ran: a
		// file the engine wrote is written whether or not the job can go on.
		// A stopped run sends nothing further, so a file it segmented stays
		// the dynamic object it is. Only an item record that cannot itself
		// be kept ends the recording, because the rest have nowhere to go.
		for _, item := range items {
			outcome := engineOutcome(answers, item, engineErr)
			// The engine uploads a file larger than the chunk size in
			// segments, behind a dynamic manifest at the file's name.
			if u.stopped == nil && outcome.Result == transfercopy.ResultCopied && class == swiftcontrol.ClassSLO && item.Size > preflight.ChunkBytes {
				outcome = u.makeStatic(ctx, dst, container, item, segments[filepath.Base(item.Path)], preflight.ChunkBytes)
			}
			if err := u.observe(item, outcome); err != nil {
				break
			}
			progress()
		}
		if u.stopped != nil {
			return report, u.stopped
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
	}
	return report, nil
}

func (u *uploadRun) copy(ctx context.Context, session *swiftcontrol.Session, engine Engine,
	src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	for attempt := 0; ; attempt++ {
		result, err := engine.Copy(ctx, src, dst, names, onEvent)
		if u.stopped != nil || err != nil {
			return result, err
		}
		if transfercopy.ResultFailure(result) != nil && attempt == 0 && transfercopy.IsAuthFailure(result) && ctx.Err() == nil {
			session.ForceReauth()
			continue
		}
		return result, nil
	}
}

// uploadGroup is one engine copy: the files of one source directory that go
// to one destination directory.
type uploadGroup struct {
	dir, into string
	items     []UploadItem
}

// destinationDir is the directory part of an item's object name,
// slash-terminated, or empty for a name at the container's top. The preview
// admits only a name that ends in the file's own name, so the engine, given
// this directory and the file, writes exactly the item's name.
func destinationDir(item UploadItem) string {
	return item.Name[:strings.LastIndex(item.Name, "/")+1]
}

// engineOutcome is one file's outcome from what the engine said (ADR 0122
// decision 7). A file the engine reported as copied is written. One it
// reported an error for failed with that error. One it said nothing about is
// unknown when the engine itself stopped in failure, and was not written
// when the engine finished cleanly: the pinned engine lists every file it
// writes.
func engineOutcome(answers rclone.Answers, item UploadItem, engineErr error) uploadResult {
	name := filepath.Base(item.Path)
	switch {
	case answers.Copied[name]:
		return uploadDone(item)
	case answers.Same[name]:
		return uploadSame()
	case answers.Refused[name] != "":
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageEngine, transfercopy.CodeIO, "engine", errors.New(answers.Refused[name]))
	case engineErr != nil:
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageEngine, transfercopy.CodeUnknown, "engine",
			fmt.Errorf("the engine stopped without reporting this file: %w", engineErr))
	}
	return uploadFailed(transfercopy.EffectNotAttempted, transfercopy.StageEngine, transfercopy.CodeIncomplete, "engine",
		errors.New("the engine finished without reporting this file as written"))
}

// uploadSame is a file a confirmed upload left because the engine found the
// object at its name the same as the file (ADR 0122 decision 12): verified,
// not copied, and not published again.
func uploadSame() uploadResult {
	return uploadResult{Observation: transfercopy.Observation{Result: transfercopy.ResultSame, Effect: transfercopy.EffectNotAttempted},
		verification: &transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}}
}

// uploadDone is a file the engine or the store answered for: it is written
// at its name. That answer is also the item's second record, which a
// transfer keeps until the removal step of ADR 0122: it states the answer
// that was taken, not a read of the name.
func uploadDone(item UploadItem) uploadResult {
	return uploadResult{Observation: transfercopy.Observation{
		Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: item.Size},
		verification: &transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}}
}

// putFailed puts the store's answer to a refused or unanswered PUT in the
// record's words (ADR 0122 decisions 1 and 7): a refusal wrote nothing, and
// no answer or a server error leaves it unknown.
func putFailed(label string, err error) uploadResult {
	var refused *swiftcontrol.SwiftError
	if errors.As(err, &refused) && refused.HTTPStatus >= 400 && refused.HTTPStatus < 500 {
		return uploadFailed(transfercopy.EffectNotAttempted, transfercopy.StageCommit, transfercopy.CodeIO, label, err)
	}
	return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeUnknown, label, err)
}

type localSource interface {
	io.ReadSeekCloser
}

// heldDigest is the MD5 of the held file, read for that purpose and then
// rewound for the body. The header must precede the body, so the
// descriptor is read twice; taking the digest from the bytes already
// sent would be no declaration at all (ADR 0080 decisions 1 and 2).
func heldDigest(file io.ReadSeeker) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	sum := md5.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// modTimeHeader is the metadata header under which the engine records a
// file's modification time on every object it uploads, read from the file
// for the application's own writes: an uploaded object says when its file
// last changed whichever route wrote it.
func modTimeHeader(path string) (name, value string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	name, value = swiftcontrol.EngineModTime(info.ModTime())
	return name, value, nil
}

// nativeUpload sends a file whose name the engine cannot carry as one PUT
// that declares the file's digest, which the store checks against the bytes
// that arrive (ADR 0122 decision 3). It sends no create-only condition and
// reads nothing back: an accepted PUT is the file at its name, and a body
// the store refuses against the digest leaves the name as it was.
func nativeUpload(ctx context.Context, dst *swiftcontrol.Session, container string, item UploadItem, file localSource) (result uploadResult) {
	defer func() { result.addIssue(transfercopy.StageSourceRead, "native-close", file.Close()) }()
	declared, err := heldDigest(file)
	if err != nil {
		return uploadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-digest", err)
	}
	mtime, modified, err := modTimeHeader(item.Path)
	if err != nil {
		return uploadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-mtime", err)
	}
	if _, err := dst.PutObject(ctx, container, item.Name, file, "application/octet-stream", map[string]string{"ETag": declared, mtime: modified}); err != nil {
		if swiftcontrol.IsDeclaredDigestRefused(err) {
			return uploadFailed(transfercopy.EffectNotAttempted, transfercopy.StageCommit, transfercopy.CodeUnverified, "native-put",
				fmt.Errorf("the file changed under the upload or its bytes did not survive the network: the store refused them against the declared digest %s: %w", declared, err))
		}
		return putFailed("native-put", err)
	}
	return uploadDone(item)
}
