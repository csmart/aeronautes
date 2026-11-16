package systemtest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// On the owned Swift, in a container with a history location (ADR 0120
// proof): the store keeps a deleted manifest in the archive as a manifest
// that names the same segments, so neither deletion takes segments there.
// The one-object deletion with segments and the bulk option are refused;
// without them the manifest, then the container, go and every segment
// stays. The archive is a container of its own: deleting it with the option
// deletes the versions and their segments, and nothing else.
func TestLargeObjectsUnderAHistoryLocationSwift(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "hist-live")
	archive := h.Container(h.Primary, "hist-archive")
	parts := h.Container(h.Primary, "hist-parts")
	h.rawPut(h.Primary, "/"+c, nil, map[string]string{"X-History-Location": archive})
	t.Cleanup(func() {
		// A failed run can leave the container standing; the store refuses a
		// delete there once the archive is gone, so the harness could not
		// empty it.
		if t.Failed() {
			h.rawPut(h.Primary, "/"+c, nil, map[string]string{"X-Remove-History-Location": "x"})
		}
	})
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
	h.Seed(h.Primary, parts, "unrelated", []byte("no manifest names this"), textType)
	everyPart := []string{"dlo/pfx/000000", "dlo/pfx/000001", "dlo/pfx/000002", "slo/000001", "slo/000002", "unrelated"}

	listed := func(container string) []string {
		t.Helper()
		names, err := h.Primary.Oracle.ListObjects(container)
		if err != nil {
			t.Fatalf("list %s: %v", container, err)
		}
		slices.Sort(names)
		return names
	}
	// versions is what the archive keeps of the deleted objects, by the name
	// each came from, leaving out the store's deletion markers.
	versions := func() map[string]app.ObjectDetailDTO {
		t.Helper()
		kept := map[string]app.ObjectDetailDTO{}
		for _, name := range listed(archive) {
			detail, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, archive, name)
			if err != nil {
				t.Fatalf("read archived %s: %v", name, err)
			}
			if strings.HasPrefix(detail.ContentType, "application/x-deleted") {
				continue
			}
			// The store files a version under three hex digits of the name's
			// length, the name, a slash and a timestamp.
			kept[name[3:strings.LastIndex(name, "/")]] = detail
		}
		return kept
	}

	reviewed := h.ReviewSegmentMembership(ctx, c, "big.slo")
	rec := h.DeleteManifestWithSegments(ctx, app.SegmentsDeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Name: "big.slo", Force: true, Confirm: "big.slo", Reviewed: reviewed})
	if rec.Err() == nil || !strings.Contains(rec.Err().Error(), "keeps versions") {
		t.Fatalf("one object with its segments: job %q, err %v; want it refused because versions are kept", rec.JobID(), rec.Err())
	}
	if got := listed(c); !slices.Equal(got, []string{"big.dlo", "big.slo", "plain.txt"}) || len(listed(archive)) != 0 || !slices.Equal(listed(parts), everyPart) {
		t.Fatalf("the refusal changed something: %s holds %v, the archive %v, the segments %v", c, got, listed(archive), listed(parts))
	}

	shown, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "big.slo")
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.Mutate.DeleteManifest(ctx, h.ActiveScope(ctx), c, "big.slo")
	if err != nil || result.Outcome != "applied" || result.ObjectAfter != nil {
		t.Fatalf("manifest only: result = %+v, err = %v; want applied with the name gone", result, err)
	}
	if kept := versions(); len(kept) != 1 || kept["big.slo"].Class != "slo" || kept["big.slo"].ContentLength != shown.ContentLength {
		t.Fatalf("the archive keeps %+v, want the static manifest as a static large object of %d bytes", kept, shown.ContentLength)
	}
	if got := listed(parts); !slices.Equal(got, everyPart) {
		t.Fatalf("a manifest-only deletion changed the segments: %v", got)
	}

	rec = h.StartDelete(ctx, app.DeleteRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: c, Mode: app.ModeDeleteContainer,
		Segments: true, Confirm: "delete " + c + " and segments"})
	if rec.Err() == nil || !strings.Contains(rec.Err().Error(), "keeps versions") {
		t.Fatalf("the container with segments: job %q, err %v; want it refused because versions are kept", rec.JobID(), rec.Err())
	}
	rec = h.StartDelete(ctx, app.DeleteRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: c, Mode: app.ModeDeleteContainer, Confirm: "delete " + c})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
		t.Fatalf("the container: job = %s: %s", job.State, job.Error)
	}
	if _, standing := h.SnapshotNamespace(h.Primary).Containers[c]; standing {
		t.Fatalf("container %s still stands", c)
	}
	kept := versions()
	if len(kept) != 3 || kept["big.slo"].Class != "slo" || kept["big.dlo"].Class != "dlo" || kept["big.dlo"].Manifest != parts+"/dlo/pfx" || kept["plain.txt"].Class != "plain" {
		t.Fatalf("the archive keeps %+v, want each object as the kind it was, the dynamic one naming the same prefix", kept)
	}
	if got := listed(parts); !slices.Equal(got, everyPart) {
		t.Fatalf("deleting the container changed the segments: %v", got)
	}

	rec = h.StartDelete(ctx, app.DeleteRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: archive, Mode: app.ModeDeleteContainer,
		Segments: true, Confirm: "delete " + archive + " and segments"})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
		t.Fatalf("the archive with segments: job = %s: %s", job.State, job.Error)
	}
	if _, standing := h.SnapshotNamespace(h.Primary).Containers[archive]; standing {
		t.Fatalf("archive %s still stands", archive)
	}
	if got := listed(parts); !slices.Equal(got, []string{"unrelated"}) {
		t.Fatalf("after the archive went with its segments, %s holds %v, want only the object no manifest named", parts, got)
	}
}
