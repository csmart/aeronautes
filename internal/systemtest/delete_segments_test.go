package systemtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// sweepWithSegments deletes a container with the option that takes segments
// (ADR 0119): a plain object, a static and a dynamic large object go with
// their segments, the container goes, and nothing else on the account moves,
// neither a neighbour's large object, nor its segment, nor an object in the
// segments container that no manifest of the scope names. The scenario cells
// of the deletion are credited by the deletion's own producers; this proves
// the option against the store's own large objects.
func sweepWithSegments(t *testing.T, h *Harness) {
	ctx := context.Background()
	c := h.Container(h.Primary, "del-segs")
	parts := h.Container(h.Primary, "del-segs-parts")
	neighbour := h.Container(h.Primary, "del-segs-keep")
	entry := func(name string, body []byte) string {
		return fmt.Sprintf(`{"path":"/%s/%s","etag":"%s","size_bytes":%d}`, parts, name, md5Hex(body), len(body))
	}
	h.Seed(h.Primary, c, "plain.txt", []byte("plain"), textType)
	h.Seed(h.Primary, parts, "slo/000001", []byte("0123456789"), textType)
	h.Seed(h.Primary, parts, "slo/000002", []byte("abcdefghij"), textType)
	h.SeedSLO(h.Primary, c, "big.slo", "["+entry("slo/000001", []byte("0123456789"))+","+entry("slo/000002", []byte("abcdefghij"))+"]")
	for i := range 3 {
		h.Seed(h.Primary, parts, fmt.Sprintf("dlo/pfx/00000%d", i), []byte("segment"), textType)
	}
	h.Seed(h.Primary, c, "big.dlo", nil, map[string]string{"X-Object-Manifest": parts + "/dlo/pfx"})
	h.Seed(h.Primary, parts, "keep/000001", []byte("klmnopqrst"), textType)
	h.SeedSLO(h.Primary, neighbour, "kept.slo", "["+entry("keep/000001", []byte("klmnopqrst"))+"]")
	h.Seed(h.Primary, parts, "unrelated", []byte("no manifest names this"), textType)

	before := h.SnapshotNamespace(h.Primary)
	rec := h.StartDelete(ctx, app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Mode: app.ModeDeleteContainer, Segments: true, Confirm: "delete " + c + " and segments",
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
		t.Fatalf("delete job = %s: %s", job.State, job.Error)
	}
	after := h.SnapshotNamespace(h.Primary)

	gone := []string{c + "/plain.txt", c + "/big.slo", c + "/big.dlo",
		parts + "/slo/000001", parts + "/slo/000002", parts + "/dlo/pfx/000000", parts + "/dlo/pfx/000001", parts + "/dlo/pfx/000002"}
	h.AssertNoCollateral(before, after, append([]string{c}, gone...)...)
	if _, standing := after.Containers[c]; standing {
		t.Fatalf("container %s still stands", c)
	}
	for _, segment := range []string{"slo/000001", "slo/000002", "dlo/pfx/000000", "dlo/pfx/000001", "dlo/pfx/000002"} {
		if _, there := after.Containers[parts].Objects[segment]; there {
			t.Fatalf("segment %s survived", segment)
		}
	}
}

func TestSweepWithSegmentsMock(t *testing.T)  { sweepWithSegments(t, findMockHarness(t)) }
func TestSweepWithSegmentsSwift(t *testing.T) { sweepWithSegments(t, New(t)) }
