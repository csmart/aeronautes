package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

func deleteHarness(t *testing.T) (*harness, *DeleteService, string) {
	t.Helper()
	h := newHarness(t)
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	return h, service, bookmark
}

func TestDeletePreviewCountsAndDisclosures(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "scope/a.txt", "aaaa", nil)
	seedObject(h.mock, "media", "scope/b.txt", "bbbbbb", nil)
	seedObject(h.mock, "media", "kept.txt", "kept", nil)
	h.mock.PutObject("media", "scope/manifest", swifttest.Object{Manifest: "media_segments/x/"})

	preview, err := service.PreviewDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Objects != 3 || preview.Bytes != 10 || preview.PossibleManifests != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Strategy != "bulk-delete" {
		t.Errorf("strategy = %s", preview.Strategy)
	}

	// Wrong typed confirmation refuses before any job exists.
	_, err = service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "meda",
	})
	if err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Errorf("bad confirmation accepted: %v", err)
	}
	// Prefix mode without a prefix refuses.
	if _, err := service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Mode: ModePrefix, Confirm: "media",
	}); err == nil {
		t.Error("prefixless prefix-mode accepted")
	}
	list, _ := h.jobsSvc.List(ctx, "", false, 10)
	if len(list) != 0 {
		t.Errorf("refused starts created jobs: %+v", list)
	}
}

func TestDeletePrefixJob(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("media_segments", "standard")
	for i := 0; i < 30; i++ {
		seedObject(h.mock, "media", "scope/obj-"+strings.Repeat("x", i%5)+string(rune('a'+i%26)), "data", nil)
	}
	seedObject(h.mock, "media", "kept.txt", "kept", nil)
	// A shared-segment manifest inside the scope; sibling outside.
	seedObject(h.mock, "media_segments", "shared/0", strings.Repeat("s", 64), nil)
	h.mock.PutObject("media", "scope/video", swifttest.Object{Manifest: "media_segments/shared/"})
	h.mock.PutObject("media", "sibling", swifttest.Object{Manifest: "media_segments/shared/"})

	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "media",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Type != "delete_prefix" {
		t.Fatalf("job = %+v", job)
	}
	if job.Counters.ObjectsPlanned == 0 || job.Counters.ObjectsCopied != job.Counters.ObjectsPlanned {
		t.Errorf("counters = %+v", job.Counters)
	}
	if !strings.Contains(job.Settings, "bulk-delete") || !strings.Contains(job.Settings, "limit=1") {
		t.Errorf("effective config = %q", job.Settings)
	}
	// Survivors: kept.txt and the sibling manifest, still assembling.
	d, err := h.transfer.connections.sessions[bookmark].session.ObjectDetail(ctx, "media", "sibling")
	if err != nil || d.ContentLength != 64 {
		t.Fatalf("sibling = %+v %v (shared segments must survive)", d, err)
	}
}

func TestEmptyAndDeleteContainerJobs(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "one.txt", "1", nil)
	seedObject(h.mock, "media", "two.txt", "22", nil)
	session := h.transfer.connections.sessions[bookmark].session
	// Container config that must survive Empty (§33): empty base
	// fingerprint = unconditional apply in the mutate flow.
	if _, err := h.mutate.UpdateContainer(ctx, "media", "", ContainerEditDTO{
		Meta:      map[string]string{"Team": "ops"},
		ReadACL:   ".r:*",
		AckPublic: true,
	}); err != nil {
		t.Fatal(err)
	}

	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Mode: ModeEmpty, Confirm: "media",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" {
		t.Fatalf("empty job = %+v", job)
	}
	awaitJobAudit(t, h, jobID, "delete.empty.complete")
	after, err := session.ContainerDetail(ctx, "media")
	if err != nil {
		t.Fatal(err)
	}
	if after.Meta["Team"] != "ops" || after.ObjectCount != 0 {
		t.Fatalf("after empty = meta %v count %d", after.Meta, after.ObjectCount)
	}

	// Delete the (now empty) container entirely.
	jobID, err = service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Mode: ModeDeleteContainer, Confirm: "media",
	})
	if err != nil {
		t.Fatal(err)
	}
	job = awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Type != "delete_container" {
		t.Fatalf("delete-container job = %+v", job)
	}
	if _, err := session.ContainerDetail(ctx, "media"); err == nil {
		t.Fatal("container survived")
	}

	// Audit carries the destructive trail.
	awaitJobAudit(t, h, jobID, "delete.delete_container.complete")
	events, err := h.auditSvc.Recent(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var starts, completes int
	for _, e := range events {
		if strings.HasPrefix(e.Operation, "delete.") && strings.HasSuffix(e.Operation, ".start") {
			starts++
		}
		if strings.HasPrefix(e.Operation, "delete.") && strings.HasSuffix(e.Operation, ".complete") {
			completes++
		}
	}
	if starts != 2 || completes != 2 {
		t.Errorf("audit: %d starts %d completes", starts, completes)
	}
}

func TestSegmentDeleteDLOOwnership(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("media_segments", "standard")

	// An owned DLO (prefix recorded as job-written) and an external one.
	for i := 0; i < 3; i++ {
		seedObject(h.mock, "media_segments", "owned/pfx/0000000"+string(rune('0'+i)), "seg", nil)
		seedObject(h.mock, "media_segments", "external/pfx/0000000"+string(rune('0'+i)), "seg", nil)
	}
	h.mock.PutObject("media", "ours.bin", swifttest.Object{Manifest: "media_segments/owned/pfx/"})
	h.mock.PutObject("media", "theirs.bin", swifttest.Object{Manifest: "media_segments/external/pfx/"})
	if err := h.evidence.RecordOwnership(ctx, bookmark, "job-x", "media_segments", "owned/pfx/"); err != nil {
		t.Fatal(err)
	}

	// Status: proven for ours, not for theirs.
	status, err := service.CheckSegmentOwnership(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "ours.bin",
	})
	if err != nil || !status.Proven || status.Class != "dlo" {
		t.Fatalf("ours status = %+v %v", status, err)
	}
	status, err = service.CheckSegmentOwnership(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "theirs.bin",
	})
	if err != nil || status.Proven {
		t.Fatalf("theirs status = %+v %v", status, err)
	}

	// Unproven without force: refused, nothing deleted.
	_, err = service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "theirs.bin", Confirm: "theirs.bin",
	})
	if err == nil || !strings.Contains(err.Error(), "Backstage force") {
		t.Fatalf("unproven delete = %v", err)
	}
	session := h.transfer.connections.sessions[bookmark].session
	if _, err := session.ObjectDetail(ctx, "media", "theirs.bin"); err != nil {
		t.Fatal("refusal deleted the manifest anyway")
	}

	// Wrong confirmation refused even for the owned one.
	if _, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "ours.bin", Confirm: "ours",
	}); err == nil {
		t.Fatal("bad confirmation accepted")
	}

	// Owned + force: manifest + exactly its prefix die; external stays.
	// (Force is now required for ALL segment deletes — evidence is
	// advisory until the live reference scan exists, review R-02.)
	result, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "ours.bin", Confirm: "ours.bin", Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ManifestDeleted || result.SegmentsDeleted != 3 || result.Basis != "backstage-forced-with-evidence" {
		t.Fatalf("result = %+v", result)
	}
	d, err := session.ObjectDetail(ctx, "media", "theirs.bin")
	if err != nil || d.ContentLength != 9 {
		t.Fatalf("external DLO harmed: %+v %v", d, err)
	}

	// Backstage force takes the external one, loudly.
	result, err = service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "theirs.bin", Confirm: "theirs.bin", Force: true,
	})
	if err != nil || result.Basis != "backstage-forced" || result.SegmentsDeleted != 3 {
		t.Fatalf("forced = %+v %v", result, err)
	}
	events, _ := h.auditSvc.Recent(ctx, 10)
	var sawForced bool
	for _, e := range events {
		if e.Operation == "segments.delete" && strings.Contains(e.Summary, "backstage-forced") {
			sawForced = true
		}
	}
	if !sawForced {
		t.Error("forced deletion not audited as backstage")
	}
}

func TestSegmentDeleteSLO(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("parts", "standard")
	seedObject(h.mock, "parts", "job/aa/000001", "0123456789", nil)
	seedObject(h.mock, "parts", "job/aa/000002", "abcdefghij", nil)
	seedObject(h.mock, "parts", "elsewhere/x", "shared!", nil)
	manifest := `[{"name":"/parts/job/aa/000001","hash":"h1","bytes":10},{"name":"/parts/job/aa/000002","hash":"h2","bytes":10}]`
	h.mock.PutObject("media", "big.slo", swifttest.Object{
		Bytes: 20, SLO: true, SLOManifestJSON: manifest, ContentType: "application/octet-stream",
	})
	mixed := `[{"name":"/parts/job/aa/000001","hash":"h1","bytes":10},{"name":"/parts/elsewhere/x","hash":"h3","bytes":7}]`
	h.mock.PutObject("media", "mixed.slo", swifttest.Object{
		Bytes: 17, SLO: true, SLOManifestJSON: mixed, ContentType: "application/octet-stream",
	})
	if err := h.evidence.RecordOwnership(ctx, bookmark, "job-y", "parts", "job/aa/"); err != nil {
		t.Fatal(err)
	}

	// Fully covered → proven; partially covered → not.
	status, err := service.CheckSegmentOwnership(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "big.slo",
	})
	if err != nil || !status.Proven || status.Segments != 2 {
		t.Fatalf("big.slo status = %+v %v", status, err)
	}
	status, err = service.CheckSegmentOwnership(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "mixed.slo",
	})
	if err != nil || status.Proven {
		t.Fatalf("mixed.slo status = %+v %v", status, err)
	}

	// Native manifest+segments for the proven one (Force now required
	// for all segment deletes, review R-02).
	result, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "big.slo", Confirm: "big.slo", Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ManifestDeleted || result.SegmentsDeleted != 2 {
		t.Fatalf("result = %+v", result)
	}
	session := h.transfer.connections.sessions[bookmark].session
	if _, err := session.ObjectDetail(ctx, "parts", "elsewhere/x"); err != nil {
		t.Fatal("uninvolved segment deleted")
	}
	if _, err := session.ObjectDetail(ctx, "parts", "job/aa/000001"); err == nil {
		t.Fatal("owned segment survived the native delete")
	}
}

func TestCopyJobOwnershipRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	srcBookmark := h.transfer.connections.ActiveBookmarkID()
	dstBookmark, dstMock := secondCluster(t, h)
	deleteSvc := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)

	// Chunked creation records the generated prefix for later inspection.
	h.installChunkingEngine(nil, 1024)
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "video.bin", strings.Repeat("v", 5000), nil)
	dstMock.AddContainer("backup", "standard")

	jobID, err := h.transfer.StartCopy(ctx, CopyRequestDTO{
		SourceBookmarkID: srcBookmark, SourceContainer: "media",
		DestBookmarkID: dstBookmark, DestContainer: "backup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" {
		t.Fatalf("job = %+v", job)
	}
	owned, err := h.evidence.Ownerships(ctx, dstBookmark, "backup_segments")
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 || owned[0].JobID != jobID {
		t.Fatalf("ownership rows = %+v", owned)
	}

	// Recorded creation is not exclusivity; segment deletion still needs
	// explicit force even when the legacy status field reports Proven.
	status, err := deleteSvc.CheckSegmentOwnership(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: dstBookmark, Container: "backup", Name: "video.bin",
	})
	if err != nil || !status.Proven || status.Class != "dlo" {
		t.Fatalf("status = %+v %v", status, err)
	}
	result, err := deleteSvc.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: dstBookmark, Container: "backup", Name: "video.bin", Confirm: "video.bin", Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ManifestDeleted || result.SegmentsDeleted != 5 || result.Basis != "backstage-forced-with-evidence" {
		t.Fatalf("cleanup = %+v", result)
	}
}

func TestAuditSearch(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "scope/x.txt", "x", nil)

	// Generate a mixed trail: a delete job plus a refused segments call.
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "media",
	})
	if err != nil {
		t.Fatal(err)
	}
	awaitTerminal(t, h, jobID)
	awaitJobAudit(t, h, jobID, "delete.prefix.complete")
	h.mock.PutObject("media", "ext.bin", swifttest.Object{Manifest: "media_segments/x/"})
	_, _ = service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "ext.bin", Confirm: "ext.bin",
	})

	all, err := h.auditSvc.Search(ctx, AuditQueryDTO{})
	if err != nil || len(all) < 3 {
		t.Fatalf("all = %d %v", len(all), err)
	}
	// Operation-prefix filter.
	deletes, err := h.auditSvc.Search(ctx, AuditQueryDTO{OperationPrefix: "delete.prefix"})
	if err != nil || len(deletes) != 2 { // start + terminal
		t.Fatalf("delete.prefix = %d %v", len(deletes), err)
	}
	// Result filter catches the refusal.
	failed, err := h.auditSvc.Search(ctx, AuditQueryDTO{Result: "failed"})
	if err != nil || len(failed) != 1 || !strings.Contains(failed[0].Summary, "Backstage force") {
		t.Fatalf("failed = %+v %v", failed, err)
	}
	// A LIKE metacharacter is literal, never a wildcard.
	if rows, _ := h.auditSvc.Search(ctx, AuditQueryDTO{OperationPrefix: "%"}); len(rows) != 0 {
		t.Fatalf("wildcard prefix matched %d rows", len(rows))
	}
	// Paging backwards.
	page1, err := h.auditSvc.Search(ctx, AuditQueryDTO{Limit: 2})
	if err != nil || len(page1) != 2 {
		t.Fatal(err)
	}
	page2, err := h.auditSvc.Search(ctx, AuditQueryDTO{Limit: 2, BeforeID: page1[1].ID})
	if err != nil || len(page2) == 0 || page2[0].ID >= page1[1].ID {
		t.Fatalf("page2 = %+v %v", page2, err)
	}
}

func TestNestedRangedAndBoundaryProofsRefused(t *testing.T) {
	// The review-wave C-05/G13 set. A nested sub-SLO whose manifest
	// OBJECT sits under an owned prefix must NOT prove: the proof covers
	// the nested manifest, not its descendants, and Swift's recursive
	// delete follows them. Ranged entries and non-slash prefix
	// boundaries are equally unprovable.
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("parts", "standard")
	seedObject(h.mock, "parts", "job/aa/000001", "0123456789", nil)
	seedObject(h.mock, "parts", "job/aa/sub.slo", "inner manifest", nil)
	if err := h.evidence.RecordOwnership(ctx, bookmark, "job-y", "parts", "job/aa/"); err != nil {
		t.Fatal(err)
	}

	// Nested: every entry under the owned prefix, one is a sub-SLO.
	nested := `[{"name":"/parts/job/aa/000001","hash":"h1","bytes":10},{"name":"/parts/job/aa/sub.slo","hash":"h2","bytes":14,"sub_slo":true}]`
	h.mock.PutObject("media", "nested.slo", swifttest.Object{Bytes: 24, SLO: true, SLOManifestJSON: nested, ContentType: "application/octet-stream"})
	status, err := service.CheckSegmentOwnership(ctx, SegmentsDeleteRequestDTO{BookmarkID: bookmark, Container: "media", Name: "nested.slo"})
	if err != nil || status.Proven {
		t.Fatalf("nested proof = %+v %v (must be unprovable)", status, err)
	}
	if _, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "nested.slo", Confirm: "nested.slo",
	}); err == nil {
		t.Fatal("nested SLO segment deletion proceeded without force")
	}

	// Ranged: whole-object deletion of a partially-referenced object is
	// not what the proof proves.
	ranged := `[{"name":"/parts/job/aa/000001","hash":"h1","bytes":5,"range":"0-4"}]`
	h.mock.PutObject("media", "ranged.slo", swifttest.Object{Bytes: 5, SLO: true, SLOManifestJSON: ranged, ContentType: "application/octet-stream"})
	status, err = service.CheckSegmentOwnership(ctx, SegmentsDeleteRequestDTO{BookmarkID: bookmark, Container: "media", Name: "ranged.slo"})
	if err != nil || status.Proven {
		t.Fatalf("ranged proof = %+v %v (must be unprovable)", status, err)
	}

	// Boundary: an owned "job/bb" (no trailing slash) must not cover
	// "job/bb-evil/x".
	if err := h.evidence.RecordOwnership(ctx, bookmark, "job-z", "parts", "job/bb"); err != nil {
		t.Fatal(err)
	}
	seedObject(h.mock, "parts", "job/bb-evil/x", "unrelated!", nil)
	evil := `[{"name":"/parts/job/bb-evil/x","hash":"h9","bytes":10}]`
	h.mock.PutObject("media", "evil.slo", swifttest.Object{Bytes: 10, SLO: true, SLOManifestJSON: evil, ContentType: "application/octet-stream"})
	status, err = service.CheckSegmentOwnership(ctx, SegmentsDeleteRequestDTO{BookmarkID: bookmark, Container: "media", Name: "evil.slo"})
	if err != nil || status.Proven {
		t.Fatalf("boundary proof = %+v %v (job/bb must not cover job/bb-evil/)", status, err)
	}
}

func TestSegmentDeleteNeverUsesRecursiveMultipart(t *testing.T) {
	// C-06's wire lock: the proven manifest+segments path must never
	// send ?multipart-manifest=delete — the server-side recursive
	// delete follows whatever manifest occupies the live name at
	// execution time. Reintroducing the fast path fails this test.
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("parts", "standard")
	seedObject(h.mock, "parts", "job/bb/000001", "0123456789", nil)
	manifest := `[{"name":"/parts/job/bb/000001","hash":"h1","bytes":10}]`
	h.mock.PutObject("media", "wire.slo", swifttest.Object{Bytes: 10, SLO: true, SLOManifestJSON: manifest, ContentType: "application/octet-stream"})
	if err := h.evidence.RecordOwnership(ctx, bookmark, "job-w", "parts", "job/bb/"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "wire.slo", Confirm: "wire.slo", Force: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, line := range h.mock.RequestLog() {
		if strings.Contains(line, "multipart-manifest=delete") {
			t.Fatalf("recursive multipart delete on the wire: %s", line)
		}
	}
}

func TestSegmentDeleteRefusesSwappedManifest(t *testing.T) {
	// The SLO-SWAP barrier: the live manifest is replaced right after
	// the proof fetch (planted by the mock's AfterRequest hook on that
	// exact HEAD) — the pre-delete identity re-check must refuse, the
	// replacement survive, and its segments stay untouched.
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("parts", "standard")
	seedObject(h.mock, "parts", "job/cc/000001", "0123456789", nil)
	seedObject(h.mock, "parts", "other/keep", "unrelated!", nil)
	manifest := `[{"name":"/parts/job/cc/000001","hash":"h1","bytes":10}]`
	h.mock.PutObject("media", "swap.slo", swifttest.Object{Bytes: 10, ETag: "etag-original", SLO: true, SLOManifestJSON: manifest, ContentType: "application/octet-stream"})
	if err := h.evidence.RecordOwnership(ctx, bookmark, "job-s", "parts", "job/cc/"); err != nil {
		t.Fatal(err)
	}
	replacement := `[{"name":"/parts/other/keep","hash":"h2","bytes":10}]`
	swapped := false
	h.mock.AfterRequest = func(method, path string) {
		// The proof fetch ends with the manifest GET; the swap lands
		// right after it — BETWEEN the reviewed graph and the
		// pre-delete identity re-HEAD.
		if method == "GET" && strings.HasSuffix(path, "/media/swap.slo") && !swapped {
			swapped = true
			h.mock.PutObject("media", "swap.slo", swifttest.Object{Bytes: 10, ETag: "etag-replacement", SLO: true, SLOManifestJSON: replacement, ContentType: "text/plain"})
		}
	}
	defer func() { h.mock.AfterRequest = nil }()

	_, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "swap.slo", Confirm: "swap.slo", Force: true,
	})
	if err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("swapped manifest delete = %v, want identity refusal", err)
	}
	session := h.transfer.connections.sessions[bookmark].session
	if _, err := session.ObjectDetail(ctx, "media", "swap.slo"); err != nil {
		t.Fatal("the replacement manifest was deleted despite the refusal")
	}
	if _, err := session.ObjectDetail(ctx, "parts", "job/cc/000001"); err != nil {
		t.Fatal("old-graph segments were deleted despite the refusal")
	}
}

func TestDeletePartialAfterIrreversibleSubset(t *testing.T) {
	// F-10 / Grok P1: a prefix delete that removes some objects and then
	// fails must end PARTIAL with the exact count — not a plain Failed
	// that erases the fact that data is already gone. The mock fails
	// deletes after the first batch lands.
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	for i := 0; i < 20; i++ {
		seedObject(h.mock, "media", fmt.Sprintf("doomed/obj-%03d", i), "x", nil)
	}
	// Delete a handful, then start failing: some objects are
	// irreversibly gone when the error surfaces.
	// Five objects delete, then every further delete fails per-item:
	// an irreversible subset is gone when the error surfaces.
	h.mock.FailDeletesAfter = 5
	defer func() { h.mock.FailDeletesAfter = 0 }()

	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "doomed/", Mode: ModePrefix, Confirm: "media",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "partial" {
		t.Fatalf("state = %s, want partial (some objects were deleted before the failure)", job.State)
	}
	if job.Counters.ObjectsCopied == 0 {
		t.Fatal("partial delete reported zero deletions")
	}
}

func TestProvenSegmentsStillNeedForce(t *testing.T) {
	// R-02 containment: even a PROVEN prefix must not auto-delete —
	// segment deletion is force-only until the live reference scan
	// exists, so a stale/shared evidence row cannot nuke shared data.
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("parts", "standard")
	seedObject(h.mock, "parts", "job/pp/000001", "0123456789", nil)
	manifest := `[{"name":"/parts/job/pp/000001","hash":"h1","bytes":10}]`
	h.mock.PutObject("media", "proven.slo", swifttest.Object{Bytes: 10, ETag: "e1", SLO: true, SLOManifestJSON: manifest, ContentType: "application/octet-stream"})
	if err := h.evidence.RecordOwnership(ctx, bookmark, "job-p", "parts", "job/pp/"); err != nil {
		t.Fatal(err)
	}
	status, err := service.CheckSegmentOwnership(ctx, SegmentsDeleteRequestDTO{BookmarkID: bookmark, Container: "media", Name: "proven.slo"})
	if err != nil || !status.Proven {
		t.Fatalf("status = %+v %v, want proven", status, err)
	}
	// Proven, but no force → refused; the manifest survives.
	if _, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "proven.slo", Confirm: "proven.slo",
	}); err == nil || !strings.Contains(err.Error(), "Backstage force") {
		t.Fatalf("proven-without-force = %v, want force-required refusal", err)
	}
	session := h.transfer.connections.sessions[bookmark].session
	if _, err := session.ObjectDetail(ctx, "media", "proven.slo"); err != nil {
		t.Fatal("proven-without-force refusal still deleted the manifest")
	}
}
