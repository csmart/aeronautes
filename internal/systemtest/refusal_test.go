package systemtest

import (
	"context"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestDestructiveRefusalsHaveNoEffect proves the refusal twins: when a
// destructive action is refused (wrong confirmation, overlapping scopes,
// segment deletion without the Backstage force), it changes NOTHING on
// the account. A refusal that half-acted would be worse than no guard.
// The whole namespace — the target container AND the segment container a
// refused segment-delete would have touched — is witnessed before and
// after, so a half-applied segment delete cannot hide (grok war-room).
func TestDestructiveRefusalsHaveNoEffect(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "refuse")
	h.Seed(h.Primary, c, "keep-a.txt", []byte("a"), nil)
	h.Seed(h.Primary, c, "keep-b.txt", []byte("b"), nil)

	// A real DLO whose segments live in a separate container — the target
	// of refusal #3. Seed it BEFORE the baseline so the segment container
	// is inside the negative-space snapshot.
	segs := h.Container(h.Primary, "refuse-segs")
	h.Seed(h.Primary, segs, "g/0", []byte("segment"), nil)
	h.Seed(h.Primary, c, "big.bin", nil, map[string]string{"X-Object-Manifest": segs + "/g/"})

	before := h.SnapshotNamespace(h.Primary)

	// 1. Delete with the WRONG typed confirmation is refused.
	if _, err := h.Delete.StartDelete(ctx, app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Mode: app.ModeEmpty, Confirm: "not-the-container",
	}); err == nil {
		t.Fatal("empty-container delete accepted a wrong confirmation")
	}
	RecordScenario(t, "DeleteService.StartDelete", "refusal")

	// 2. Copy with overlapping same-account nested scopes is refused at
	// start (before any write).
	if _, err := h.Transfer.StartCopy(ctx, app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: c, SourcePrefix: "x/y/",
		DestBookmarkID: h.Primary.BookmarkID, DestContainer: c, DestPrefix: "x/",
	}); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlapping copy = %v, want overlap refusal", err)
	}
	RecordScenario(t, "TransferService.StartCopy", "refusal")

	// 3. Segment deletion WITHOUT the Backstage force is refused, even
	// though a DLO exists (evidence is advisory; force is mandatory).
	if _, err := h.Delete.DeleteManifestWithSegments(ctx, app.SegmentsDeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Name: "big.bin", Confirm: "big.bin",
	}); err == nil || !strings.Contains(err.Error(), "Backstage force") {
		t.Fatalf("segment delete without force = %v, want force-required refusal", err)
	}
	RecordScenario(t, "DeleteService.DeleteManifestWithSegments", "refusal")

	after := h.SnapshotNamespace(h.Primary)
	// Every refusal was total: NOTHING moved anywhere in the namespace —
	// not keep-a/keep-b, not big.bin's manifest, not a single segment in
	// the refuse-segs container, not a container's config.
	h.AssertNoCollateral(before, after)
}
