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
	switch swiftcontrol.ObjectClass(preflight.LargeObjects) {
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
	stagePrefix := rclone.StageNamespace + stageID + "/batch/"
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
	// Engine-route items are staged a source directory at a time, each group
	// under its own relative directory of the stage prefix, so two files of
	// one name in different folders stage apart (ADR 0103 decision 4).
	groups := map[string]*stageGroup{}
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
		staging := ""
		if item.Route != "native" {
			staging = stageObject(stagePrefix, prefix, item)
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
			present, err := u.observePredecessor(ctx, dst, container, item)
			if err != nil {
				return report, err
			}
			file, err := os.Open(item.Path)
			var result uploadResult
			if err != nil {
				result = uploadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-open", err)
			} else {
				result = nativeUpload(ctx, dst, container, item, file, present, u.confirmUnchanged())
			}
			if err := u.settle(ctx, dst, item, result, progress); err != nil {
				return report, err
			}
			continue
		}
		dir, relDir := filepath.Dir(item.Path), relativeDir(prefix, item)
		key := dir + "\x00" + relDir
		group, exists := groups[key]
		if !exists {
			group = &stageGroup{dir: dir, relDir: relDir}
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
		result, err := u.copy(ctx, dst, engine, group.dir, dstSpec+"/"+strings.TrimSuffix(stagePrefix+group.relDir, "/"), names, capture)
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
			present, err := u.observePredecessor(ctx, dst, container, item)
			if err != nil {
				return report, err
			}
			plan := segmentPlan{class: swiftcontrol.ObjectClass(preflight.LargeObjects), chunkBytes: preflight.ChunkBytes,
				captured: report.CapturedPrefixes, check: u.staticCheck(rclone.StageNamespace + stageID + "/static-check")}
			outcome := commitStagedUpload(ctx, dst, container, stageObject(stagePrefix, prefix, item), item, present, u.confirmUnchanged(), recovery, plan)
			if err := u.settle(ctx, dst, item, outcome, progress); err != nil {
				return report, err
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

// stageGroup is one engine copy: the files of one source directory that
// share one relative directory under the stage prefix.
type stageGroup struct {
	dir, relDir string
	items       []UploadItem
}

// stageObject names an item's staging object: the job's stage prefix, the
// item's relative directory under the request's prefix, and its file name,
// which is what the engine writes when it copies the group there. Two
// files of one name in different folders therefore stage apart (ADR 0103
// decision 4); a file pick's relative directory is empty, which leaves its
// stage name as it has always been.
func stageObject(stagePrefix, prefix string, item UploadItem) string {
	return stagePrefix + relativeDir(prefix, item) + filepath.Base(item.Path)
}

// relativeDir is the directory part of an item's name under the request's
// prefix, slash-terminated, or empty at the top.
func relativeDir(prefix string, item UploadItem) string {
	if dir := path.Dir(strings.TrimPrefix(item.Name, prefix)); dir != "." && dir != "/" {
		return dir + "/"
	}
	return ""
}

// verifyUpload asks the live name what it holds, after the publication exists.
//
// class and tag are what this route published: a plain object and the file's own
// MD5, a static large object and the tag its segments' digests make, or nothing
// for a dynamic manifest, whose binding is the staged content comparison that ran
// before the commit. For a plain publication ADR 0045 requires the live HEAD to
// report a plain object whose length AND entity tag equal the local file's length
// and digest: a plain Swift object's entity tag IS the MD5 of its content, so that
// is the one question this pass can ask that distinguishes the file's bytes from
// other bytes of the same length.
//
// Length alone was all this checked before, which is what ADR 0045 named as the
// defect. A declared entity tag on the PUT makes the store refuse a body that
// does not hash to it, but a path between this process and the store can remove
// that header, and then nothing had asked. The store is the only party that can
// PREVENT such a publication; this is the party that can report it.
func verifyUpload(ctx context.Context, dst *swiftcontrol.Session, container string, item UploadItem,
	class swiftcontrol.ObjectClass, tag string) uploadResult {
	var live *swiftcontrol.ObjectDetail
	err := untilListed(ctx, func() (bool, error) {
		var err error
		if live, err = dst.ObjectDetail(ctx, container, item.Name); err != nil {
			return false, err
		}
		if live.ContentLength != item.Size {
			// A dynamic object's size is summed from a listing of its
			// segments, which can still lag the publication; a short one is
			// read again while it does (ADR 0111 decision 6).
			return class == swiftcontrol.ClassDLO && live.ContentLength < item.Size,
				fmt.Errorf("size %d != local %d", live.ContentLength, item.Size)
		}
		return false, nil
	})
	if err == nil && tag != "" {
		// The class is asked first and separately. An object of another class
		// reports another kind of entity tag, so comparing the two values would
		// refuse for the wrong reason and say the wrong thing about what is live.
		if live.Class != class {
			err = fmt.Errorf("live object is %s, this upload published %s", live.Class, class)
		} else if liveTag := strings.Trim(live.ETag, `"`); !strings.EqualFold(liveTag, tag) {
			err = fmt.Errorf("entity tag %s != local %s", liveTag, tag)
		}
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
//
// A dynamic manifest is assembled from a listing of its prefix, so a
// complete response that ends short may be a listing that has not caught
// up with the last segment yet. It is read again while that is so, within
// a bound (ADR 0111 decision 6); a response that is not complete, and any
// other disagreement, fails at once.
func matchesStagedContent(ctx context.Context, dst *swiftcontrol.Session, container, stagedName string,
	ladder transfercopy.Ladder) (code transfercopy.Code, err error) {
	err = untilListed(ctx, func() (bool, error) {
		body, err := dst.OpenObjectContent(ctx, container, stagedName)
		if err != nil {
			code = transfercopy.CodeIncomplete
			return false, fmt.Errorf("open complete staged content: %w", err)
		}
		code, err = transfercopy.CompareContent(ctx, body, ladder)
		return code == transfercopy.CodeIncomplete, err
	})
	return code, err
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
func localDigest(file string) (string, error) {
	opened, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer opened.Close()
	sum := md5.New()
	if _, err := io.Copy(sum, opened); err != nil {
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
//
// It reports whether a predecessor was there, because the publication has to
// agree with what was recorded. A run that recorded an absent predecessor
// published nothing over anything, so its request must be a create, and
// ADR 0085 says so: a name recreated after this read would otherwise be
// overwritten with no conditional and no acknowledgement of THAT object,
// while the ledger said there had been nothing to replace.
func (u *uploadRun) observePredecessor(ctx context.Context, dst *swiftcontrol.Session,
	container string, item UploadItem) (present bool, err error) {
	if !item.Replaces {
		return false, nil
	}
	detail, err := dst.ObjectDetail(ctx, container, item.Name)
	switch {
	case swiftcontrol.IsNotFound(err):
		return false, u.replacing(item, ObservedPredecessor{})
	case err != nil:
		return false, u.stop(fmt.Errorf("reading what is at %s before replacing it: %w", item.Name, err))
	}
	return true, u.replacing(item, ObservedPredecessor{
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
			return FragmentInventory{Unread: "the static manifest could not be read whole within the bounds this build sets"}
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
		segmentContainer, prefix, ok := swiftcontrol.DynamicSegments(detail.Manifest)
		if !ok {
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
		inventory := FragmentInventory{Prefix: segmentContainer + "/" + prefix}
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
// replacing is whether THIS publication is going over a predecessor the run
// observed, which is not the same question as item.Replaces — that one says
// the preflight found the name live and the operator acknowledged it. When
// the two disagree the name went away in between, the run recorded that it
// did, and the publication is a create (ADR 0085).
//
// This is the one place in the protocol where the refusal does not depend on
// when we looked. Every other precondition this store was measured against is
// ignored, but If-None-Match: * is honoured, and the STORE is what evaluates
// it — so a name present at that evaluation is refused there rather than
// detected here, however old our own observation is.
//
// That is not a claim that the window is closed, and an earlier version of this
// comment said it was. The evaluation point has since been measured
// (docs/experiments/2026-10-10-conditional-evaluation-point.md): a name created
// while the body was still arriving was never refused, twelve observations of
// twelve, so the conditional is evaluated before the body completes and a create
// landing after it is not covered — on the native route, which streams the whole
// local file, that interval is the length of the upload. What this transition
// removes is the dependence on our observation's age, not the exposure after the
// store has decided.
//
// The same measurement found something worth knowing here: in eleven of those
// twelve the conditional creator was answered 201 while the OTHER writer's object
// remained at the name. A 201 is not evidence that these bytes are at this name,
// which is why verifyUpload asks the live name afterwards instead of trusting the
// status.
//
// The declared entity tag is kept either way, wherever the route declares
// one, so a replacement whose bytes did not survive the wire is refused by
// the store with the prior object intact, as measured.
func publishHeaders(replacing bool, declared string) map[string]string {
	headers := map[string]string{}
	if declared != "" {
		headers["ETag"] = declared
	}
	if !replacing {
		headers["If-None-Match"] = "*"
	}
	return headers
}

func nativeUpload(ctx context.Context, dst *swiftcontrol.Session, container string, item UploadItem,
	file localSource, replacing bool, confirm unchangedSince) (result uploadResult) {
	defer func() { result.addIssue(transfercopy.StageSourceRead, "native-close", file.Close()) }()
	declared, err := heldDigest(file)
	if err != nil {
		return uploadFailed(transfercopy.EffectNotAttempted, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-digest", err)
	}
	// The fixed point: everything this route prepares is done, and the next
	// thing that happens is the publishing request.
	if err := confirmBefore(ctx, confirm, dst, container, item, replacing); err != nil {
		return uploadRefusedBefore("native-reobserve", err)
	}
	headers := publishHeaders(replacing, declared)
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
	// The digest this route declared is also what the live name must report:
	// this publication is a plain object and nothing else was sent.
	return verifyUpload(ctx, dst, container, item, swiftcontrol.ClassPlain, declared)
}

func commitStagedUpload(ctx context.Context, dst *swiftcontrol.Session, container, stagedName string,
	item UploadItem, replacing bool, confirm unchangedSince, recovery func(kind, container, prefix, object string) error, plan segmentPlan) uploadResult {
	staged, err := dst.ObjectDetail(ctx, container, stagedName)
	if err != nil {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeMissing, "stage-verify", fmt.Errorf("staged object missing: %w", err))
	}
	if _, err := dst.ObjectDetail(ctx, container, item.Name); err == nil {
		// A name that is live is a conflict for a create and the expected
		// state for a replacement the operator acknowledged. A run whose
		// predecessor was recorded absent is publishing a create, so this
		// refuses it here rather than reading and streaming a whole staged
		// object the conditional below would have had the store refuse.
		if !replacing {
			return uploadConflict("commit-conflict", errors.New("destination appeared since preflight"))
		}
	} else if !swiftcontrol.IsNotFound(err) {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeUnknown, "commit-rehead", err)
	}
	var checkCleanup error
	if staged.Class == swiftcontrol.ClassDLO && plan.class == swiftcontrol.ClassSLO {
		result, published, cleanup := commitStatic(ctx, dst, container, stagedName, staged, item, replacing, confirm, recovery, plan)
		if published {
			return result
		}
		// The store does not return static manifests, so what the engine
		// staged is published as it was staged (ADR 0111 decision 10).
		checkCleanup = cleanup
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
	// The fixed point for this route is per publication shape, because the two
	// shapes do not prepare the same things. A dynamic manifest's publication
	// sends an empty body and its last preparation is the comparison of the
	// assembled content above, so the comparison comes next and ADR 0042's
	// ordering then puts the committing fact between it and the request — a
	// fact recorded for a publication that is then refused is a fact about
	// nothing. A plain or static materialisation still has to OPEN the staged
	// object, which is a request of its own and is preparation rather than the
	// publication, so its comparison comes after that open and immediately
	// before the PUT. A review found the comparison in front of that open, which
	// left a writer arriving during it to be overwritten.
	var recordErr, closeErr error
	if staged.Class == swiftcontrol.ClassDLO {
		if err := confirmBefore(ctx, confirm, dst, container, item, replacing); err != nil {
			return uploadRefusedBefore("commit-reobserve", err)
		}
		segmentContainer, segmentPrefix, hasIntent := swiftcontrol.DynamicSegments(staged.Manifest)
		hasIntent = hasIntent && recovery != nil
		intent := rclone.SegmentPrefix{Container: segmentContainer, Prefix: segmentPrefix}.Canonical()
		if hasIntent {
			if err := recovery("committing", intent.Container, intent.Prefix, container+"/"+item.Name); err != nil {
				return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageRecording, transfercopy.CodeIO, "commit-intent", err)
			}
		}
		// The manifest body is empty, so it declares no digest of its own:
		// what binds this publication is the comparison above, over the
		// segments this manifest names (ADR 0080 decision 3).
		headers := publishHeaders(replacing, "")
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
		// The fixed point for this shape: the reader is open and the next thing
		// is the publishing request itself.
		if err := confirmBefore(ctx, confirm, dst, container, item, replacing); err != nil {
			refused := uploadRefusedBefore("commit-reobserve", err)
			refused.addIssue(transfercopy.StageStaging, "staged-close", body.Close())
			return refused
		}
		// The staged object's entity tag was just compared with the local
		// file, so declaring it here binds the publication to those same
		// bytes as they stream through (ADR 0080 decision 4).
		_, putErr := dst.PutObject(ctx, container, item.Name, body, staged.ContentType,
			publishHeaders(replacing, declared))
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
	// A plain commit published the staged bytes themselves, which were compared
	// with the file before being read, so the live name must report the file's
	// own digest. A manifest commit published an empty body whose live entity tag
	// is an aggregate over segments, and what binds it is the staged content
	// comparison above, which ADR 0042's ordering already put before the
	// committing fact and the manifest PUT.
	class, tag := swiftcontrol.ClassPlain, declared
	if staged.Class == swiftcontrol.ClassDLO {
		class, tag = swiftcontrol.ClassDLO, ""
	}
	result := verifyUpload(ctx, dst, container, item, class, tag)
	result.addIssue(transfercopy.StageRecording, "commit-record", recordErr)
	result.addIssue(transfercopy.StageCleanup, "static-check-cleanup", checkCleanup)
	result.addIssue(transfercopy.StageStaging, "staged-close", closeErr)
	if result.Result == transfercopy.ResultCopied && recordErr == nil {
		if err := dst.DeleteObject(ctx, container, stagedName); err != nil && !swiftcontrol.IsNotFound(err) {
			result.addIssue(transfercopy.StageCleanup, "staging-cleanup", fmt.Errorf("delete staging object %q: %w", stagedName, err))
		}
	}
	return result
}

// unchangedSince is the fixed-point comparison ADR 0085 requires for an
// acknowledged overwrite: a fresh observation taken after all preparation and
// immediately before the publishing request, compared with what the operator was
// shown when they accepted replacing the name.
//
// It is a fresh HEAD and not either stored record. observePredecessor runs
// before the preparation that decision refuses to leave inside the window — the
// native route hashes the whole local file after it, and the staged route reads
// an entire assembled stage — so the acknowledged baseline and that record can
// agree while the replacement lands between them. Comparing the two stored rows
// would therefore pass exactly the case this exists to fail.
//
// What it costs is stated in the decision: one HEAD per replaced name on the
// native route, whose only execution-time destination read is
// observePredecessor's, and that one cannot be moved because its result feeds
// the fragment inventory and the durable predecessor record.
//
// What it does NOT do is close the window. The measurement in
// docs/experiments/2026-10-10-conditional-evaluation-point.md establishes that
// this store evaluates a create conditional before a body completes and that a
// 201 is not evidence the sent bytes are at the name, so what remains after this
// comparison is the publishing request's own transport and application. This
// narrows; the route's verification afterwards is what reports.
type unchangedSince func(ctx context.Context, dst *swiftcontrol.Session, container string, item UploadItem) error

// confirmUnchanged builds that comparison for one run, or a comparison that
// refuses everything when no hook can answer what was acknowledged.
func (u *uploadRun) confirmUnchanged() unchangedSince {
	return func(ctx context.Context, dst *swiftcontrol.Session, container string, item UploadItem) error {
		if !item.Replaces {
			// Nothing was acknowledged for this name and nothing is being
			// replaced; the create's own conditional is what protects it.
			return nil
		}
		if u.recorder == nil || u.recorder.Acknowledged == nil {
			return fmt.Errorf("replacing %s needs the identity its acknowledgement was shown, and this run has no way to ask for it", item.Name)
		}
		shown, identified, err := u.recorder.Acknowledged(item)
		if err != nil {
			return fmt.Errorf("reading what %s was acknowledged as: %w", item.Name, err)
		}
		if !identified {
			return fmt.Errorf("replacing %s was acknowledged without identifying what was there, so this publication has nothing to compare against", item.Name)
		}
		// The container's versioning is read again here, BEFORE the object
		// comparison, so the window ADR 0085 keeps between that read and the
		// request is not widened by it (ADR 0102 decision 2).
		if u.recorder.VersioningUnchanged == nil {
			return fmt.Errorf("replacing %s needs the destination's versioning read again before publishing, and this run has no way to ask for it", item.Name)
		}
		refusal, err := u.recorder.VersioningUnchanged(ctx, item)
		if err != nil {
			// Refused either way; the run stops because the refusal left no
			// record, which is the loss the identity refusal below also
			// refuses to let pass in silence. This comes before the cancel
			// below: a record that failed is lost whether or not the job was
			// also cancelled.
			refused := fmt.Errorf("replacing %s was refused on its versioning and the refusal could not be recorded: %w", item.Name, err)
			u.stop(refused)
			return refused
		}
		if cancelled := ctx.Err(); cancelled != nil {
			// A cancel that lands in the read is the job stopping, not a
			// finding about the container, and is coded as the cancelled
			// object read below always was.
			return fmt.Errorf("reading the versioning of %s's container before replacing it: %w", item.Name, cancelled)
		}
		if refusal != "" {
			return fmt.Errorf("replacing %s: %s", item.Name, refusal)
		}
		detail, err := dst.ObjectDetail(ctx, container, item.Name)
		switch {
		case swiftcontrol.IsNotFound(err):
			// The name went away during preparation. This publication was
			// planned as a replacement, so it carries no create conditional
			// and would overwrite whatever has appeared there since. Refuse
			// rather than publish blind: restoring the conditional at this
			// point is a change to ADR 0085's own conditional-create
			// decision, which reads the earlier observation, and is not this
			// comparison's to make.
			return fmt.Errorf("%s no longer holds anything, so the object acknowledged for replacement is gone", item.Name)
		case err != nil:
			return fmt.Errorf("reading %s before replacing it: %w", item.Name, err)
		}
		// The fresh read is held to the same completeness rule as the capture.
		// Without it a HEAD stating no length arrives as a zero that compares
		// equal to a genuine empty object's baseline, and the replacement
		// proceeds against an identity nobody established.
		found, identifiable := shownFrom(detail)
		if !identifiable {
			return fmt.Errorf("%s could not be identified before replacing it: its HEAD stated no entity tag or no length", item.Name)
		}
		if found != shown {
			refusal := fmt.Errorf("%s no longer holds what was acknowledged: it was a %s of %d bytes with entity tag %s, and it is now a %s of %d bytes with entity tag %s",
				item.Name, shown.Class, shown.Length, shown.ETag, found.Class, found.Length, found.ETag)
			// Both identities go somewhere durable, per name. The error above
			// reaches a bounded in-memory list of which only the first entry's
			// message is saved, so it is the report and not the record.
			if u.recorder != nil && u.recorder.Refused != nil {
				if err := u.recorder.Refused(item, shown, found); err != nil {
					// The refusal stands either way; what must not be lost is
					// that it went unrecorded. Joining it into this item's own
					// error is not enough: only the first failure's message
					// reaches the job record, so an ordinary conflict on an
					// earlier name would hide it. Stopping the run is how this
					// package makes such a loss visible, and it is what
					// internal/audit asks of a caller.
					u.stop(fmt.Errorf("replacing %s was refused and the refusal could not be recorded: %w", item.Name, err))
				}
			}
			return refusal
		}
		return nil
	}
}

// confirmBefore applies the fixed-point comparison, and treats an absent one the
// way a missing Replacing hook is treated: a replacement with no comparison is
// refused rather than published uncompared, while a create needs none because
// its own conditional protects it.
func confirmBefore(ctx context.Context, confirm unchangedSince, dst *swiftcontrol.Session, container string,
	item UploadItem, replacing bool) error {
	// The EFFECTIVE publication mode, not the preflight's flag. When
	// observePredecessor found the name already absent, replacing is false and
	// publishHeaders restores If-None-Match: * — ADR 0085 keeps that create
	// transition expressly, so comparing here would refuse a publication that
	// decision allows, and the store's conditional is what protects it.
	if !replacing {
		return nil
	}
	if confirm == nil {
		return fmt.Errorf("replacing %s needs the comparison ADR 0085 makes immediately before publishing, and this publication was given none", item.Name)
	}
	return confirm(ctx, dst, container, item)
}
