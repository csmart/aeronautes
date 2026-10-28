package systemtest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// Maintained regressions for ADR 0044. A dynamic large object's segment
// deletion is authorised over its namespace boundary and over nothing
// else, and it refuses before removing anything when the preflight
// OBSERVES a name the manifest reads from outside that boundary.
//
// These replace the probe archived with the plan. The collateral case
// there — a forced, typed-confirmed deletion reported as proven that
// destroyed a sibling namespace, a prefix substring and a second live
// manifest's backing — is the refusal asserted here, reached through the
// same bridge-facing methods.
//
// The equivalent property on the operator-facing path is
// TestPrefixDeleteSparesEverythingElse. This is its segment-path twin,
// where the prefix comes from a header instead of the operator.

// seedSubject builds a subject manifest over "job1/" with the header
// spelled as the caller asks, and returns the two container names.
func seedSubject(t *testing.T, h *Harness, manifestSuffix string) (segments, aliases string) {
	t.Helper()
	segments = h.Container(h.Primary, "segs")
	aliases = h.Container(h.Primary, "alias")
	for i := 0; i < 2; i++ {
		h.Seed(h.Primary, segments, fmt.Sprintf("job1/%08d", i), []byte("subject bytes"), nil)
	}
	h.Seed(h.Primary, aliases, "subject.bin", nil,
		map[string]string{"X-Object-Manifest": segments + "/" + manifestSuffix})
	return segments, aliases
}

func subjectRequest(h *Harness, aliases string) app.SegmentsDeleteRequestDTO {
	return app.SegmentsDeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: aliases, Name: "subject.bin",
		Force: true, Confirm: "subject.bin",
	}
}

// An unslashed header is what the pinned engine writes. Its segments are
// deleted exactly, and the neighbours that merely share the prefix
// string are not touched, because the operation refuses instead.
func TestSegmentDeleteRefusesNamesOutsideTheNamespace(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	segments, aliases := seedSubject(t, h, "job1")

	// Neighbours the raw prefix reads but the namespace excludes. One is
	// another live manifest's backing.
	h.Seed(h.Primary, segments, "job10/00000000", []byte("sibling dlo bytes"), nil)
	h.Seed(h.Primary, segments, "job1-archive/00000000", []byte("sibling archive bytes"), nil)
	h.Seed(h.Primary, segments, "job1keep.bin", []byte("prefix substring, not a child"), nil)
	h.Seed(h.Primary, aliases, "sibling.bin", nil,
		map[string]string{"X-Object-Manifest": segments + "/job10/"})

	if err := h.Evidence.RecordOwnership(ctx, h.Primary.BookmarkID, "job-subject", segments, "job1/"); err != nil {
		t.Fatal(err)
	}
	req := subjectRequest(h, aliases)

	// The dialog states the boundary that would be swept, not the raw
	// header, so the operator is not shown a narrower set than the one
	// the operation would act on.
	owned, err := h.Delete.CheckSegmentOwnership(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if want := segments + "/job1/"; owned.Target != want {
		t.Errorf("dialog target = %q, want the namespace %q", owned.Target, want)
	}
	// The dialog's reviewed graph is carried back on the request (ADR 0095
	// decision 8); the outside-name refusal is made synchronously, before
	// any job row exists.
	req.Reviewed = owned.Reviewed

	before := h.SnapshotNamespace(h.Primary)
	rec := h.DeleteManifestWithSegments(ctx, req)
	if rec.Err() == nil {
		t.Fatal("a manifest reading three names outside its namespace was deleted; want a refusal")
	}
	for _, want := range []string{"outside its own namespace", segments + "/job1/"} {
		if !strings.Contains(rec.Err().Error(), want) {
			t.Errorf("refusal %q does not state %q", rec.Err(), want)
		}
	}
	// A refusal removes NOTHING, including the selected manifest.
	h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary))
}

// The engine's own shape still works: an unslashed header whose segments
// all sit under its namespace deletes exactly its own graph.
func TestSegmentDeleteAcceptsTheEnginesUnslashedHeader(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	segments, aliases := seedSubject(t, h, "job1")
	// A neighbour that shares no prefix at all must also survive.
	h.Seed(h.Primary, segments, "other/00000000", []byte("unrelated"), nil)

	req := subjectRequest(h, aliases)
	req.Reviewed = h.ReviewSegmentMembership(ctx, aliases, "subject.bin")
	before := h.SnapshotNamespace(h.Primary)
	rec := h.DeleteManifestWithSegments(ctx, req)
	if rec.Err() != nil {
		t.Fatalf("the engine's own manifest shape was refused: %v", rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
		t.Fatalf("the segment deletion did not complete: state %q, error %q", job.State, job.Error)
	}
	h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary),
		aliases+"/subject.bin",
		segments+"/job1/00000000",
		segments+"/job1/00000001",
	)
}

// A slash-terminated header reads nothing outside its namespace,
// because the raw prefix and the boundary are the same string. It
// deletes its own graph and spares a name that merely shares the stem —
// the property the operator-facing prefix delete already proves.
func TestSegmentDeleteWithASlashTerminatedHeaderSparesTheStem(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	segments, aliases := seedSubject(t, h, "job1/")
	h.Seed(h.Primary, segments, "job1keep.bin", []byte("prefix substring, not a child"), nil)

	req := subjectRequest(h, aliases)
	req.Reviewed = h.ReviewSegmentMembership(ctx, aliases, "subject.bin")
	before := h.SnapshotNamespace(h.Primary)
	rec := h.DeleteManifestWithSegments(ctx, req)
	if rec.Err() != nil {
		t.Fatalf("a manifest reading only its own namespace was refused: %v", rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
		t.Fatalf("the segment deletion did not complete: state %q, error %q", job.State, job.Error)
	}
	h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary),
		aliases+"/subject.bin",
		segments+"/job1/00000000",
		segments+"/job1/00000001",
	)
}
