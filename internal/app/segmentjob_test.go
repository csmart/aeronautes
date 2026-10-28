package app

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// seedSLO seeds a static large object and its whole-object segments whose
// recorded hashes match the stored bytes, so the segment members compare
// equal at execution.
func seedSLO(h *harness, container, name, segContainer string, segments map[string]string) {
	h.mock.AddContainer(container, "standard")
	h.mock.AddContainer(segContainer, "standard")
	entries := make([]string, 0, len(segments))
	for seg, content := range segments {
		seedObject(h.mock, segContainer, seg, content, nil)
		entries = append(entries, fmt.Sprintf(`{"name":"/%s/%s","hash":"%s","bytes":%d}`, segContainer, seg, md5hex(content), len(content)))
	}
	h.mock.PutObject(container, name, swifttest.Object{
		Bytes: 20, SLO: true, SLOEtag: "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f",
		SLOManifestJSON: "[" + strings.Join(entries, ",") + "]", ContentType: "application/octet-stream",
	})
}

func reviewOf(t *testing.T, service *DeleteService, bookmark, container, name string) ReviewedMembershipDTO {
	t.Helper()
	status, err := service.CheckSegmentOwnership(context.Background(), SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: container, Name: name,
	})
	if err != nil {
		t.Fatalf("review %s/%s: %v", container, name, err)
	}
	return status.Reviewed
}

func segmentReadable(t *testing.T, h *harness, bookmark, container, name string) bool {
	t.Helper()
	_, err := h.transfer.connections.sessions[bookmark].session.ObjectDetail(context.Background(), container, name)
	return err == nil
}

// ADR 0095 decision 8: a static manifest with its whole-object segments is
// deleted as one job — the manifest manifest-only, then each reviewed entry
// once by the per-name protocol, no recursive multipart request, the ledger
// carrying the manifest and every segment as a deleted member.
func TestSegmentJobDeletesStaticManifestAndSegments(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	seedSLO(h, "media", "big.slo", "parts", map[string]string{"job/aa/000001": "0123456789", "job/aa/000002": "abcdefghij"})
	seedObject(h.mock, "parts", "elsewhere/x", "shared!", nil)
	reviewed := reviewOf(t, service, bookmark, "media", "big.slo")
	if len(reviewed.Entries) != 2 || reviewed.Class != "slo" {
		t.Fatalf("reviewed = %+v", reviewed)
	}
	jobID, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "big.slo", Confirm: "big.slo", Force: true, Reviewed: reviewed,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Type != "delete_segments" {
		t.Fatalf("job = %+v", job)
	}
	if segmentReadable(t, h, bookmark, "media", "big.slo") {
		t.Fatal("the manifest survived")
	}
	for _, seg := range []string{"job/aa/000001", "job/aa/000002"} {
		if segmentReadable(t, h, bookmark, "parts", seg) {
			t.Fatalf("segment %s survived", seg)
		}
	}
	if !segmentReadable(t, h, bookmark, "parts", "elsewhere/x") {
		t.Fatal("an uninvolved object was deleted")
	}
	totals, err := h.jobsSvc.ItemTotals(ctx, bookmark, jobID)
	if err != nil || totals.Deleted != 3 || totals.Admitted != 3 {
		t.Fatalf("totals = %+v %v (manifest + two segments)", totals, err)
	}
	for _, line := range h.mock.RequestLog() {
		if strings.Contains(line, "multipart-manifest=delete") {
			t.Fatalf("a recursive multipart delete was sent: %s", line)
		}
	}
}

// ADR 0095 decision 8: a dynamic manifest is deleted with its namespace swept
// by the per-name protocol; an external manifest sharing neither namespace is
// untouched.
func TestSegmentJobDeletesDynamicManifestAndNamespace(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("media_segments", "standard")
	for i := range 3 {
		seedObject(h.mock, "media_segments", fmt.Sprintf("owned/pfx/00000%d", i), "seg", nil)
		seedObject(h.mock, "media_segments", fmt.Sprintf("external/pfx/00000%d", i), "seg", nil)
	}
	h.mock.PutObject("media", "ours.bin", swifttest.Object{Manifest: "media_segments/owned/pfx/"})
	h.mock.PutObject("media", "theirs.bin", swifttest.Object{Manifest: "media_segments/external/pfx/"})
	reviewed := reviewOf(t, service, bookmark, "media", "ours.bin")
	if reviewed.Class != "dlo" || reviewed.Namespace != "media_segments/owned/pfx/" {
		t.Fatalf("reviewed = %+v", reviewed)
	}
	jobID, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "ours.bin", Confirm: "ours.bin", Force: true, Reviewed: reviewed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" {
		t.Fatalf("job = %+v", job)
	}
	if segmentReadable(t, h, bookmark, "media", "ours.bin") {
		t.Fatal("the manifest survived")
	}
	for i := range 3 {
		if segmentReadable(t, h, bookmark, "media_segments", fmt.Sprintf("owned/pfx/00000%d", i)) {
			t.Fatalf("owned segment %d survived", i)
		}
		if !segmentReadable(t, h, bookmark, "media_segments", fmt.Sprintf("external/pfx/00000%d", i)) {
			t.Fatalf("external segment %d was deleted", i)
		}
	}
}

// ADR 0095 decision 8: the deletion is always force-gated; without the force
// nothing starts and the manifest survives.
func TestSegmentJobRefusesWithoutForce(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	seedSLO(h, "media", "big.slo", "parts", map[string]string{"job/aa/000001": "0123456789"})
	reviewed := reviewOf(t, service, bookmark, "media", "big.slo")
	_, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "big.slo", Confirm: "big.slo", Reviewed: reviewed,
	})
	if err == nil || !strings.Contains(err.Error(), "Backstage force") {
		t.Fatalf("without force = %v, want a force-required refusal", err)
	}
	if !segmentReadable(t, h, bookmark, "media", "big.slo") {
		t.Fatal("the manifest was deleted despite the refusal")
	}
}

// ADR 0095 decision 8: a static manifest whose graph is swapped for one of
// equal assembled identity between the review and the execution is refused,
// the job failed, nothing deleted and its segments untouched.
func TestSegmentJobRefusesAnEqualTagGraphSwap(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	seedSLO(h, "media", "swap.slo", "parts", map[string]string{"job/cc/000001": "0123456789"})
	seedObject(h.mock, "parts", "other/keep", "unrelated!", nil)
	reviewed := reviewOf(t, service, bookmark, "media", "swap.slo")
	// Swap the graph for a different segment, keeping the SLO etag unchanged.
	swapped := fmt.Sprintf(`[{"name":"/parts/other/keep","hash":"%s","bytes":9}]`, md5hex("unrelated!"))
	h.mock.PutObject("media", "swap.slo", swifttest.Object{
		Bytes: 20, SLO: true, SLOEtag: "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f",
		SLOManifestJSON: swapped, ContentType: "application/octet-stream",
	})
	jobID, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "swap.slo", Confirm: "swap.slo", Force: true, Reviewed: reviewed,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "failed" {
		t.Fatalf("job = %+v, want failed for the swapped graph", job)
	}
	if !segmentReadable(t, h, bookmark, "media", "swap.slo") {
		t.Fatal("the swapped manifest was deleted")
	}
	if !segmentReadable(t, h, bookmark, "parts", "job/cc/000001") || !segmentReadable(t, h, bookmark, "parts", "other/keep") {
		t.Fatal("a segment was deleted despite the refusal")
	}
	item := itemsOf(t, h, jobID)["media/swap.slo"]
	if item.Execution == nil || item.Execution.Result != jobs.ItemConflict {
		t.Fatalf("manifest item = %+v, want a conflict", item)
	}
	// The conflict keeps the identity the operator reviewed as its baseline,
	// not the later startup read (ADR 0095 decision 8).
	if item.Baseline == nil || item.Baseline.Class != "slo" || item.Baseline.Length != 20 {
		t.Fatalf("the reviewed identity was not kept on the conflict: %+v", item.Baseline)
	}
	if item.Successor == nil || item.Successor.Role != "comparison" {
		t.Fatalf("the found identity was not kept on the conflict: %+v", item.Successor)
	}
}

// ADR 0095 decision 8: a manifest whose DELETE reply is lost is unverified,
// the job partial, and no segment is attempted.
func TestSegmentJobLeavesSegmentsOnUnverifiedManifest(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	seedSLO(h, "media", "big.slo", "parts", map[string]string{"job/aa/000001": "0123456789"})
	reviewed := reviewOf(t, service, bookmark, "media", "big.slo")
	h.mock.DropNextObjectDELETE()
	jobID, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "big.slo", Confirm: "big.slo", Force: true, Reviewed: reviewed,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "partial" {
		t.Fatalf("job = %+v, want partial", job)
	}
	manifest := itemsOf(t, h, jobID)["media/big.slo"]
	if manifest.Execution == nil || manifest.Execution.Result != jobs.ItemUnverified {
		t.Fatalf("manifest item = %+v, want unverified", manifest)
	}
	if !segmentReadable(t, h, bookmark, "parts", "job/aa/000001") {
		t.Fatal("a segment was deleted though the manifest was unverified")
	}
	if deletes := deletesUnder(h, "parts"); len(deletes) != 0 {
		t.Fatalf("segment DELETEs = %v, want none", deletes)
	}
}

// ADR 0095 decision 8: a reference repeated in the manifest is one member,
// requested once.
func TestSegmentJobDeletesARepeatedReferenceOnce(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("parts", "standard")
	seedObject(h.mock, "parts", "job/aa/000001", "0123456789", nil)
	hash := md5hex("0123456789")
	manifest := fmt.Sprintf(`[{"name":"/parts/job/aa/000001","hash":"%s","bytes":10},{"name":"/parts/job/aa/000001","hash":"%s","bytes":10}]`, hash, hash)
	h.mock.PutObject("media", "dup.slo", swifttest.Object{
		Bytes: 20, SLO: true, SLOEtag: "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f",
		SLOManifestJSON: manifest, ContentType: "application/octet-stream",
	})
	reviewed := reviewOf(t, service, bookmark, "media", "dup.slo")
	jobID, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "dup.slo", Confirm: "dup.slo", Force: true, Reviewed: reviewed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" {
		t.Fatalf("job = %+v", job)
	}
	totals, err := h.jobsSvc.ItemTotals(ctx, bookmark, jobID)
	if err != nil || totals.Admitted != 2 || totals.Deleted != 2 {
		t.Fatalf("totals = %+v %v (manifest + one deduped segment)", totals, err)
	}
	seen := 0
	for _, line := range h.mock.RequestLog() {
		if strings.HasPrefix(line, "DELETE /v1/AUTH_test/parts/job/aa/000001") {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("the repeated reference was requested %d times, want once", seen)
	}
}

// ADR 0095 decision 8, boundary one: the manifest's own outcome write failing
// after its DELETE leaves only its issued mark, no segment attempted, the job
// unavailable, and the member unresolved on reopen with no deleted outcome.
func TestSegmentJobLeavesTheJobOpenWhenTheManifestRecordIsLost(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	seedSLO(h, "media", "big.slo", "parts", map[string]string{"job/aa/000001": "0123456789"})
	reviewed := reviewOf(t, service, bookmark, "media", "big.slo")
	refuseSQL(t, h, `CREATE TRIGGER refuse_seg_outcomes BEFORE INSERT ON job_item_observations
		BEGIN SELECT RAISE(ABORT,'fixture required outcome refusal'); END`)
	jobID, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "big.slo", Confirm: "big.slo", Force: true, Reviewed: reviewed,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := 0
	for {
		events, err := h.auditSvc.RecentFor(ctx, bookmark, 20)
		if err != nil {
			t.Fatal(err)
		}
		noted := false
		for _, e := range events {
			if e.Operation == "segments.delete.unavailable" {
				noted = true
			}
		}
		if noted {
			break
		}
		if deadline++; deadline > 200 {
			t.Fatal("the job never reported its records unavailable")
		}
		awaitTick()
	}
	if err := h.transfer.jobs.Available(); err == nil {
		t.Fatal("the ledger did not latch unavailable after the lost manifest record")
	}
	if deletes := deletesUnder(h, "parts"); len(deletes) != 0 {
		t.Fatalf("a segment was deleted after the lost manifest record: %v", deletes)
	}
	refuseSQL(t, h, `DROP TRIGGER refuse_seg_outcomes`)
	reopened := jobs.NewStore(h.db)
	if _, err := reopened.FailInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	totals, err := reopened.ReadItemTotals(ctx, jobID)
	if err != nil || totals.Deleted != 0 || totals.Unresolved != 1 {
		t.Fatalf("reopened totals = %+v %v (manifest unresolved, nothing deleted)", totals, err)
	}
}

// ADR 0044 under the segment job: a dynamic manifest that reads a name outside
// its namespace is refused before any effect, with nothing deleted.
func TestSegmentJobRefusesAnOutsideNamespaceName(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("media_segments", "standard")
	seedObject(h.mock, "media_segments", "pfx/0001", "seg", nil)
	// A sibling that shares the prefix string but not the namespace boundary.
	seedObject(h.mock, "media_segments", "pfxevil", "unrelated", nil)
	h.mock.PutObject("media", "wide.bin", swifttest.Object{Manifest: "media_segments/pfx"})
	reviewed := reviewOf(t, service, bookmark, "media", "wide.bin")
	_, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "wide.bin", Confirm: "wide.bin", Force: true, Reviewed: reviewed,
	})
	if err == nil || !strings.Contains(err.Error(), "outside its own namespace") {
		t.Fatalf("outside-namespace delete = %v, want a refusal", err)
	}
	if !segmentReadable(t, h, bookmark, "media", "wide.bin") || !segmentReadable(t, h, bookmark, "media_segments", "pfx/0001") {
		t.Fatal("the manifest or a segment was deleted despite the outside-name refusal")
	}
}

// ADR 0095 decision 8: a segment whose object name equals the manifest's
// container/object key is a distinct member, because every member in this
// multi-container job is keyed by its own container and object, so it is not
// mistaken for the already-held manifest.
func TestSegmentJobKeysSegmentsByContainer(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("parts", "standard")
	// The segment's object name is "media/manifest", the same string as the
	// manifest member's container/object key.
	seedObject(h.mock, "parts", "media/manifest", "segment bytes", nil)
	h.mock.PutObject("media", "manifest", swifttest.Object{Manifest: "parts/media"})
	reviewed := reviewOf(t, service, bookmark, "media", "manifest")
	jobID, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "manifest", Force: true, Confirm: "manifest", Reviewed: reviewed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" {
		t.Fatalf("job = %s: %s", job.State, job.Error)
	}
	if segmentReadable(t, h, bookmark, "media", "manifest") {
		t.Fatal("the manifest survived")
	}
	if segmentReadable(t, h, bookmark, "parts", "media/manifest") {
		t.Fatal("the segment whose name collided with the manifest key was not deleted")
	}
	totals, err := h.jobsSvc.ItemTotals(ctx, bookmark, jobID)
	if err != nil || totals.Admitted != 2 || totals.Deleted != 2 {
		t.Fatalf("totals = %+v %v (manifest + one distinct segment)", totals, err)
	}
}

// ADR 0095 decision 8: a dynamic sweep whose namespace listing fails after the
// manifest's deletion has committed ends partial with the deleted manifest
// disclosed, never complete; ledger totals alone cannot say an unlisted
// namespace was swept.
func TestSegmentJobPartialWhenTheNamespaceListingFails(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("media_segments", "standard")
	seedObject(h.mock, "media_segments", "owned/pfx/0001", "seg", nil)
	h.mock.PutObject("media", "ours.bin", swifttest.Object{Manifest: "media_segments/owned/pfx/"})
	reviewed := reviewOf(t, service, bookmark, "media", "ours.bin")
	failed := false
	h.mock.AfterRequest = func(method, path string) {
		// After the manifest's DELETE, the namespace sweep's listing fails.
		if method == "DELETE" && strings.HasSuffix(path, "/media/ours.bin") && !failed {
			failed = true
			h.mock.FailNextListings(10)
		}
	}
	jobID, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "ours.bin", Force: true, Confirm: "ours.bin", Reviewed: reviewed,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	h.mock.AfterRequest = nil
	h.mock.FailNextListings(0)
	if job.State != "partial" || !strings.Contains(job.Error, "the manifest is gone") {
		t.Fatalf("job = %s: %s, want partial disclosing the deleted manifest", job.State, job.Error)
	}
	if segmentReadable(t, h, bookmark, "media", "ours.bin") {
		t.Fatal("the manifest was not deleted")
	}
}
