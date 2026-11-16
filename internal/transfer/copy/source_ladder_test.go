package copy

import (
	"context"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// The ladder is the content expectation a download still compares against,
// and is tested here for what it reads; the routes that use it test what they
// do with it.

// A dynamic source's manifest header is percent-encoded and Swift decodes
// it, so its expectation is built from the decoded prefix's segments (F37);
// read raw, the listing would find none.
func TestDynamicSourceLadderReadsTheDecodedPrefix(t *testing.T) {
	w := newWorld(t)
	w.srcMock.AddContainer("media_segments", "standard")
	seed(w.srcMock, "media_segments", "my asset/src/000000", []byte("GOOD"), nil)
	seed(w.srcMock, "media_segments", "my asset/src/000001", []byte("DATA"), nil)
	w.srcMock.PutObject("media", "asset", swifttest.Object{Manifest: "media_segments/my%20asset/src/"})
	src, err := w.src.ObjectDetail(context.Background(), "media", "asset")
	if err != nil {
		t.Fatal(err)
	}
	ladder, err := newExecutor(t, w, nil).dynamicSourceLadder(context.Background(), src, 4)
	if err != nil || len(ladder) != 2 {
		t.Fatalf("ladder = %d runs, %v; want the two decoded segments", len(ladder), err)
	}
}

func TestSourceLadderCeilingRefusesRatherThanTruncating(t *testing.T) {
	// A source past the ceiling must refuse with that reason, not be
	// compared against an expectation that quietly stops early. The
	// production ceiling is a quarter of a million segments, which no
	// fixture can build, so the ceiling is driven directly.
	w := newWorld(t)
	w.srcMock.AddContainer("media_segments", "standard")
	seed(w.srcMock, "media_segments", "asset/src/00000000", []byte("GOOD"), nil)
	seed(w.srcMock, "media_segments", "asset/src/00000001", []byte("DATA"), nil)
	w.srcMock.PutObject("media", "asset", swifttest.Object{Manifest: "media_segments/asset/src/"})
	src, err := w.src.ObjectDetail(context.Background(), "media", "asset")
	if err != nil {
		t.Fatal(err)
	}
	e := newExecutor(t, w, nil)
	ladder, err := e.dynamicSourceLadder(context.Background(), src, 1)
	if err == nil {
		t.Fatalf("a source past the ceiling produced a ladder of %d runs", len(ladder))
	}
	if !strings.Contains(err.Error(), "beyond what one content comparison holds") {
		t.Fatalf("refusal does not name the ceiling: %v", err)
	}
	// The same source inside the ceiling still builds, so the refusal is
	// the ceiling and not the fixture.
	if ladder, err := e.dynamicSourceLadder(context.Background(), src, 2); err != nil || len(ladder) != 2 {
		t.Fatalf("ladder inside the ceiling = %d runs, err = %v", len(ladder), err)
	}
}
