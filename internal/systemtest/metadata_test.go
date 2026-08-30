package systemtest

import (
	"context"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestObjectMetadataEditIsSurgical is the first war-room matrix row: an
// object metadata edit must change EXACTLY the edited object and leave
// every sentinel byte-for-byte identical (the negative-space invariant).
func TestObjectMetadataEditIsSurgical(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "meta")
	h.Seed(h.Primary, c, "target.txt", []byte("payload"), map[string]string{"Content-Type": "text/plain", "X-Object-Meta-Team": "ops"})
	h.Seed(h.Primary, c, "sentinel.txt", []byte("do not touch"), map[string]string{"Content-Type": "text/plain", "X-Object-Meta-Keep": "yes"})

	before := h.Snapshot(h.Primary, c)

	// Edit target's metadata through the real service (active = primary).
	result, err := h.UpdateObjectMetadata(ctx, c, "target.txt", "", app.ObjectMetadataEditDTO{
		Meta: map[string]string{"Team": "migrations", "Wave": "5"}, ContentType: "text/plain",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "applied" {
		t.Fatalf("outcome = %s", result.Outcome)
	}

	after := h.Snapshot(h.Primary, c)

	// The target's metadata is exactly the new set; content is untouched.
	tgt := after.Containers[c].Objects["target.txt"]
	if tgt.Meta["Team"] != "migrations" || tgt.Meta["Wave"] != "5" || tgt.Meta["Keep"] != "" {
		t.Fatalf("target metadata after edit = %+v", tgt.Meta)
	}
	if b := before.Containers[c].Objects["target.txt"]; tgt.Digest != b.Digest || tgt.Size != b.Size {
		t.Fatalf("metadata edit changed the CONTENT: %s->%s", b.Digest, tgt.Digest)
	}
	// The sentinel is byte-for-byte and metadata-for-metadata identical.
	if sb, sa := before.Containers[c].Objects["sentinel.txt"], after.Containers[c].Objects["sentinel.txt"]; sa.Digest != sb.Digest || sa.Meta["Keep"] != "yes" {
		t.Fatalf("sentinel changed: %+v -> %+v", sb, sa)
	}
	// Nothing outside target.txt moved anywhere on the account.
	h.AssertNoCollateral(before, after, c+"/target.txt")
	h.RecordScenario("MutateService.UpdateObjectMetadata", "happy")

	// Optimistic-concurrency refusal: an edit built on a STALE fingerprint
	// is refused as a conflict, and a refused edit changes NOTHING (codex
	// war-room H-07 — the earlier empty base fingerprint never exercised
	// this path).
	conflictBefore := h.Snapshot(h.Primary, c)
	stale, err := h.UpdateObjectMetadata(ctx, c, "target.txt", "stale-fingerprint-that-cannot-match", app.ObjectMetadataEditDTO{
		Meta: map[string]string{"Team": "must-not-apply"}, ContentType: "text/plain",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Outcome != "conflict" {
		t.Fatalf("stale-fingerprint edit outcome = %q, want conflict", stale.Outcome)
	}
	conflictAfter := h.Snapshot(h.Primary, c)
	if d := diffOf(conflictBefore, conflictAfter); !d.Empty() {
		t.Fatalf("a refused (conflict) metadata edit still changed state:\n%s", d)
	}
	h.RecordScenario("MutateService.UpdateObjectMetadata", "conflict")
}
