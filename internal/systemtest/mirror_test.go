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

	// Source: A (present on both, identical), B (missing on dst).
	h.Seed(h.Primary, src, "A.txt", []byte("shared identical"), map[string]string{"X-Object-Meta-K": "a"})
	h.Seed(h.Primary, src, "B.txt", []byte("only on source"), nil)
	// Destination: A (identical), plus dest-only C and D that mirror
	// must delete.
	h.Seed(h.Secondary, dst, "A.txt", []byte("shared identical"), map[string]string{"X-Object-Meta-K": "a"})
	h.Seed(h.Secondary, dst, "C.txt", []byte("dest-only, doomed"), nil)
	h.Seed(h.Secondary, dst, "D.txt", []byte("dest-only, doomed too"), nil)

	srcBefore := h.Snapshot(h.Primary, src)
	dstBefore := h.Snapshot(h.Secondary, dst)

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
	dstAfter := h.Snapshot(h.Secondary, dst)

	// Destination now equals the source set exactly: {A, B}, nothing else.
	dst2 := dstAfter.Containers[dst].Objects
	if len(dst2) != 2 || dst2["A.txt"].Digest == "" || dst2["B.txt"].Digest == "" {
		names := []string{}
		for n := range dst2 {
			names = append(names, n)
		}
		t.Fatalf("destination did not converge to {A,B}: %v", names)
	}
	// B was copied with source-identical content.
	if dst2["B.txt"].Digest != srcAfter.Containers[src].Objects["B.txt"].Digest {
		t.Fatal("mirrored B.txt content differs from source")
	}
	// The SOURCE is completely unchanged.
	if d := diffOf(srcBefore, srcAfter); !d.Empty() {
		t.Fatalf("source changed during a mirror:\n%s", d)
	}
	// The delete half removed EXACTLY the dest-only C and D — the
	// accidental-deletion guard.
	h.AssertNoCollateral(dstBefore, dstAfter, dst+"/B.txt", dst+"/C.txt", dst+"/D.txt")
}
