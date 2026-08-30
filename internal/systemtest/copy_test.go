package systemtest

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestCopyCrossAccountPreservesLogicalContent is the copy matrix's
// cornerstone: a cross-account copy of plain, DLO, and SLO objects lands
// byte-for-byte-identical LOGICAL content on the destination, leaves the
// source account completely untouched, and touches nothing else on the
// destination (no sidecar/segment container, no staging residue). The
// oracle compares logical GET digests, so re-chunking by the engine is
// irrelevant — only the readable bytes must match.
func TestCopyCrossAccountPreservesLogicalContent(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	srcC := h.Container(h.Primary, "copysrc")
	srcSegs := h.Container(h.Primary, "copysrc-segs")
	dstC := h.Container(h.Secondary, "copydst")

	// A plain object, a DLO (two segments), an SLO (one leaf), and a
	// sentinel on the source.
	plain := []byte("cross-account plain payload")
	h.Seed(h.Primary, srcC, "plain.txt", plain, map[string]string{"Content-Type": "text/plain", "X-Object-Meta-Team": "ops"})
	segA, segB := bytes.Repeat([]byte("X"), 1024), bytes.Repeat([]byte("Y"), 512)
	h.Seed(h.Primary, srcSegs, "dlo/0", segA, nil)
	h.Seed(h.Primary, srcSegs, "dlo/1", segB, nil)
	h.Seed(h.Primary, srcC, "big.bin", nil, map[string]string{"X-Object-Manifest": srcSegs + "/dlo/"})
	leaf := bytes.Repeat([]byte("S"), 2048)
	h.Seed(h.Primary, srcSegs, "slo/leaf", leaf, nil)
	h.SeedSLO(h.Primary, srcC, "manifest.slo", fmt.Sprintf(`[{"path":"/%s/slo/leaf","size_bytes":2048}]`, srcSegs))
	h.Seed(h.Primary, srcC, "sentinel.txt", []byte("must not change"), nil)

	srcBefore := h.Snapshot(h.Primary, srcC, srcSegs)
	// Witness the WHOLE destination namespace — dstC and any sidecar the
	// engine might create — before and after.
	dstBefore := h.SnapshotNamespace(h.Secondary)

	rec := h.StartCopy(ctx, app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: srcC,
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dstC,
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
		t.Fatalf("copy job = %s: %s", job.State, job.Error)
	}

	srcAfter := h.Snapshot(h.Primary, srcC, srcSegs)
	dstAfter := h.SnapshotNamespace(h.Secondary)

	// Destination LOGICAL content matches the source exactly. This asserts
	// readable-bytes identity (class-independent): a DLO/SLO whose engine
	// reassembles to a plain object on the destination still passes. It
	// does NOT assert class preservation (DLO-copied-as-DLO) or ordered
	// per-segment/metadata fidelity — that stronger contract is deferred
	// verification work (codex W-07/W-12), not claimed here (grok #3).
	copied := []string{"plain.txt", "big.bin", "manifest.slo", "sentinel.txt"}
	sb, db := srcBefore.Containers[srcC].Objects, dstAfter.Containers[dstC].Objects
	for _, name := range copied {
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
	// The destination namespace gained ONLY the copied names — no sidecar
	// container, no segment leak, no staging residue anywhere.
	allowed := make([]string, len(copied))
	for i, name := range copied {
		allowed[i] = dstC + "/" + name
	}
	h.AssertNoCollateral(dstBefore, dstAfter, allowed...)
	rec.CreditHappy()
}
