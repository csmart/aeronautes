package local

import (
	"context"
	"crypto/md5"
	"encoding/hex"
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
			if err := u.observePredecessor(ctx, dst, container, item); err != nil {
				return report, err
			}
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
			if err := u.observePredecessor(ctx, dst, container, item); err != nil {
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

// matchesStagedContent reads the staged object once and folds its bytes
// through the source's expectation. Listing evidence is deliberately not
// accepted in its place: a same-size segment replacement can leave the
// container row at its previous length and entity tag while the stored
// bytes differ, which ADR 0042 rejected and keeps as a regression
// (ADR 0080 decisions 3 and 4).
//
// It drives the project's one comparison lifecycle rather than a read of
// its own. OpenObjectContent admits only a complete response, so a
// partial body cannot be read as the whole staged object, and
// CompareContent consumes the stream to EOF and joins the close and
// cancellation failures into its verdict, so a reader that reports its
// corruption only on Close cannot be accepted as a match.
func matchesStagedContent(ctx context.Context, dst *swiftcontrol.Session, container, stagedName string,
	ladder transfercopy.Ladder) (transfercopy.Code, error) {
	body, err := dst.OpenObjectContent(ctx, container, stagedName)
	if err != nil {
		return transfercopy.CodeIncomplete, fmt.Errorf("open complete staged content: %w", err)
	}
	return transfercopy.CompareContent(ctx, body, ladder)
}

// localSource is the held local file an upload sends. The native route
// takes its digest and its body from this one descriptor, so a pathname
// replaced under a running job changes neither (ADR 0080 decision 2).
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

// localDigest is the MD5 of the file at path. The engine route holds no
// descriptor of its own, so its staged comparison can only ask what the
// pathname holds now; ADR 0080's consequences state what that costs.
func localDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	sum := md5.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// observePredecessor reads what is at a name and records it, immediately
// before that name is published over. Both routes call it at the same
// point, so a replacement cannot publish without the ledger saying what
// was there (ADR 0081 decision 7).
//
// A name that is not being replaced reads nothing: the create's own
// conditional is what protects it, and a HEAD here would be a request
// made for nothing. A name that has gone between the preflight and now is
// recorded as absent rather than refused, because publishing over nothing
// is what a create does and the operator acknowledged the stronger act.
func (u *uploadRun) observePredecessor(ctx context.Context, dst *swiftcontrol.Session,
	container string, item UploadItem) error {
	if !item.Replaces {
		return nil
	}
	detail, err := dst.ObjectDetail(ctx, container, item.Name)
	switch {
	case swiftcontrol.IsNotFound(err):
		return u.replacing(item, ObservedPredecessor{})
	case err != nil:
		return u.stop(fmt.Errorf("reading what is at %s before replacing it: %w", item.Name, err))
	}
	return u.replacing(item, ObservedPredecessor{
		Present: true,
		ETag:    strings.Trim(detail.ETag, `"`), Length: detail.ContentLength,
		LastModified: detail.LastModified, Class: string(detail.Class),
		Fragments: fragmentInventory(ctx, dst, container, item.Name, detail),
	})
}

// fragmentInventory reads what a replaced manifest named, before the
// replacement, because once the manifest is gone the set cannot be
// recovered (ADR 0081 decision 5). It deletes nothing and authorises
// nothing: creation provenance is not authority over other references, so
// the inventory exists to make the space visible and no more.
//
// A failure to read it does not stop the replacement. The operator
// acknowledged replacing the object, and refusing over an inventory would
// trade the thing they asked for against a record; the record says it
// could not be taken instead.
func fragmentInventory(ctx context.Context, dst *swiftcontrol.Session, container, name string,
	detail *swiftcontrol.ObjectDetail) FragmentInventory {
	switch detail.Class {
	case swiftcontrol.ClassSLO:
		// Read strictly and within this build's own bounds. The display
		// reader accepts a partial response, a ranged one and one with
		// trailing garbage, any of which would be credited here as the
		// exact list of what is about to be replaced. A manifest this build
		// will not read whole is reported as one it could not inventory,
		// which is the honest answer, because a static list cannot be read
		// again once the manifest is gone (ADR 0081 decision 5).
		entries, err := dst.SLOManifestEntries(ctx, container, name, MaxManifestEntries, MaxManifestBytes)
		if err != nil {
			return FragmentInventory{Unread: "the static manifest could not be read whole within this build's bounds"}
		}
		// A static manifest lists its members exactly, so this set is the
		// set, and every entry is recorded in its own position. What it is
		// not is a graph: each entry says whether it named something other
		// than a whole remote object.
		inventory := FragmentInventory{Complete: true}
		for _, entry := range entries {
			switch segment := entry.(type) {
			case swiftcontrol.ObjectSegment:
				inventory.record(Fragment{Container: segment.Path.Container, Name: segment.Path.Object,
					Shape: segmentShape(segment)})
			case swiftcontrol.InlineDataSegment:
				// Inline data lives inside the manifest being replaced. It
				// has no remote object at all, so it is recorded as the
				// shape it is, in its position, and never as a name that
				// could outlive the manifest.
				inventory.record(Fragment{Shape: FragmentInline})
			default:
				// An entry shape this build does not recognise cannot be
				// counted as a part it might not be, and cannot be dropped
				// either: its position is part of what the manifest named.
				inventory.record(Fragment{Shape: FragmentUnknown})
				inventory.Complete = false
			}
		}
		return inventory
	case swiftcontrol.ClassDLO:
		segmentContainer, prefix, ok := strings.Cut(detail.Manifest, "/")
		if !ok || segmentContainer == "" {
			return FragmentInventory{Unread: "the dynamic manifest names no segment prefix"}
		}
		// Listed rather than named, so it is a lower bound: a listing on an
		// eventually consistent store may omit a segment that exists.
		//
		// Deep, because a segment name may contain a slash and the default
		// delimiter view would group everything below it into one
		// pseudo-directory row that names no object. A manifest whose
		// segments sit below child paths would otherwise be inventoried as
		// holding nothing at all.
		inventory := FragmentInventory{Prefix: detail.Manifest}
		for entry, err := range dst.Objects(ctx, segmentContainer,
			swiftcontrol.ObjectListOptions{Prefix: prefix, Deep: true}) {
			if err != nil {
				inventory.Unread = "the segment prefix could not be listed in full"
				break
			}
			object, ok := entry.(swiftcontrol.ObjectEntry)
			if !ok {
				continue
			}
			// A listing names objects and nothing about how the manifest
			// referenced them, so every listed member is a whole object as
			// far as this record can say.
			inventory.recordListed(Fragment{Container: segmentContainer, Name: object.Name, Shape: FragmentWhole})
		}
		return inventory
	default:
		return FragmentInventory{Complete: true}
	}
}

// segmentShape is what one static object entry referenced.
func segmentShape(segment swiftcontrol.ObjectSegment) FragmentShape {
	switch {
	case segment.Range != nil && segment.NestedSLO:
		return FragmentRangedNested
	case segment.Range != nil:
		return FragmentRanged
	case segment.NestedSLO:
		return FragmentNested
	}
	return FragmentWhole
}

// publishHeaders are the conditions a publication carries. A create keeps
// the conditional that makes it race-free: the store refuses when the name
// exists and the first writer wins. A replacement drops it, because the
// name existing is the point, and that is the whole of what an
// acknowledged overwrite changes about the write (ADR 0081 decision 6).
//
// The declared entity tag is kept either way, wherever the route declares
// one, so a replacement whose bytes did not survive the wire is refused by
// the store with the prior object intact, as measured.
func publishHeaders(item UploadItem, declared string) map[string]string {
	headers := map[string]string{}
	if declared != "" {
		headers["ETag"] = declared
	}
	if !item.Replaces {
		headers["If-None-Match"] = "*"
	}
	return headers
}

func nativeUpload(ctx context.Context, dst *swiftcontrol.Session, container string, item UploadItem, file localSource) (result uploadResult) {
	defer func() { result.addIssue(transfercopy.StageSourceRead, "native-close", file.Close()) }()
	declared, err := heldDigest(file)
	if err != nil {
		return uploadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-digest", err)
	}
	headers := publishHeaders(item, declared)
	if _, err := dst.PutObject(ctx, container, item.Name, file, "application/octet-stream", headers); err != nil {
		if swiftcontrol.IsPreconditionFailed(err) {
			return uploadConflict("native-put", err)
		}
		if swiftcontrol.IsDeclaredDigestRefused(err) {
			// A PUT was invoked, so the effect this contract records is
			// unresolved: what the store did with the request is its
			// answer to report, not ours to assume (ADR 0080 decision 7).
			return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeUnverified, "native-put",
				fmt.Errorf("the file changed under the upload or its bytes did not survive the network: the store refused them against the declared digest %s: %w", declared, err))
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
	if _, err := dst.ObjectDetail(ctx, container, item.Name); err == nil {
		// A name that is live is a conflict for a create and the expected
		// state for a replacement the operator acknowledged.
		if !item.Replaces {
			return uploadConflict("commit-conflict", errors.New("destination appeared since preflight"))
		}
	} else if !swiftcontrol.IsNotFound(err) {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeUnknown, "commit-rehead", err)
	}
	// What the engine staged is compared with the file this job planned
	// to send, before any of it is published. A large object's assembled
	// bytes are read once and folded through that expectation; a plain
	// staged object is compared by its own object HEAD's entity tag,
	// which ADR 0042 names as the one pair it does not read. A
	// disagreement publishes nothing, and the staged bytes stay where
	// they are under ADR 0020 (ADR 0080 decisions 3, 4, 5, 7 and 8).
	//
	// The read is for every class that is not plain, not for the dynamic
	// manifest alone: a static large object's entity tag is the digest of
	// its segments' entity tags, so it can never equal the file's own
	// digest, and comparing the two would refuse content that matches.
	declared, err := localDigest(item.Path)
	if err != nil {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeIncomplete, "stage-expectation", err)
	}
	ladder, err := transfercopy.PlainLadder(item.Size, declared)
	if err != nil {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeIncomplete, "stage-expectation", err)
	}
	if staged.Class != swiftcontrol.ClassPlain {
		if code, contentErr := matchesStagedContent(ctx, dst, container, stagedName, ladder); contentErr != nil {
			return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, code, "stage-content",
				fmt.Errorf("the staged content does not match the file this job planned to send: %w", contentErr))
		}
	} else if stagedTag := strings.Trim(staged.ETag, `"`); staged.ContentLength != item.Size || !strings.EqualFold(stagedTag, declared) {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeUnverified, "stage-content",
			fmt.Errorf("the staged content does not match the file this job planned to send: the staged object states %d bytes with entity tag %s, the file is %d bytes with %s",
				staged.ContentLength, stagedTag, item.Size, declared))
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
		// The manifest body is empty, so it declares no digest of its own:
		// what binds this publication is the comparison above, over the
		// segments this manifest names (ADR 0080 decision 3).
		headers := publishHeaders(item, "")
		headers["X-Object-Manifest"] = staged.Manifest
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
		// The staged object's entity tag was just compared with the local
		// file, so declaring it here binds the publication to those same
		// bytes as they stream through (ADR 0080 decision 4).
		_, putErr := dst.PutObject(ctx, container, item.Name, body, staged.ContentType,
			publishHeaders(item, declared))
		closeErr = body.Close()
		if putErr != nil {
			result := uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "commit-copy", putErr)
			if swiftcontrol.IsPreconditionFailed(putErr) {
				result = uploadConflict("commit-conflict", putErr)
			} else if swiftcontrol.IsDeclaredDigestRefused(putErr) {
				// The store refused the body against the entity tag this
				// commit declared, which means the staged bytes changed
				// between the comparison and this read. That is a commit
				// it could not verify, not a transport failure, and the
				// native route already records it that way (ADR 0080
				// decision 7).
				result = uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeUnverified, "commit-copy",
					fmt.Errorf("the staged object changed between the comparison and the publication: the store refused it against the declared digest %s: %w", declared, putErr))
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
