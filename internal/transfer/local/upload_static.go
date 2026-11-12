package local

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// segmentPlan is what a staged dynamic manifest is published as
// (ADR 0111). The zero plan publishes it as it was staged, which is what
// a caller that never chose a kind has always had.
type segmentPlan struct {
	class      swiftcontrol.ObjectClass
	chunkBytes int64
	// captured are the segment prefixes this job recorded. A static
	// manifest names only segments under one of them, because that is what
	// lets a later deletion prove the segments are this job's (ADR 0005).
	captured []rclone.SegmentPrefix
	// check learns whether the store returns static manifests before the
	// run's first static publication (ADR 0111 decision 9); nil checks
	// nothing, for a caller that publishes a staged item directly.
	check *staticCheck
}

// staticCheck is one run's way to learn, once, whether the store returns
// static manifests, at a check name of its own under its staging prefix.
type staticCheck struct {
	run  *uploadRun
	name string
}

func (u *uploadRun) staticCheck(name string) *staticCheck {
	return &staticCheck{run: u, name: name}
}

// learn keeps the answer for the rest of the run and hands it to the
// recorder, whose failure stops the run before anything is published.
func (c *staticCheck) learn(returned bool) error {
	c.run.static = staticManifests{known: true, returned: returned}
	if c.run.recorder == nil || c.run.recorder.StaticManifests == nil {
		return nil
	}
	if err := c.run.recorder.StaticManifests(returned); err != nil {
		return c.run.stop(fmt.Errorf("recording whether the store returns static manifests: %w", err))
	}
	return nil
}

// checkStaticManifest writes the paired manifest at the check name with the
// create conditional and no content tag, reads it back with the strict
// bounded reader, and deletes it, the manifest alone, so the segments it
// names stay (ADR 0111 decisions 9 and 11). The store returns static
// manifests when the read lists exactly the entries written, and does not
// when it answers with an empty body; anything else is an error, which
// leaves the question open. A failed delete is reported apart: the check
// manifest is then staging residue, retained like any other.
func checkStaticManifest(ctx context.Context, dst *swiftcontrol.Session, container, name string, segments []swiftcontrol.StaticSegment) (returned bool, cleanup, err error) {
	if _, err := dst.PutStaticManifest(ctx, container, name, segments, "application/octet-stream", publishHeaders(false, "")); err != nil {
		return false, nil, fmt.Errorf("write the static manifest check: %w", err)
	}
	entries, readErr := dst.SLOManifestEntries(ctx, container, name, MaxManifestEntries, MaxManifestBytes)
	if err := dst.DeleteObject(ctx, container, name); err != nil && !swiftcontrol.IsNotFound(err) {
		cleanup = fmt.Errorf("delete the static manifest check %q: %w", name, err)
	}
	switch {
	case errors.Is(readErr, swiftcontrol.ErrManifestNotReturned):
		return false, cleanup, nil
	case readErr != nil:
		return false, cleanup, fmt.Errorf("read the static manifest check back: %w", readErr)
	case !namesExactly(entries, segments):
		return false, cleanup, errors.New("the static manifest check read back other entries than were written")
	}
	return true, cleanup, nil
}

// namesExactly reports whether a manifest read back lists exactly these
// whole segments, in order, with their tags and sizes.
func namesExactly(entries []swiftcontrol.SLOEntry, segments []swiftcontrol.StaticSegment) bool {
	if len(entries) != len(segments) {
		return false
	}
	for i, entry := range entries {
		got, ok := entry.(swiftcontrol.ObjectSegment)
		want := segments[i]
		if !ok || got.Range != nil || got.NestedSLO || got.ETag == nil ||
			got.Path != (swiftcontrol.ObjectRef{Container: want.Container, Object: want.Name}) ||
			!strings.EqualFold(strings.Trim(*got.ETag, `"`), want.ETag) || got.Size != uint64(want.Bytes) {
			return false
		}
	}
	return true
}

// chunk is one piece of the local file as the engine split it.
type chunk struct {
	size int64
	md5  string
}

// fileChunks reads the file once and returns each chunk's size and MD5,
// split by the chunk size the engine was given, the last chunk short when
// the file is not a multiple of it (ADR 0111 decision 3). A file whose
// size is no longer the planned size is refused: the segments were made
// from what the engine read, and this is the expectation they answer.
func fileChunks(path string, size, chunkBytes int64) ([]chunk, error) {
	if chunkBytes <= 0 {
		return nil, errors.New("no chunk size to pair the segments by")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var chunks []chunk
	var total int64
	for {
		sum := md5.New()
		n, err := io.CopyN(sum, file, chunkBytes)
		if n > 0 {
			chunks = append(chunks, chunk{size: n, md5: hex.EncodeToString(sum.Sum(nil))})
			total += n
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if total != size {
		return nil, fmt.Errorf("the file is %d bytes now and was %d bytes when the upload was planned", total, size)
	}
	return chunks, nil
}

// staticTag is the entity tag a static large object of these chunks
// reports: the MD5 of its segments' hexadecimal entity tags, concatenated
// in order. It is computed from the local chunks, so a live object that
// reports it was assembled from segments with the file's own digests.
func staticTag(chunks []chunk) string {
	sum := md5.New()
	for _, c := range chunks {
		io.WriteString(sum, c.md5)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// stagedSegments is the segment container and prefix a staged dynamic
// manifest names, decoded as Swift decodes the engine's header.
func stagedSegments(manifest string) (rclone.SegmentPrefix, error) {
	container, prefix, ok := swiftcontrol.DynamicSegments(manifest)
	if !ok || prefix == "" {
		return rclone.SegmentPrefix{}, fmt.Errorf("the staged manifest %q names no segment prefix", manifest)
	}
	return rclone.SegmentPrefix{Container: container, Prefix: prefix}.Canonical(), nil
}

// pairSegments lists the job's segment prefix and pairs segment i, named
// by the engine's zero-padded index, with chunk i of the local file by
// size and MD5 (ADR 0111 decision 3). A missing index is waited for,
// because the listing can lag the upload. A name that is not one of the
// expected indexes, or a segment whose size or tag differs from its chunk,
// fails at once: waiting does not change what was written.
func pairSegments(ctx context.Context, dst *swiftcontrol.Session, prefix rclone.SegmentPrefix, chunks []chunk) ([]swiftcontrol.StaticSegment, transfercopy.Code, error) {
	var segments []swiftcontrol.StaticSegment
	code := transfercopy.Code("")
	err := untilListed(ctx, func() (bool, error) {
		listed, missing, listCode, err := listSegments(ctx, dst, prefix, chunks)
		if err != nil {
			code = listCode
			return false, err
		}
		if len(missing) > 0 {
			code = transfercopy.CodeIncomplete
			return true, fmt.Errorf("%d of the %d segments are not listed under %s/%s, the first being segment %d",
				len(missing), len(chunks), prefix.Container, prefix.Prefix, missing[0])
		}
		segments = listed
		return false, nil
	})
	if err != nil {
		return nil, code, err
	}
	return segments, "", nil
}

func listSegments(ctx context.Context, dst *swiftcontrol.Session, prefix rclone.SegmentPrefix, chunks []chunk) (
	segments []swiftcontrol.StaticSegment, missing []int, code transfercopy.Code, err error) {
	segments = make([]swiftcontrol.StaticSegment, len(chunks))
	for entry, err := range dst.Objects(ctx, prefix.Container, swiftcontrol.ObjectListOptions{Prefix: prefix.Prefix, Deep: true}) {
		if err != nil {
			return nil, nil, transfercopy.CodeIO, fmt.Errorf("list the upload's segments: %w", err)
		}
		object, ok := entry.(swiftcontrol.ObjectEntry)
		if !ok {
			continue
		}
		index, ok := segmentIndex(strings.TrimPrefix(object.Name, prefix.Prefix))
		if !ok || index >= len(chunks) {
			return nil, nil, transfercopy.CodeUnverified, fmt.Errorf("%s/%s is under the upload's segment prefix and is not one of its %d segments",
				prefix.Container, object.Name, len(chunks))
		}
		want := chunks[index]
		if tag := strings.Trim(object.ETag, `"`); object.Bytes != want.size || !strings.EqualFold(tag, want.md5) {
			return nil, nil, transfercopy.CodeUnverified, fmt.Errorf("segment %d is listed as %d bytes with entity tag %s, and that chunk of the file is %d bytes with %s",
				index, object.Bytes, tag, want.size, want.md5)
		}
		segments[index] = swiftcontrol.StaticSegment{Container: prefix.Container, Name: object.Name, ETag: want.md5, Bytes: want.size}
	}
	for i, segment := range segments {
		if segment.Name == "" {
			missing = append(missing, i)
		}
	}
	return segments, missing, "", nil
}

// segmentIndex reads the engine's segment name: the index in decimal,
// zero-padded to eight digits, and nothing else.
func segmentIndex(name string) (int, bool) {
	index, err := strconv.Atoi(name)
	if err != nil || index < 0 || fmt.Sprintf("%08d", index) != name {
		return 0, false
	}
	return index, true
}

// commitStatic publishes a staged dynamic upload as a static large object
// naming the same segments (ADR 0111 decisions 3 and 4). The pairing is
// the binding: the store refuses a manifest whose named segment does not
// have the size and tag it states, so a manifest built from paired
// segments can only be accepted over the file's own chunks. published is
// false, with nothing published, when the store does not return static
// manifests, and the caller publishes what was staged (decision 10); cleanup
// is then a check manifest that could not be deleted, for that result.
func commitStatic(ctx context.Context, dst *swiftcontrol.Session, container, stagedName string, staged *swiftcontrol.ObjectDetail,
	item UploadItem, replacing bool, confirm unchangedSince, recovery func(kind, container, prefix, object string) error, plan segmentPlan) (result uploadResult, published bool, cleanup error) {
	if plan.check != nil && plan.check.run.static.known && !plan.check.run.static.returned {
		return uploadResult{}, false, nil
	}
	prefix, err := stagedSegments(staged.Manifest)
	if err != nil {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeUnverified, "stage-segments", err), true, nil
	}
	if !slices.Contains(plan.captured, prefix) {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeUnverified, "stage-segments",
			fmt.Errorf("the staged manifest names segments under %s/%s, which this job did not record", prefix.Container, prefix.Prefix)), true, nil
	}
	chunks, err := fileChunks(item.Path, item.Size, plan.chunkBytes)
	if err != nil {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeIncomplete, "stage-expectation", err), true, nil
	}
	segments, code, err := pairSegments(ctx, dst, prefix, chunks)
	if err != nil {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, code, "stage-segments",
			fmt.Errorf("the staged segments do not match the file this job planned to send: %w", err)), true, nil
	}
	if plan.check != nil && !plan.check.run.static.known {
		returned, checkCleanup, err := checkStaticManifest(ctx, dst, container, plan.check.name, segments)
		cleanup = checkCleanup
		if err == nil {
			err = plan.check.learn(returned)
		}
		if err != nil {
			failed := uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageStaging, transfercopy.CodeUnverified, "static-check", err)
			failed.addIssue(transfercopy.StageCleanup, "static-check-cleanup", cleanup)
			return failed, true, nil
		}
		if !returned {
			return uploadResult{}, false, cleanup
		}
	}
	// The fixed point: the listing wait and the check were preparation, and
	// the next request is the publication (ADR 0085).
	if err := confirmBefore(ctx, confirm, dst, container, item, replacing); err != nil {
		refused := uploadRefusedBefore("commit-reobserve", err)
		refused.addIssue(transfercopy.StageCleanup, "static-check-cleanup", cleanup)
		return refused, true, nil
	}
	if recovery != nil {
		if err := recovery("committing", prefix.Container, prefix.Prefix, container+"/"+item.Name); err != nil {
			return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageRecording, transfercopy.CodeIO, "commit-intent", err), true, nil
		}
	}
	// No content tag is declared: the file's MD5 would bind the manifest's
	// JSON body, not the segments it names (ADR 0111 decision 4).
	if _, err := dst.PutStaticManifest(ctx, container, item.Name, segments, staged.ContentType, publishHeaders(replacing, "")); err != nil {
		if swiftcontrol.IsPreconditionFailed(err) {
			return uploadConflict("commit-conflict", err), true, nil
		}
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "commit-manifest", err), true, nil
	}
	var recordErr error
	if recovery != nil {
		recordErr = recovery("committed", prefix.Container, prefix.Prefix, "")
	}
	result = verifyUpload(ctx, dst, container, item, swiftcontrol.ClassSLO, staticTag(chunks))
	result.addIssue(transfercopy.StageRecording, "commit-record", recordErr)
	result.addIssue(transfercopy.StageCleanup, "static-check-cleanup", cleanup)
	if result.Result == transfercopy.ResultCopied && recordErr == nil {
		// The staged dynamic manifest is staging now; deleting it removes
		// the manifest alone and leaves the segments the live object names.
		if err := dst.DeleteObject(ctx, container, stagedName); err != nil && !swiftcontrol.IsNotFound(err) {
			result.addIssue(transfercopy.StageCleanup, "staging-cleanup", fmt.Errorf("delete staging object %q: %w", stagedName, err))
		}
	}
	return result, true, nil
}
