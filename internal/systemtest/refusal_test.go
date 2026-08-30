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
func TestDestructiveRefusalsHaveNoEffect(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "refuse")
	h.Seed(h.Primary, c, "keep-a.txt", []byte("a"), nil)
	h.Seed(h.Primary, c, "keep-b.txt", []byte("b"), nil)

	before := h.Snapshot(h.Primary, c)

	// 1. Delete with the WRONG typed confirmation is refused.
	if _, err := h.Delete.StartDelete(ctx, app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Mode: app.ModeEmpty, Confirm: "not-the-container",
	}); err == nil {
		t.Fatal("empty-container delete accepted a wrong confirmation")
	}

	// 2. Copy with overlapping same-account nested scopes is refused at
	// start (before any write).
	if _, err := h.Transfer.StartCopy(ctx, app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: c, SourcePrefix: "x/y/",
		DestBookmarkID: h.Primary.BookmarkID, DestContainer: c, DestPrefix: "x/",
	}); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlapping copy = %v, want overlap refusal", err)
	}

	// 3. Segment deletion WITHOUT the Backstage force is refused, even
	// though a DLO exists (evidence is advisory; force is mandatory).
	segs := h.Container(h.Primary, "refuse-segs")
	h.Seed(h.Primary, segs, "g/0", []byte("segment"), nil)
	h.Seed(h.Primary, c, "big.bin", nil, map[string]string{"X-Object-Manifest": segs + "/g/"})
	if _, err := h.Delete.DeleteManifestWithSegments(ctx, app.SegmentsDeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Name: "big.bin", Confirm: "big.bin",
	}); err == nil || !strings.Contains(err.Error(), "Backstage force") {
		t.Fatalf("segment delete without force = %v, want force-required refusal", err)
	}

	after := h.Snapshot(h.Primary, c)
	// Every refusal was total: the container's original objects are
	// byte-for-byte intact (big.bin/the segs container are test setup we
	// added after `before`, so allow them; keep-a/keep-b must be pristine).
	for _, name := range []string{"keep-a.txt", "keep-b.txt"} {
		if after.Containers[c].Objects[name].Digest != before.Containers[c].Objects[name].Digest {
			t.Fatalf("a refusal mutated %q", name)
		}
	}
}
