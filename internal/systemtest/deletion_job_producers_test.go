package systemtest

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// These producers credit the deletion jobs' scenario cells through the public
// service (ADR 0095 decision 9, ADR 0122 decision 6): the bulk deletion and
// the manifest-with-segments deletion are jobs, requiring happy, refusal,
// cancel and partial. The happy bulk deletion is credited by
// TestPrefixDeleteSparesEverythingElse and the refusals by the refusal suite;
// these cover the rest on both the mock and the owned Swift.

// bulkDeletePartial deletes the members the store accepts and records the
// one it refuses, so the job is partial. The refusal is armed on the mock or
// on the owned Swift's gate, and lifted with a status of 0 so that the
// fixture can be removed.
func bulkDeletePartial(t *testing.T, h *Harness, bookmarkID string, refuse func(container, name string, status int)) {
	ctx := context.Background()
	c := h.Container(h.Primary, "bulk-partial")
	h.Seed(h.Primary, c, "scope/plain.txt", []byte("doomed"), textType)
	h.Seed(h.Primary, c, "scope/held.txt", []byte("the store refuses this one"), textType)
	refuse(c, "scope/held.txt", http.StatusForbidden)
	t.Cleanup(func() { refuse(c, "scope/held.txt", 0) })
	rec := h.StartDelete(ctx, app.DeleteRequestDTO{
		BookmarkID: bookmarkID, Container: c, Prefix: "scope/", Mode: app.ModePrefix, Confirm: c + "/" + "scope/",
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "partial" || job.Counters.ObjectsDeleted != 1 {
		t.Fatalf("job = %s: %s", job.State, job.Error)
	}
	after := h.Snapshot(h.Primary, c)
	if _, still := after.Containers[c].Objects["scope/held.txt"]; !still {
		t.Fatal("the refused member is gone")
	}
	if _, still := after.Containers[c].Objects["scope/plain.txt"]; still {
		t.Fatal("the accepted member is still there")
	}
	rec.CreditPartial()
}

func TestBulkDeletePartialMock(t *testing.T) {
	h := findMockHarness(t)
	bulkDeletePartial(t, h, h.Primary.BookmarkID, h.MockPrimary.RefuseObjectDELETE)
}

func TestBulkDeletePartialSwift(t *testing.T) {
	h := New(t)
	g := newSwiftGate(t, h.Primary.authURL)
	gated := gatedSwift(t, h, g)
	bulkDeletePartial(t, h, gated, g.refuseObjectDELETE)
}

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

// bulkDeleteCancel cancels as the scope's listing is asked for, before any
// DELETE, so the bulk deletion ends cancelled with nothing deleted.
func bulkDeleteCancel(t *testing.T, h *Harness, bookmarkID string, arm func(container string, cancel func()), ready chan string) {
	ctx := context.Background()
	c := h.Container(h.Primary, "bulk-cancel")
	h.Seed(h.Primary, c, "scope/a.txt", []byte("alpha"), textType)
	before := h.Snapshot(h.Primary, c)
	done := false
	arm(c, func() {
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
	if d := diffOf(before, h.Snapshot(h.Primary, c)); !d.Empty() {
		t.Fatalf("a cancelled job changed state:\n%s", d)
	}
	rec.CreditCancelled()
}

func TestBulkDeleteCancelMock(t *testing.T) {
	h := findMockHarness(t)
	ready := make(chan string, 1)
	bulkDeleteCancel(t, h, h.Primary.BookmarkID, func(container string, cancel func()) {
		h.MockPrimary.BeforeRequest = func(method, path string) {
			if method == "GET" && strings.HasSuffix(path, "/"+container) {
				cancel()
			}
		}
		t.Cleanup(func() { h.MockPrimary.BeforeRequest = nil })
	}, ready)
}

func TestBulkDeleteCancelSwift(t *testing.T) {
	h := New(t)
	g := newSwiftGate(t, h.Primary.authURL)
	gated := gatedSwift(t, h, g)
	ready := make(chan string, 1)
	bulkDeleteCancel(t, h, gated, func(container string, cancel func()) {
		g.mu.Lock()
		g.beforeAny = func(method, path string) {
			if method == "GET" && strings.HasSuffix(path, "/"+container) {
				cancel()
			}
		}
		g.mu.Unlock()
	}, ready)
}
