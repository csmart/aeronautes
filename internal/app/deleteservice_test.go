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
	service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner)
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
