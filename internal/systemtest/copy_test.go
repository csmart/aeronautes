package systemtest

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"maps"
	"net/url"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
)

// TestCopyCrossAccountPreservesLogicalContent is the copy matrix's
// cornerstone: a cross-account copy of plain, DLO, and SLO objects lands
// byte-for-byte-identical LOGICAL content on the destination, each object at
// its own name (ADR 0122 decision 4), leaves the source account completely
// untouched, and touches nothing else on the destination: no sidecar or
// segment container, and no name of the application's own invention. The
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
	h.Seed(h.Primary, srcC, "line\rbreak", []byte("native carriage return"), nil)
	segA, segB := bytes.Repeat([]byte("X"), 1024), bytes.Repeat([]byte("Y"), 512)
	h.Seed(h.Primary, srcSegs, "dlo/0", segA, nil)
	h.Seed(h.Primary, srcSegs, "dlo/1", segB, nil)
	h.Seed(h.Primary, srcC, "big.bin", nil, map[string]string{"X-Object-Manifest": srcSegs + "/dlo/"})
	leaf := bytes.Repeat([]byte("S"), 2048)
	h.Seed(h.Primary, srcSegs, "slo/leaf", leaf, nil)
	h.SeedSLO(h.Primary, srcC, "manifest.slo", fmt.Sprintf(`[{"path":"/%s/slo/leaf","size_bytes":2048}]`, srcSegs))
	h.Seed(h.Primary, srcC, "sentinel.txt", []byte("must not change"), nil)
	// A name under .aeronautes/stage/ is an ordinary name: one an earlier
	// build left behind is copied like any other.
	h.Seed(h.Primary, srcC, ".aeronautes/stage/earlier-job/left.txt", []byte("left by an earlier build"), nil)

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
	copied := []string{"plain.txt", "line\rbreak", "big.bin", "manifest.slo", "sentinel.txt", ".aeronautes/stage/earlier-job/left.txt"}
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
	// container, no segment leak, and nothing written under any other name.
	allowed := make([]string, len(copied))
	for i, name := range copied {
		allowed[i] = dstC + "/" + name
	}
	h.AssertNoCollateral(dstBefore, dstAfter, allowed...)
	rec.CreditHappy()
}

// TestCopyUpToTheContainerTop is the 0.5.0 test run's case: a copy from a
// marker-backed folder up to the top of its own container, through the
// engine, lands each object at the top with the source's bytes, leaves the
// source folder and its marker as they were, and adds nothing else, the
// container included (DESIGN §65.9 item 9, as amended).
func TestCopyUpToTheContainerTop(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "copyup")
	// The folder's marker, as a folder made in the application has, stays
	// in files/: its destination name at the top would be empty, the
	// container itself.
	h.Seed(h.Primary, c, "files/", nil, map[string]string{"Content-Type": "application/directory"})
	h.Seed(h.Primary, c, "files/one.txt", []byte("first object"), nil)
	h.Seed(h.Primary, c, "files/two.txt", []byte("second"), nil)
	before := h.SnapshotNamespace(h.Primary)

	rec := h.StartCopy(ctx, app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: c, SourcePrefix: "files/",
		DestBookmarkID: h.Primary.BookmarkID, DestContainer: c,
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
		t.Fatalf("copy job = %s: %s", job.State, job.Error)
	}

	after := h.SnapshotNamespace(h.Primary)
	src, dst := before.Containers[c].Objects, after.Containers[c].Objects
	for _, name := range []string{"one.txt", "two.txt"} {
		if dst[name].Digest != src["files/"+name].Digest || dst[name].Size != src["files/"+name].Size {
			t.Fatalf("%s: digest %s (%d), want the source's %s (%d)", name, dst[name].Digest, dst[name].Size, src["files/"+name].Digest, src["files/"+name].Size)
		}
		if dst["files/"+name].Digest != src["files/"+name].Digest {
			t.Fatalf("source files/%s changed during the copy", name)
		}
	}
	h.AssertNoCollateral(before, after, c+"/one.txt", c+"/two.txt")
	rec.CreditHappy()
}

// copyCarriesHeadersAndLinks holds what a copied object carries (ADR 0122
// decision 4). The engine copies the content type and a modification time
// and no other header, and copies a link as its target's bytes. So the
// application gives an object the engine wrote the source's user metadata,
// content encoding, content disposition and expiry in one request, sends
// none for an object that has only what the engine delivers, and carries a
// link itself, as a link. The stored bytes are never decoded on the way:
// an object stored with a content encoding arrives as the same bytes.
//
// The copy stays in one account so that the copied link still resolves: the
// witness cannot describe a link whose target is missing.
func copyCarriesHeadersAndLinks(t *testing.T, h *Harness) {
	t.Helper()
	src, dst := h.Container(h.Primary, "carried-src"), h.Container(h.Primary, "carried-dst")
	var zipped bytes.Buffer
	writer := gzip.NewWriter(&zipped)
	if _, err := writer.Write(bytes.Repeat([]byte("stored as gzip "), 64)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	expires := fmt.Sprint(time.Now().Add(time.Hour).Unix())
	h.Seed(h.Primary, src, "from/tagged.txt", zipped.Bytes(), map[string]string{
		"Content-Type": "text/plain", "Content-Encoding": "gzip", "Content-Disposition": `attachment; filename="report.txt"`,
		"X-Object-Meta-Team": "ops", "X-Object-Meta-Colour": "blue", "X-Delete-At": expires})
	// Bytes that claim an encoding they are not in, and no bytes at all:
	// either would break an engine that decoded what it carried.
	h.Seed(h.Primary, src, "from/broken.gz", []byte("not a gzip stream"), map[string]string{"Content-Encoding": "gzip", "X-Object-Meta-Team": "ops"})
	h.Seed(h.Primary, src, "from/empty.gz", nil, map[string]string{"Content-Encoding": "gzip", "X-Object-Meta-Team": "ops"})
	h.Seed(h.Primary, src, "from/plain.txt", []byte("only what the engine delivers"), map[string]string{"Content-Type": "text/plain"})
	h.Seed(h.Primary, src, "from/link", nil, map[string]string{"X-Symlink-Target": src + "/from/tagged.txt", "X-Object-Meta-Kind": "pointer"})
	names := []string{"tagged.txt", "broken.gz", "empty.gz", "plain.txt", "link"}
	before := h.SnapshotNamespace(h.Primary)
	source := before.Containers[src].Objects
	if h.MockPrimary == nil {
		// The in-process store keeps neither of these two headers, so only
		// the owned Swift can show that the fixture has them to carry.
		if tagged := source["from/tagged.txt"]; tagged.ContentEncoding != "gzip" || tagged.ContentDisposition == "" {
			t.Fatalf("the source was not seeded with its encoding and disposition: %+v", tagged)
		}
	}
	if tagged := source["from/tagged.txt"]; tagged.DeleteAt == "" || tagged.Meta["Team"] != "ops" || source["from/link"].Class != "symlink" {
		t.Fatalf("the source fixture is not what the case needs: %+v %+v", tagged, source["from/link"])
	}
	trace := copyCompletionProxy(t, h, h.Primary, "")

	rec := h.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "from/",
		DestBookmarkID: h.Primary.BookmarkID, DestContainer: dst, DestPrefix: "to/",
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	job := h.AwaitJob(rec.JobID())
	if job.State != "complete" || job.Counters.ObjectsCopied != int64(len(names)) || job.Counters.ObjectsFailed != 0 {
		t.Fatalf("copy job = %s %+v: %s", job.State, job.Counters, job.Error)
	}
	copyCompletionAwaitPins(t, h, h.Primary)

	after := h.SnapshotNamespace(h.Primary)
	var allowed []string
	for _, name := range names {
		want, got := source["from/"+name], after.Containers[dst].Objects["to/"+name]
		allowed = append(allowed, dst+"/to/"+name)
		// A link is the pointer it holds: the witness reads its other
		// headers through it, from the target.
		if got.Class != want.Class || got.Digest != want.Digest || got.SymlinkTarget != want.SymlinkTarget || got.SymlinkAccount != want.SymlinkAccount {
			t.Errorf("to/%s arrived as %+v, want the source's %+v", name, got, want)
		}
		if want.Class != "symlink" && (got.Size != want.Size || got.ContentType != want.ContentType || got.ContentEncoding != want.ContentEncoding ||
			got.ContentDisposition != want.ContentDisposition || got.DeleteAt != want.DeleteAt) {
			t.Errorf("to/%s arrived as %+v, want the source's %+v", name, got, want)
		}
		// The engine records a modification time on an object it writes;
		// every other key is the source's, and nothing else is added.
		meta := maps.Clone(got.Meta)
		if _, has := want.Meta["Mtime"]; !has {
			delete(meta, "Mtime")
		}
		if !maps.Equal(meta, want.Meta) && (len(meta) != 0 || len(want.Meta) != 0) {
			t.Errorf("to/%s carries metadata %v, want the source's %v", name, got.Meta, want.Meta)
		}
	}
	h.AssertNoCollateral(before, after, allowed...)

	// One metadata request for an object that has more than the engine
	// delivers, none for one that has not, and the link written once by the
	// application.
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	sent := map[string]int{}
	for _, write := range trace.writeSnapshot() {
		sent[write]++
	}
	at := func(method, name string) int { return sent[method+" "+storage.Path+"/"+dst+"/to/"+name] }
	if at("POST", "tagged.txt") != 1 || at("POST", "plain.txt") != 0 || at("POST", "link") != 0 || at("PUT", "link") != 1 {
		t.Errorf("the destination was sent %v", sent)
	}
}

func TestCopyCarriesHeadersAndLinksMock(t *testing.T) {
	copyCarriesHeadersAndLinks(t, findMockHarness(t))
}

func TestCopyCarriesHeadersAndLinksRealSwift(t *testing.T) {
	copyCarriesHeadersAndLinks(t, New(t))
}
