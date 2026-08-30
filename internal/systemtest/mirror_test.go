package systemtest

import (
	"context"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestMirrorConvergesAndDeletesOnlyDestOnly is the mirror matrix's
// safety core: mirror makes the destination match the source — copying
// missing/changed names and deleting ONLY the destination-only ones —
// while the source stays untouched and nothing outside the intended set
// moves. The delete half is where accidental data loss would show, so
// the assertion is exact: exactly the dest-only names are removed.
func TestMirrorConvergesAndDeletesOnlyDestOnly(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	src := h.Container(h.Primary, "mirsrc")
	dst := h.Container(h.Secondary, "mirdst")

	// Source: A (present on both, identical), B (missing on dst), E
	// (present on both but with DIFFERENT content — the "changed" case).
	h.Seed(h.Primary, src, "A.txt", []byte("shared identical"), map[string]string{"X-Object-Meta-K": "a"})
	h.Seed(h.Primary, src, "B.txt", []byte("only on source"), nil)
	h.Seed(h.Primary, src, "E.txt", []byte("E: the CURRENT source content"), nil)
	// Destination: A (identical), a STALE E that mirror must overwrite,
	// plus dest-only C and D that mirror must delete.
	h.Seed(h.Secondary, dst, "A.txt", []byte("shared identical"), map[string]string{"X-Object-Meta-K": "a"})
	h.Seed(h.Secondary, dst, "E.txt", []byte("E: stale destination content"), nil)
	h.Seed(h.Secondary, dst, "C.txt", []byte("dest-only, doomed"), nil)
	h.Seed(h.Secondary, dst, "D.txt", []byte("dest-only, doomed too"), nil)

	srcBefore := h.Snapshot(h.Primary, src)
	// Witness the whole destination namespace so a sidecar/segment
	// container mirror might create is inside the negative space, not just
	// the target container (grok war-room: named-containers-only blind spot).
	dstBefore := h.SnapshotNamespace(h.Secondary)

	jobID, err := h.Transfer.StartMirror(ctx, app.MirrorRequestDTO{
		CopyRequestDTO: app.CopyRequestDTO{
			SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src,
			DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst,
		},
		Confirm: dst,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := h.AwaitJob(jobID); job.State != "complete" {
		t.Fatalf("mirror job = %s: %s", job.State, job.Error)
	}

	srcAfter := h.Snapshot(h.Primary, src)
	dstAfter := h.SnapshotNamespace(h.Secondary)

	// Destination now equals the source set exactly: {A, B, E}, nothing else.
	dst2, srcObjs := dstAfter.Containers[dst].Objects, srcAfter.Containers[src].Objects
	if len(dst2) != 3 || dst2["A.txt"].Digest == "" || dst2["B.txt"].Digest == "" || dst2["E.txt"].Digest == "" {
		names := []string{}
		for n := range dst2 {
			names = append(names, n)
		}
		t.Fatalf("destination did not converge to {A,B,E}: %v", names)
	}
	// B (missing) and E (changed) both now carry source-identical content.
	if dst2["B.txt"].Digest != srcObjs["B.txt"].Digest {
		t.Fatal("mirrored B.txt content differs from source")
	}
	if dst2["E.txt"].Digest != srcObjs["E.txt"].Digest {
		t.Fatal("mirror did not overwrite the stale E.txt with source content")
	}
	// The SOURCE is completely unchanged.
	if d := diffOf(srcBefore, srcAfter); !d.Empty() {
		t.Fatalf("source changed during a mirror:\n%s", d)
	}
	// The delete half removed EXACTLY the dest-only C and D, and the only
	// other moves are the added B and the overwritten E — the
	// accidental-deletion guard.
	h.AssertNoCollateral(dstBefore, dstAfter, dst+"/B.txt", dst+"/E.txt", dst+"/C.txt", dst+"/D.txt")
}
