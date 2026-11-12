//go:build realcloud

package journeys

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestCopyWithinJourney: on each cloud, copy a folder to a sibling folder of
// the same container, copy it up to the container's top, and copy two
// selected files into a folder inside their own, checking each result byte
// for byte and that the source files are unchanged (journey 2, within a
// cloud).
func TestCopyWithinJourney(t *testing.T) {
	w := newWorld(t)
	for round := 1; round <= repeats(t); round++ {
		for _, label := range []string{"ceph", "swift"} {
			t.Run(fmt.Sprintf("%s/%d", label, round), func(t *testing.T) {
				w.t = t
				copyWithinJourney(w, w.accounts[label], round)
			})
		}
	}
}

func copyWithinJourney(w *world, a *account, round int) {
	t := w.t
	c := w.container(a, fmt.Sprintf("within%d", round))
	source := map[string][32]byte{}
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		body := []byte(fmt.Sprintf("round %d %s", round, name))
		if _, err := a.check.PutObject(w.ctx, c, "files/"+name, bytes.NewReader(body), "text/plain", nil); err != nil {
			t.Fatalf("seed: %s", failure(err))
		}
		source[name] = sha256.Sum256(body)
	}
	// files reads the three source files only, whatever else is under files/.
	files := func() map[string][32]byte {
		return only(w.stored(a, c, "files/"), func(name string) bool { return !strings.Contains(name, "/") })
	}
	base := app.CopyRequestDTO{SourceBookmarkID: a.bookmarkID, SourceContainer: c, DestBookmarkID: a.bookmarkID, DestContainer: c}

	step := func(name string, req app.CopyRequestDTO, read func() map[string][32]byte, want map[string][32]byte) {
		t.Helper()
		if _, err := w.transfer.PlanCopy(w.ctx, req); err != nil {
			t.Fatalf("%s: preview: %s", name, failure(err))
		}
		id, err := w.transfer.StartCopy(w.ctx, req)
		if err != nil {
			t.Fatalf("%s: %s", name, failure(err))
		}
		w.ended(name, w.await(a, id), "complete")
		if diff := w.settled(func() string { return same(read(), want) }); diff != "" {
			t.Fatalf("%s: the copy differs: %s", name, diff)
		}
		if diff := w.settled(func() string { return same(files(), source) }); diff != "" {
			t.Fatalf("%s: the source files changed: %s", name, diff)
		}
	}

	sibling := base
	sibling.SourcePrefix, sibling.DestPrefix = "files/", "backup/"
	step("copy to a sibling folder", sibling, func() map[string][32]byte { return w.stored(a, c, "backup/") }, source)

	// Up to the container's top (DESIGN §65.9 item 9): the top level then
	// holds the three files, beside the folders.
	up := base
	up.SourcePrefix = "files/"
	step("copy up to the top", up, func() map[string][32]byte {
		return only(w.stored(a, c, ""), func(name string) bool { return !strings.Contains(name, "/") })
	}, source)

	// Two selected files into a folder inside their own (ADR 0108 decision 4).
	inside := base
	inside.SourcePrefix, inside.DestPrefix = "files/", "files/picked/"
	inside.Members = []string{"files/a.txt", "files/c.txt"}
	step("copy a selection inside its folder", inside, func() map[string][32]byte { return w.stored(a, c, "files/picked/") },
		map[string][32]byte{"a.txt": source["a.txt"], "c.txt": source["c.txt"]})
}

// only keeps the names keep accepts.
func only(set map[string][32]byte, keep func(string) bool) map[string][32]byte {
	out := map[string][32]byte{}
	for name, d := range set {
		if keep(name) {
			out[name] = d
		}
	}
	return out
}
