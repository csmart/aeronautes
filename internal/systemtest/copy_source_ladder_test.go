package systemtest

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

// newSegmentedSourceFixture seeds the same two-name scope as the plain
// staged-content fixture, except that the subject is a dynamic large
// object in the source account. That is the case the earlier slice left
// open: the commit skipped its content comparison because the source was
// not plain, so the staged bytes reached the live name unchecked.
func newSegmentedSourceFixture(t *testing.T, h *Harness) stagedContentFixture {
	t.Helper()
	f := stagedContentFixture{
		src: h.Container(h.Primary, "source-ladder-src"), dst: h.Container(h.Secondary, "source-ladder-dst"),
		control: []byte("first control bytes"),
		subject: append(bytes.Repeat([]byte("A"), 1<<20), bytes.Repeat([]byte("B"), 1<<20)...),
	}
	f.segments = f.dst + "_segments"
	sourceSegments := f.src + "_segments"
	h.rawPut(h.Secondary, "/"+f.segments, nil, nil)
	h.rawPut(h.Primary, "/"+sourceSegments, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, f.segments) })
	t.Cleanup(func() { h.purge(h.Primary, sourceSegments) })
	h.Seed(h.Primary, f.src, "scope/a-control", f.control, map[string]string{"X-Object-Meta-Team": "content-proof"})
	h.Seed(h.Primary, sourceSegments, "b-subject/src/00000000", f.subject[:1<<20], nil)
	h.Seed(h.Primary, sourceSegments, "b-subject/src/00000001", f.subject[1<<20:], nil)
	h.Seed(h.Primary, f.src, "scope/b-subject", nil, map[string]string{
		"X-Object-Manifest": sourceSegments + "/b-subject/src/", "X-Object-Meta-Team": "content-proof"})
	h.Seed(h.Primary, f.src, "unselected", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, f.dst, "unselected", []byte("destination sentinel"), nil)
	h.Seed(h.Secondary, f.dst, "scope/.aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	// A dynamic large object manifest lists as a zero-byte row, so the
	// planned bytes come from the control alone.
	f.listedSubjectBytes = 0
	f.beforeA, f.beforeB = h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	return f
}

// assertSegmentedSourceLadder checks the source's own ladder before the
// job runs: the segment listing has to recombine to the entity tag the
// manifest HEAD reports, which is the protocol fact the whole comparison
// rests on. A provider that does not derive its aggregate that way must
// fail here rather than silently skip the content check.
func assertSegmentedSourceLadder(t *testing.T, h *Harness, f stagedContentFixture) {
	t.Helper()
	before := h.SnapshotNamespace(h.Primary)
	subject := before.Containers[f.src].Objects["scope/b-subject"]
	if subject.Class != "dlo" || subject.Size != int64(len(f.subject)) {
		t.Fatalf("subject is not a dynamic large object of the intended size: %+v", subject)
	}
	if subject.Digest != stagedContentDigest(f.subject) {
		t.Fatalf("subject does not assemble to the intended bytes: %+v", subject)
	}
	if len(subject.Segments) != 2 {
		t.Fatalf("subject does not list two source segments: %+v", subject.Segments)
	}
	t.Logf("source ladder: length=%d segments=%d", subject.Size, len(subject.Segments))
}

// sloManifestJSON describes the two owned segments as a static large
// object manifest.
func sloManifestJSON(container string, body []byte) string {
	left, right := md5.Sum(body[:1<<20]), md5.Sum(body[1<<20:])
	return fmt.Sprintf(`[{"path":"/%s/subject/00000000","etag":"%x","size_bytes":%d},{"path":"/%s/subject/00000001","etag":"%x","size_bytes":%d}]`,
		container, left, 1<<20, container, right, 1<<20)
}

func copySegmentedSourceLadder(t *testing.T, h *Harness, mode string) {
	t.Helper()
	f := newSegmentedSourceFixture(t, h)
	assertSegmentedSourceLadder(t, h, f)
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	sourceTrace := copyCompletionProxy(t, h, h.Primary, "")
	barrier, trace := stagedContentProxy(t, h, f.dst+"/scope/a-control", nil)
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: f.src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: f.dst, DestPrefix: "scope/",
		Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	stageName, stage, gate := stagedContentHeld(t, h, f, id, barrier)
	wantBody := alterStagedContent(t, h, f, mode, stage)
	altered := h.SnapshotNamespace(h.Secondary)
	changedStage := altered.Containers[f.dst].Objects[stageName]
	if changedStage.Class != "dlo" || changedStage.Manifest != stage.Manifest || changedStage.Size != int64(len(f.subject)) || changedStage.Digest != stagedContentDigest(wantBody) {
		t.Fatalf("controlled stage bytes not independently witnessed: %+v", changedStage)
	}
	var listedBytes int64
	for _, segment := range changedStage.Segments {
		listedBytes += segment.Size
	}
	if listedBytes != int64(len(f.subject)) {
		t.Fatalf("fixture would be refused by the old segment length check: %d", listedBytes)
	}
	if mode == "resegmented_control" && (len(changedStage.Segments) != 4 || reflect.DeepEqual(stage.Segments, changedStage.Segments)) {
		t.Fatal("matching control did not change segment structure")
	}
	if _, exists := altered.Containers[f.dst].Objects["scope/b-subject"]; exists {
		t.Fatal("subject became live while the first commit was held")
	}
	allowedAlterations := []string{f.dst + "/" + stageName}
	for _, segment := range append(append([]oracle.SegmentState(nil), stage.Segments...), changedStage.Segments...) {
		allowedAlterations = append(allowedAlterations, segment.Name)
	}
	h.AssertNoCollateral(gate, altered, allowedAlterations...)
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h)
	if err := barrier.Err(); err != nil {
		t.Fatal(err)
	}
	saved := copyCompletionRead(t, h.database, id)
	accepted := mode == "control" || mode == "resegmented_control"
	assertStagedContentResult(t, h, f, id, stageName, changedStage, accepted, jobs.CodeUnverified, saved, sourceTrace, trace)
	t.Logf("source ladder boundary: mode=%s bytes=%d staged_segments=%d state=%s copied=%d verified=%d failed=%d",
		mode, len(f.subject), len(changedStage.Segments), saved.Job.State,
		saved.Job.Counters.ObjectsCopied, saved.Job.Counters.ObjectsVerified, saved.Job.Counters.ObjectsFailed)
}

var segmentedSourceModes = []string{"control", "same_size_corruption", "permuted_segments", "missing_with_duplicate", "resegmented_control"}

func TestCopySegmentedSourceLadderMock(t *testing.T) {
	t.Parallel()
	for _, mode := range segmentedSourceModes {
		t.Run(mode, func(t *testing.T) { copySegmentedSourceLadder(t, findMockHarness(t), mode) })
	}
}

func TestCopySegmentedSourceLadderRealSwift(t *testing.T) {
	t.Parallel()
	for _, mode := range segmentedSourceModes {
		t.Run(mode, func(t *testing.T) { copySegmentedSourceLadder(t, New(t), mode) })
	}
}

// TestStaticLargeObjectSourceLadderRealSwift covers the source class a
// segment listing cannot answer for. A static large object reports the
// entity tag its manifest recorded, not one derived from the segments
// that are there now, so its manifest is its ladder: each entry states
// the length and MD5 the object was created from, and their
// recombination is that reported tag. A segment replaced afterwards with
// different bytes of the same length changes nothing the object reports
// about itself, and is exactly what this catches.
//
// Real Swift only: the in-process mock does not implement
// ?multipart-manifest=put and cannot seed one over the wire. The same
// two outcomes are held against the mock at the executor boundary by
// TestStaticLargeObjectSourceVerifiedAgainstItsManifest.
func TestStaticLargeObjectSourceLadderRealSwift(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"consistent", "segment_substituted"} {
		t.Run(mode, func(t *testing.T) { staticLargeObjectSourceLadder(t, New(t), mode) })
	}
}

func staticLargeObjectSourceLadder(t *testing.T, h *Harness, mode string) {
	t.Helper()
	src, dst := h.Container(h.Primary, "slo-source-src"), h.Container(h.Secondary, "slo-source-dst")
	sourceSegments := src + "_segments"
	destSegments := dst + "_segments"
	h.rawPut(h.Primary, "/"+sourceSegments, nil, nil)
	h.rawPut(h.Secondary, "/"+destSegments, nil, nil)
	t.Cleanup(func() { h.purge(h.Primary, sourceSegments) })
	t.Cleanup(func() { h.purge(h.Secondary, destSegments) })
	body := append(bytes.Repeat([]byte("A"), 1<<20), bytes.Repeat([]byte("B"), 1<<20)...)
	h.Seed(h.Primary, sourceSegments, "subject/00000000", body[:1<<20], nil)
	h.Seed(h.Primary, sourceSegments, "subject/00000001", body[1<<20:], nil)
	h.SeedSLO(h.Primary, src, "scope/subject", sloManifestJSON(sourceSegments, body))
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	subject := beforeA.Containers[src].Objects["scope/subject"]
	if subject.Class != "slo" || subject.Size != int64(len(body)) || subject.Digest != stagedContentDigest(body) {
		t.Fatalf("subject is not the intended static large object: %+v", subject)
	}
	if mode == "segment_substituted" {
		// Same length, different bytes, after the manifest recorded its
		// digests. The object's own entity tag and logical size are
		// unchanged, so nothing it reports about itself can show this.
		h.Seed(h.Primary, sourceSegments, "subject/00000000", bytes.Repeat([]byte("C"), 1<<20), nil)
		t.Cleanup(func() { h.Seed(h.Primary, sourceSegments, "subject/00000000", body[:1<<20], nil) })
	}
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/",
		Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h)
	saved := copyCompletionRead(t, h.database, id)
	if mode == "segment_substituted" {
		// Restore the recorded bytes before the oracle reads the source:
		// the store refuses to serve a manifest whose segment no longer
		// matches, so a corrupted source cannot be snapshotted.
		h.Seed(h.Primary, sourceSegments, "subject/00000000", body[:1<<20], nil)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeA, afterA)
	stageName := "scope/.aeronautes/stage/" + id + "/batch/subject"
	live, published := afterB.Containers[dst].Objects["scope/subject"]
	if mode == "consistent" {
		if saved.Job.State != "complete" || saved.Job.Counters.ObjectsCopied != 1 || saved.Job.Counters.ObjectsFailed != 0 {
			t.Fatalf("consistent static large object was not published: %+v", saved.Job)
		}
		if !published || live.Size != int64(len(body)) || live.Digest != stagedContentDigest(body) {
			t.Fatalf("published object does not assemble to the source bytes: %+v", live)
		}
		if _, retained := afterB.Containers[dst].Objects[stageName]; retained {
			t.Fatal("published static large object left its stage behind")
		}
		t.Logf("static large object ladder: mode=%s state=%s copied=%d", mode, saved.Job.State, saved.Job.Counters.ObjectsCopied)
		return
	}
	// The observed protocol fact: this store validates a static large
	// object's recorded segment digests when it serves the object, so a
	// same-size substitution makes the source unreadable rather than
	// silently wrong, and the copy never obtains bytes to publish. That
	// is the store's own protection, not this slice's, and it is recorded
	// here rather than claimed as a verification result.
	if saved.Job.State != "partial" || saved.Job.Counters.ObjectsCopied != 0 || saved.Job.Counters.ObjectsFailed != 1 {
		t.Fatalf("substituted static large object outcome differs: %+v", saved.Job)
	}
	if !strings.Contains(saved.Job.Error, "subject") {
		t.Fatalf("failure lost the named object: %q", saved.Job.Error)
	}
	if published {
		t.Fatalf("bytes that no longer match the recorded manifest reached a live name: %+v", live)
	}
	allowed := []string{dst + "/" + stageName}
	if retained, ok := afterB.Containers[dst].Objects[stageName]; ok {
		for _, segment := range retained.Segments {
			allowed = append(allowed, segment.Name)
		}
	}
	h.AssertNoCollateral(beforeB, afterB, allowed...)
	item := saved.Items[0]
	if item.Execution == nil || item.Execution.Result != jobs.ItemFailed {
		t.Fatalf("substituted static large object did not fail: %+v", item)
	}
	t.Logf("static large object ladder: mode=%s state=%s failed=%d stage=%v error=%q",
		mode, saved.Job.State, saved.Job.Counters.ObjectsFailed, item.Execution.Diagnostic, saved.Job.Error)
}
