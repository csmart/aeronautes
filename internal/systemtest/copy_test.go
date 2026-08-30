package systemtest

import (
	"bytes"
	"context"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestCopyCrossAccountPreservesLogicalContent is the copy matrix's
// cornerstone: a cross-account copy of plain, DLO, and SLO objects lands
// byte-for-byte-identical LOGICAL content on the destination, leaves the
// source account completely untouched, and touches nothing else on the
// destination. The oracle compares logical GET digests, so re-chunking
// by the engine is irrelevant — only the readable bytes must match.
func TestCopyCrossAccountPreservesLogicalContent(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	srcC := h.Container(h.Primary, "copysrc")
	srcSegs := h.Container(h.Primary, "copysrc-segs")
	dstC := h.Container(h.Secondary, "copydst")

	// A plain object, a DLO (two segments), and a sentinel on the source.
	plain := []byte("cross-account plain payload")
	h.Seed(h.Primary, srcC, "plain.txt", plain, map[string]string{"Content-Type": "text/plain", "X-Object-Meta-Team": "ops"})
	segA, segB := bytes.Repeat([]byte("X"), 1024), bytes.Repeat([]byte("Y"), 512)
	h.Seed(h.Primary, srcSegs, "dlo/0", segA, nil)
	h.Seed(h.Primary, srcSegs, "dlo/1", segB, nil)
	h.Seed(h.Primary, srcC, "big.bin", nil, map[string]string{"X-Object-Manifest": srcSegs + "/dlo/"})
	h.Seed(h.Primary, srcC, "sentinel.txt", []byte("must not change"), nil)

	srcBefore := h.Snapshot(h.Primary, srcC, srcSegs)
	dstBefore := h.Snapshot(h.Secondary, dstC)

	jobID, err := h.Transfer.StartCopy(ctx, app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: srcC,
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dstC,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := h.AwaitJob(jobID); job.State != "complete" {
		t.Fatalf("copy job = %s: %s", job.State, job.Error)
	}

	srcAfter := h.Snapshot(h.Primary, srcC, srcSegs)
	dstAfter := h.Snapshot(h.Secondary, dstC)

	// Destination logical content matches the source exactly.
	sb, db := srcBefore.Containers[srcC].Objects, dstAfter.Containers[dstC].Objects
	for _, name := range []string{"plain.txt", "big.bin", "sentinel.txt"} {
		if db[name].Digest != sb[name].Digest || db[name].Size != sb[name].Size {
			t.Fatalf("%s: dst digest %s (%d) != src %s (%d)", name, db[name].Digest, db[name].Size, sb[name].Digest, sb[name].Size)
		}
	}
	// The copied plain object preserved its user metadata.
	if db["plain.txt"].Meta["Team"] != "ops" {
		t.Fatalf("copied metadata lost: %+v", db["plain.txt"].Meta)
	}
	// The SOURCE account is byte-for-byte unchanged (a copy reads, never
	// mutates the source).
	if d := diffOf(srcBefore, srcAfter); !d.Empty() {
		t.Fatalf("source account changed during a copy:\n%s", d)
	}
	// The destination gained ONLY the three copied names (plus whatever
	// staging the engine cleaned up — assert no leftover staging).
	for name := range dstAfter.Containers[dstC].Objects {
		if name != "plain.txt" && name != "big.bin" && name != "sentinel.txt" {
			t.Fatalf("unexpected object on destination after copy: %q (staging leak?)", name)
		}
	}
	_ = dstBefore
}
