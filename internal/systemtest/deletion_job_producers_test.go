package systemtest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// These producers credit the deletion jobs' scenario cells through the public
// service (ADR 0095 decision 9): the bulk deletion and the
// manifest-with-segments deletion are jobs now, requiring happy, refusal,
// conflict, cancel and partial. The happy bulk deletion is credited by
// TestPrefixDeleteSparesEverythingElse and the refusals by the refusal suite;
// these cover the rest on both the mock and the owned Swift.

// bulkDeletePartial deletes the plain member and refuses the link under the
// prefix, so the job is partial. No hook is needed, so it runs on either
// harness unchanged.
func bulkDeletePartial(t *testing.T, h *Harness) {
	ctx := context.Background()
	c := h.Container(h.Primary, "bulk-partial")
	h.Seed(h.Primary, c, "scope/keep-target", []byte("target"), textType)
	h.Seed(h.Primary, c, "scope/plain.txt", []byte("doomed"), textType)
	h.Seed(h.Primary, c, "scope/link", nil, map[string]string{"X-Symlink-Target": c + "/scope/keep-target"})
	rec := h.StartDelete(ctx, app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Prefix: "scope/", Mode: app.ModePrefix, Confirm: c + "/" + "scope/",
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "partial" {
		t.Fatalf("job = %s: %s", job.State, job.Error)
	}
	rec.CreditPartial()
}

func TestBulkDeletePartialMock(t *testing.T)  { bulkDeletePartial(t, findMockHarness(t)) }
func TestBulkDeletePartialSwift(t *testing.T) { bulkDeletePartial(t, New(t)) }

// segmentJobHappy deletes a dynamic manifest with its namespace as one job.
func segmentJobHappy(t *testing.T, h *Harness) {
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

func TestSegmentJobHappyMock(t *testing.T)  { segmentJobHappy(t, findMockHarness(t)) }
func TestSegmentJobHappySwift(t *testing.T) { segmentJobHappy(t, New(t)) }

// segmentJobConflict refuses a static manifest whose graph moved between the
// review and the execution, so the job records a conflicting member.
func segmentJobConflict(t *testing.T, h *Harness) {
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

func TestSegmentJobConflictMock(t *testing.T)  { segmentJobConflict(t, findMockHarness(t)) }
func TestSegmentJobConflictSwift(t *testing.T) { segmentJobConflict(t, New(t)) }

// bulkDeleteConflict refuses a member replaced after it was listed, before its
// comparison HEAD, so the job records a conflicting member. The replacement
// lands through a request hook: the mock's before-request hook or the owned
// Swift gate's before-request hook.
func bulkDeleteConflict(t *testing.T, h *Harness, bookmarkID string, arm func(replace func())) {
	ctx := context.Background()
	c := h.Container(h.Primary, "bulk-conflict")
	h.Seed(h.Primary, c, "scope/racy.txt", []byte("first"), textType)
	replaced := false
	arm(func() {
		if !replaced {
			replaced = true
			h.Seed(h.Primary, c, "scope/racy.txt", []byte("second, and different"), textType)
		}
	})
	rec := h.StartDelete(ctx, app.DeleteRequestDTO{
		BookmarkID: bookmarkID, Container: c, Prefix: "scope/", Mode: app.ModePrefix, Confirm: c + "/" + "scope/",
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	h.AwaitJob(rec.JobID())
	rec.CreditJobConflict()
}

func TestBulkDeleteConflictMock(t *testing.T) {
	h := findMockHarness(t)
	bulkDeleteConflict(t, h, h.Primary.BookmarkID, func(replace func()) {
		h.MockPrimary.BeforeRequest = func(method, path string) {
			if method == "HEAD" && strings.Contains(path, "/scope/racy.txt") {
				replace()
			}
		}
		t.Cleanup(func() { h.MockPrimary.BeforeRequest = nil })
	})
}

func TestBulkDeleteConflictSwift(t *testing.T) {
	h := New(t)
	g := newSwiftGate(t, h.Primary.authURL)
	gated := gatedSwift(t, h, g)
	bulkDeleteConflict(t, h, gated, func(replace func()) {
		g.mu.Lock()
		g.beforeAny = func(method, path string) {
			if method == "HEAD" && strings.Contains(path, "/scope/racy.txt") {
				replace()
			}
		}
		g.mu.Unlock()
	})
}

// bulkDeleteCancel cancels at the only member's baseline HEAD, before any
// DELETE, so the bulk deletion ends cancelled with nothing deleted.
func bulkDeleteCancel(t *testing.T, h *Harness, bookmarkID string, arm func(cancel func()), ready chan string) {
	ctx := context.Background()
	c := h.Container(h.Primary, "bulk-cancel")
	h.Seed(h.Primary, c, "keep-target", []byte("target"), textType)
	h.Seed(h.Primary, c, "scope/link", nil, map[string]string{"X-Symlink-Target": c + "/keep-target"})
	done := false
	arm(func() {
		if !done {
			done = true
			if err := h.Transfer.CancelJob(ctx, <-ready); err != nil {
				t.Error(err)
			}
		}
	})
	rec := h.StartDelete(ctx, app.DeleteRequestDTO{
		BookmarkID: bookmarkID, Container: c, Prefix: "scope/", Mode: app.ModePrefix, Confirm: c + "/" + "scope/",
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	ready <- rec.JobID()
	if job := h.AwaitJob(rec.JobID()); job.State != "cancelled" {
		t.Fatalf("job = %s: %s", job.State, job.Error)
	}
	rec.CreditCancelled()
}

func TestBulkDeleteCancelMock(t *testing.T) {
	h := findMockHarness(t)
	ready := make(chan string, 1)
	bulkDeleteCancel(t, h, h.Primary.BookmarkID, func(cancel func()) {
		h.MockPrimary.AfterRequest = func(method, path string) {
			if method == "HEAD" && strings.HasSuffix(path, "/scope/link") {
				cancel()
			}
		}
		t.Cleanup(func() { h.MockPrimary.AfterRequest = nil })
	}, ready)
}

func TestBulkDeleteCancelSwift(t *testing.T) {
	h := New(t)
	g := newSwiftGate(t, h.Primary.authURL)
	gated := gatedSwift(t, h, g)
	ready := make(chan string, 1)
	bulkDeleteCancel(t, h, gated, func(cancel func()) {
		g.mu.Lock()
		g.afterAny = func(method, path string) {
			if method == "HEAD" && strings.HasSuffix(path, "/scope/link") {
				cancel()
			}
		}
		g.mu.Unlock()
	}, ready)
}
