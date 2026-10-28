package systemtest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// These producers credit the deletion jobs' scenario cells through the public
// service on the mock (ADR 0095 decision 9): the bulk deletion and the
// manifest-with-segments deletion are jobs now, requiring happy, refusal,
// conflict, cancel and partial. The happy bulk deletion is credited by
// TestPrefixDeleteSparesEverythingElse and the refusals by the refusal suite;
// these cover the rest.

// A bulk prefix deletion with a link under the prefix deletes the plain member
// and refuses the link, so the job is partial.
func TestBulkDeletePartialMock(t *testing.T) {
	h := findMockHarness(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "bulk-partial")
	h.Seed(h.Primary, c, "scope/keep-target", []byte("target"), textType)
	h.Seed(h.Primary, c, "scope/plain.txt", []byte("doomed"), textType)
	h.Seed(h.Primary, c, "scope/link", nil, map[string]string{"X-Symlink-Target": c + "/scope/keep-target"})
	rec := h.StartDelete(ctx, app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Prefix: "scope/", Mode: app.ModePrefix, Confirm: c,
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	job := h.AwaitJob(rec.JobID())
	if job.State != "partial" {
		t.Fatalf("job = %s: %s", job.State, job.Error)
	}
	rec.CreditPartial()
}

// A member replaced after it was listed, before its comparison HEAD, is
// refused as changed since listed, so the job records a conflicting member.
func TestBulkDeleteConflictMock(t *testing.T) {
	h := findMockHarness(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "bulk-conflict")
	h.Seed(h.Primary, c, "scope/racy.txt", []byte("first"), textType)
	replaced := false
	h.MockPrimary.BeforeRequest = func(method, path string) {
		if method == "HEAD" && strings.HasSuffix(path, "/"+c+"/scope/racy.txt") && !replaced {
			replaced = true
			h.Seed(h.Primary, c, "scope/racy.txt", []byte("second, and different"), textType)
		}
	}
	defer func() { h.MockPrimary.BeforeRequest = nil }()
	rec := h.StartDelete(ctx, app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Prefix: "scope/", Mode: app.ModePrefix, Confirm: c,
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	h.AwaitJob(rec.JobID())
	rec.CreditJobConflict()
}

// A cancellation landing at the only member's baseline HEAD, before any
// DELETE, ends the bulk deletion cancelled with nothing deleted.
func TestBulkDeleteCancelMock(t *testing.T) {
	h := findMockHarness(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "bulk-cancel")
	h.Seed(h.Primary, c, "keep-target", []byte("target"), textType)
	h.Seed(h.Primary, c, "scope/link", nil, map[string]string{"X-Symlink-Target": c + "/keep-target"})
	ready := make(chan string, 1)
	done := false
	h.MockPrimary.AfterRequest = func(method, path string) {
		if method == "HEAD" && strings.HasSuffix(path, "/"+c+"/scope/link") && !done {
			done = true
			if err := h.Transfer.CancelJob(ctx, <-ready); err != nil {
				t.Error(err)
			}
		}
	}
	defer func() { h.MockPrimary.AfterRequest = nil }()
	rec := h.StartDelete(ctx, app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Prefix: "scope/", Mode: app.ModePrefix, Confirm: c,
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	ready <- rec.JobID()
	job := h.AwaitJob(rec.JobID())
	if job.State != "cancelled" {
		t.Fatalf("job = %s: %s", job.State, job.Error)
	}
	rec.CreditCancelled()
}

// A dynamic manifest with its namespace, deleted with the Backstage force,
// completes as one job.
func TestSegmentJobHappyMock(t *testing.T) {
	h := findMockHarness(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "seg-happy")
	segs := h.Container(h.Primary, "seg-happy-parts")
	for i := range 3 {
		h.Seed(h.Primary, segs, fmt.Sprintf("owned/pfx/00000%d", i), []byte("segment"), textType)
	}
	h.Seed(h.Primary, c, "ours.bin", nil, map[string]string{"X-Object-Manifest": segs + "/owned/pfx/"})
	reviewed := h.ReviewSegmentMembership(ctx, c, "ours.bin")
	rec := h.DeleteManifestWithSegments(ctx, app.SegmentsDeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Name: "ours.bin", Force: true, Confirm: "ours.bin", Reviewed: reviewed,
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
		t.Fatalf("job = %s: %s", job.State, job.Error)
	}
	rec.CreditHappy()
}

// A static manifest whose graph is replaced between the review and the
// execution is refused as changed since reviewed, so the job records a
// conflicting member and nothing is deleted.
func TestSegmentJobConflictMock(t *testing.T) {
	h := findMockHarness(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "seg-conflict")
	segs := h.Container(h.Primary, "seg-conflict-parts")
	h.Seed(h.Primary, segs, "a/000001", []byte("0123456789"), textType)
	h.Seed(h.Primary, segs, "a/000002", []byte("abcdefghij"), textType)
	entry := func(name string, body []byte) string {
		return fmt.Sprintf(`{"path":"/%s/%s","etag":"%s","size_bytes":%d}`, segs, name, md5Hex(body), len(body))
	}
	h.SeedSLO(h.Primary, c, "big.slo", "["+entry("a/000001", []byte("0123456789"))+","+entry("a/000002", []byte("abcdefghij"))+"]")
	reviewed := h.ReviewSegmentMembership(ctx, c, "big.slo")
	// The graph moves to a different segment before the deletion runs.
	h.Seed(h.Primary, segs, "a/000003", []byte("the new one"), textType)
	h.SeedSLO(h.Primary, c, "big.slo", "["+entry("a/000003", []byte("the new one"))+"]")
	rec := h.DeleteManifestWithSegments(ctx, app.SegmentsDeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Name: "big.slo", Force: true, Confirm: "big.slo", Reviewed: reviewed,
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	h.AwaitJob(rec.JobID())
	rec.CreditJobConflict()
}
