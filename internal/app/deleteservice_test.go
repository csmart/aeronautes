package app

import (
	"context"
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
	d, err := h.transfer.connections.sessions[bookmark].ObjectDetail(ctx, "media", "sibling")
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
	session := h.transfer.connections.sessions[bookmark]
	// Container config that must survive Empty (§33): empty base
	// fingerprint = unconditional apply in the mutate flow.
	if _, err := h.mutate.UpdateContainer(ctx, "media", "", ContainerEditDTO{
		Meta:    map[string]string{"Team": "ops"},
		ReadACL: ".r:*",
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
	events, _ := h.auditSvc.Recent(ctx, 20)
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
	if err == nil || !strings.Contains(err.Error(), "not proven") {
		t.Fatalf("unproven delete = %v", err)
	}
	session := h.transfer.connections.sessions[bookmark]
	if _, err := session.ObjectDetail(ctx, "media", "theirs.bin"); err != nil {
		t.Fatal("refusal deleted the manifest anyway")
	}

	// Wrong confirmation refused even for the owned one.
	if _, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "ours.bin", Confirm: "ours",
	}); err == nil {
		t.Fatal("bad confirmation accepted")
	}

	// Owned: manifest + exactly its prefix die; the external prefix stays.
	result, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "ours.bin", Confirm: "ours.bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ManifestDeleted || result.SegmentsDeleted != 3 || result.Basis != "exclusive-ownership" {
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

	// Native manifest+segments for the proven one.
	result, err := service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "big.slo", Confirm: "big.slo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ManifestDeleted || result.SegmentsDeleted != 2 {
		t.Fatalf("result = %+v", result)
	}
	session := h.transfer.connections.sessions[bookmark]
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

	// A source big enough for the chunking engine: the copy commits a
	// destination DLO whose prefix becomes an ownership proof.
	h.installChunkingEngine(nil, 1024)
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "video.bin", strings.Repeat("v", 5000), nil)
	// Overwrite path: an existing destination object forces
	// stage-and-commit, whose committed prefix is the recorded proof.
	dstMock.AddContainer("backup", "standard")
	seedObject(dstMock, "backup", "video.bin", "old tiny version bytes!!", nil)

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

	// The copied DLO is provably ours: segments-delete proceeds without
	// force and sweeps exactly the owned prefix.
	status, err := deleteSvc.CheckSegmentOwnership(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: dstBookmark, Container: "backup", Name: "video.bin",
	})
	if err != nil || !status.Proven || status.Class != "dlo" {
		t.Fatalf("status = %+v %v", status, err)
	}
	result, err := deleteSvc.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: dstBookmark, Container: "backup", Name: "video.bin", Confirm: "video.bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ManifestDeleted || result.SegmentsDeleted != 5 || result.Basis != "exclusive-ownership" {
		t.Fatalf("cleanup = %+v", result)
	}
}
