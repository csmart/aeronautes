package app

import (
	"context"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

func slashFixture(h *harness, container string, names ...string) {
	h.mock.AddContainer(container, "")
	for _, name := range names {
		h.mock.PutObject(container, name, swifttest.Object{Bytes: 1, ContentType: "text/plain"})
	}
}

// slashProbes counts the listings that asked for the "/" prefix, the one
// extra request ADR 0116 adds.
func slashProbes(h *harness) int {
	n := 0
	for _, line := range h.mock.RequestLog() {
		if strings.HasPrefix(line, "GET ") && (strings.Contains(line, "prefix=%2F") || strings.Contains(line, "prefix=/")) {
			n++
		}
	}
	return n
}

func rowNames(page ObjectPageDTO) []string {
	names := make([]string, len(page.Rows))
	for i, row := range page.Rows {
		names[i] = row.Kind + ":" + row.Name
	}
	return names
}

// A store that leaves the "/" folder out of its top listing still shows it,
// in its sorted place, and opening it lists its names; a store that lists
// it is not asked again (ADR 0116).
func TestTopListingShowsTheSlashFolderAStoreLeavesOut(t *testing.T) {
	ctx := context.Background()
	for _, omits := range []bool{true, false} {
		h := newHarness(t)
		h.connect(t)
		h.mock.OmitsSlashFolder = omits
		slashFixture(h, "slashes", "/a.txt", "/b/c.txt", "plain.txt", "dir/d.txt")
		before := slashProbes(h)
		page, err := h.browse.Objects(ctx, h.connected, "slashes", "", "", 100)
		if err != nil {
			t.Fatal(err)
		}
		got := rowNames(page)
		want := []string{"prefix:/", "prefix:dir/", "object:plain.txt"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Fatalf("omits=%t: top rows = %v, want %v", omits, got, want)
		}
		probes := slashProbes(h) - before
		if (omits && probes != 1) || (!omits && probes != 0) {
			t.Fatalf("omits=%t: %d slash probes, want %d", omits, probes, map[bool]int{true: 1, false: 0}[omits])
		}
		inside, err := h.browse.Objects(ctx, h.connected, "slashes", "/", "", 100)
		if err != nil {
			t.Fatal(err)
		}
		if got := rowNames(inside); len(got) != 2 || got[0] != "object:/a.txt" || got[1] != "prefix:/b/" {
			t.Fatalf("omits=%t: inside / = %v", omits, got)
		}
	}
}

// No "/" names means no "/" folder, and a page that does not span where "/"
// sorts does not ask.
func TestTopListingAddsNoSlashFolderWhereNoneBelongs(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.connect(t)
	h.mock.OmitsSlashFolder = true
	slashFixture(h, "plain", "a.txt", "dir/d.txt")
	page, err := h.browse.Objects(ctx, h.connected, "plain", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowNames(page); len(got) != 2 {
		t.Fatalf("rows = %v, want no slash folder", got)
	}
	slashFixture(h, "paged", "/x.txt", "a.txt", "b.txt")
	before := slashProbes(h)
	if _, err := h.browse.Objects(ctx, h.connected, "paged", "", "a.txt", 100); err != nil {
		t.Fatal(err)
	}
	if n := slashProbes(h) - before; n != 0 {
		t.Fatalf("a page past the slash folder probed %d times, want none", n)
	}
	// "-" sorts before "/", so a full first page of "-" names, with a name
	// after it, ends before the folder's place, and the next page is the one
	// that covers it.
	slashFixture(h, "early", "-a.txt", "-b.txt", "/x.txt", "a.txt")
	before = slashProbes(h)
	page, err = h.browse.Objects(ctx, h.connected, "early", "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if n := slashProbes(h) - before; n != 0 || page.NextMarker != "-b.txt" {
		t.Fatalf("a page ending before the slash folder: %d probes, next marker %q, want none and -b.txt", n, page.NextMarker)
	}
}
