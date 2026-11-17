package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
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
	// ADR 0095 decision 7 with ADR 0122 decision 6: the cost is stated
	// before the job, one request for each member.
	if preview.Strategy != "per-name" || preview.RequestCost != 3 {
		t.Errorf("strategy and cost = %+v", preview)
	}
	// ADR 0102 decision 3: nobody archives into this container, and the scan
	// covered the account, so the dialog has nothing to add.
	if len(preview.ArchiveOf) != 0 || preview.ArchiveUnestablished != "" {
		t.Errorf("an unrelated container reads as an archive: %+v", preview)
	}
	// The container another container archives into is named as its owner's.
	h.mock.AddContainer("owner", "standard")
	h.mock.EnableVersionArchive("owner", "media", true)
	archive, err := service.PreviewDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Mode: ModeEmpty})
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.ArchiveOf) != 1 || archive.ArchiveOf[0] != "owner" || archive.ArchiveUnestablished != "" {
		t.Errorf("the owner was not named: %+v", archive)
	}
	// An owner's name is stored data on a consent sentence: a line separator
	// is replaced and a long name is cut, as the overwrite question does.
	long := "owner-" + strings.Repeat("x", 120)
	h.mock.AddContainer("own er", "standard")
	h.mock.AddContainer(long, "standard")
	h.mock.EnableVersionArchive("own er", "media", false)
	h.mock.EnableVersionArchive(long, "media", false)
	shown, err := service.PreviewDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Mode: ModeEmpty})
	if err != nil {
		t.Fatal(err)
	}
	// Listing order: the separator's bytes sort after the ASCII names.
	if len(shown.ArchiveOf) != 3 || shown.ArchiveOf[0] != "owner" || shown.ArchiveOf[1] != "owner-"+strings.Repeat("x", 74)+"…" ||
		shown.ArchiveOf[2] != "own?er" {
		t.Errorf("the owners were not sanitised and bounded for display: %q", shown.ArchiveOf)
	}

	// Wrong typed confirmation refuses before any job exists.
	_, err = service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "meda",
	})
	if err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Errorf("bad confirmation accepted: %v", err)
	}
	// A prefix is armed by its scope, so the container name alone, which
	// would not show which prefix was read, refuses (ADR 0095 decision 7).
	_, err = service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "media",
	})
	if err == nil || !strings.Contains(err.Error(), `does not match "delete media/scope/"`) {
		t.Errorf("the container name armed a prefix deletion: %v", err)
	}
	// Every deletion is typed with its verb, so the scope alone, the words
	// before 17 November 2026, no longer arms it (ADR 0093 decision 9).
	_, err = service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "media/scope/",
	})
	if err == nil || !strings.Contains(err.Error(), `does not match "delete media/scope/"`) {
		t.Errorf("the scope without the verb armed a prefix deletion: %v", err)
	}
	// Each container job is armed by its own verb and the name, so the bare
	// name starts neither, the emptying's words never delete the container,
	// and the deletion's words never empty it (ADR 0121 decision 3).
	for name, refused := range map[string]struct {
		mode          DeleteMode
		typed, wanted string
	}{
		"the name for a deletion":              {ModeDeleteContainer, "media", "delete media"},
		"the emptying's words for a deletion":  {ModeDeleteContainer, "empty media", "delete media"},
		"the name for an emptying":             {ModeEmpty, "media", "empty media"},
		"the deletion's words for an emptying": {ModeEmpty, "delete media", "empty media"},
	} {
		_, err = service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Mode: refused.mode, Confirm: refused.typed})
		if err == nil || !strings.Contains(err.Error(), `does not match "`+refused.wanted+`"`) {
			t.Errorf("%s armed the job: %v", name, err)
		}
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
	if after, err := service.PreviewDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Mode: ModeEmpty}); err != nil || after.Objects == 0 {
		t.Errorf("a refused start deleted something: the container now holds %+v, %v", after, err)
	} else if before, err := service.PreviewDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix}); err != nil || before.Objects != preview.Objects {
		t.Errorf("a refused start deleted something: the prefix now holds %+v, %v, and held %d objects", before, err, preview.Objects)
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
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "delete media/scope/",
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
	if !strings.Contains(job.Settings, `"strategy":"per-name"`) {
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
	if _, err := h.mutate.UpdateContainer(ctx, h.portrait(), "media", "", ContainerEditDTO{
		Meta:      map[string]string{"Team": "ops"},
		ReadACL:   ".r:*",
		AckPublic: true,
	}); err != nil {
		t.Fatal(err)
	}

	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Mode: ModeEmpty, Confirm: "empty media",
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
		BookmarkID: bookmark, Container: "media", Mode: ModeDeleteContainer, Confirm: "delete media",
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
	events, err := h.auditSvc.RecentFor(ctx, h.bookmarkID(), 20)
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

// ADR 0122 decision 6: the store's answer to the container's DELETE is the
// result. A store whose replicas lag goes on answering for the container
// after accepting its DELETE, however long: the job is complete, and the
// container is not looked for.
func TestDeleteContainerTakesTheStoresAnswer(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "one.txt", "1", nil)
	h.mock.LagDeletedContainers(1000)
	jobID, err := service.StartDelete(context.Background(), DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Mode: ModeDeleteContainer, Confirm: "delete media",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Error != "" || job.Counters.ObjectsDeleted != 1 {
		t.Fatalf("job = %+v, want complete with one object deleted", job)
	}
	wire := h.mock.RequestLog()
	deleted := false
	for _, line := range wire {
		switch {
		case line == "DELETE /v1/AUTH_test/media":
			deleted = true
		case deleted && strings.Contains(line, "/v1/AUTH_test/media"):
			t.Fatalf("the container was asked for after its DELETE: %s", line)
		}
	}
	if !deleted {
		t.Fatal("the container's DELETE was not sent")
	}
}

// A container that still holds something is refused by the store, which the
// job reports as that: here a name written after the last listing.
func TestDeleteContainerReportsTheStoresRefusalOfANonEmptyContainer(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "one.txt", "1", nil)
	// The writer lands after the sweep's last listing, as the container's
	// own DELETE arrives.
	h.mock.BeforeRequest = func(method, path string) {
		if method == "DELETE" && path == "/v1/AUTH_test/media" {
			seedObject(h.mock, "media", "late.txt", "late", nil)
		}
	}
	jobID, err := service.StartDelete(context.Background(), DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Mode: ModeDeleteContainer, Confirm: "delete media",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	h.mock.BeforeRequest = nil
	if job.State != "partial" || !strings.Contains(job.Error, "the store refused to delete the container because it is not empty") || job.Counters.ObjectsDeleted != 1 {
		t.Fatalf("job = %+v, want partial with the store's refusal", job)
	}
	if !stillReadable(t, h, "media", "late.txt") {
		t.Fatal("the late object is gone")
	}
}

// A container another writer deleted as its DELETE arrived is already gone,
// which is the outcome asked for: the job is complete.
func TestDeleteContainerTakesNotFoundAsAlreadyGone(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "one.txt", "1", nil)
	h.mock.BeforeRequest = func(method, path string) {
		if method == "DELETE" && path == "/v1/AUTH_test/media" {
			h.mock.RemoveContainer("media")
		}
	}
	jobID, err := service.StartDelete(context.Background(), DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Mode: ModeDeleteContainer, Confirm: "delete media",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	h.mock.BeforeRequest = nil
	if job.State != "complete" || job.Error != "" {
		t.Fatalf("job = %+v, want complete", job)
	}
}

// The other answers to a container's DELETE leave the job partial, and say
// which they were: a refusal with the store's status, or no answer.
func TestContainerDeleteLeftSaysWhatTheStoreAnswered(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"forbidden", &swiftcontrol.SwiftError{Kind: swiftcontrol.KindForbidden, HTTPStatus: 403}, "refused to delete the container with HTTP 403"},
		{"server error", &swiftcontrol.SwiftError{Kind: swiftcontrol.KindServer, HTTPStatus: 503}, "nothing says whether the container was deleted"},
		{"no answer", &swiftcontrol.SwiftError{Kind: swiftcontrol.KindNetwork}, "nothing says whether the container was deleted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := containerDeleteLeft(tc.err); !strings.Contains(got, tc.want) {
				t.Fatalf("left = %q, want it to say %q", got, tc.want)
			}
		})
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
	jobID, err = deleteSvc.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
		BookmarkID: dstBookmark, Container: "backup", Name: "video.bin", Confirm: "video.bin", Force: true,
		Reviewed: status.Reviewed,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Type != "delete_segments" {
		t.Fatalf("cleanup job = %+v", job)
	}
	// The manifest and its five recorded segments are gone.
	dstSession := h.transfer.connections.sessions[dstBookmark].session
	if _, err := dstSession.ObjectDetail(ctx, "backup", "video.bin"); err == nil {
		t.Fatal("the manifest survived the cleanup")
	}
	if totals, err := h.jobsSvc.ItemTotals(ctx, dstBookmark, jobID); err != nil || totals.Deleted != 6 {
		t.Fatalf("cleanup totals = %+v %v (manifest + five segments)", totals, err)
	}
}

func TestAuditSearch(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "scope/x.txt", "x", nil)

	// Generate a mixed trail: a delete job plus a refused segments call.
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "delete media/scope/",
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
		BookmarkID: bookmark, Container: "media", Prefix: "doomed/", Mode: ModePrefix, Confirm: "delete media/doomed/",
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
