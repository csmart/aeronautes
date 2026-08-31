package systemtest

import (
	"testing"
)

// TestHostileObjectNameRoundTrips seeds objects whose names contain a
// tab, a newline, a space, and percent — the seed writer and the
// independent oracle must AGREE on the encoding against real Swift, or a
// negative-space assertion over such names would be blind (codex war-room
// A-04: the seed escaper was not previously exercised end-to-end). It
// also proves AssertNoCollateral keys these names correctly: a change to
// one hostile name is caught unless that exact name is allowed.
func TestHostileObjectNameRoundTrips(t *testing.T) {
	h := New(t)
	c := h.Container(h.Primary, "hostile")

	names := map[string][]byte{
		"tab\tname":    []byte("tabbed body"),
		"nl\nname":     []byte("newline body"),
		"sp ace name":  []byte("spaced body"),
		"pct%25name":   []byte("percent body"),
		"deep/a/b.txt": []byte("nested path body"),
	}
	for name, body := range names {
		h.Seed(h.Primary, c, name, body, nil)
	}

	snap := h.Snapshot(h.Primary, c)
	objs := snap.Containers[c].Objects
	if len(objs) != len(names) {
		got := make([]string, 0, len(objs))
		for n := range objs {
			got = append(got, n)
		}
		t.Fatalf("witnessed %d hostile-named objects, want %d: %q", len(objs), len(names), got)
	}
	for name, body := range names {
		o, ok := objs[name]
		if !ok {
			t.Fatalf("hostile name %q not witnessed — seed/oracle encoding disagree", name)
		}
		if o.Size != int64(len(body)) {
			t.Fatalf("hostile name %q size %d, want %d", name, o.Size, len(body))
		}
	}

	// Negative space keys on the exact hostile name: change one, and it is
	// collateral unless that precise name is allowed.
	before := h.Snapshot(h.Primary, c)
	h.Seed(h.Primary, c, "tab\tname", []byte("CHANGED tabbed body!!"), nil)
	after := h.Snapshot(h.Primary, c)
	if s := collateral(before, after, nil); len(s) != 1 {
		t.Fatalf("change to a tab-named object not caught as collateral: %v", s)
	}
	h.AssertNoCollateral(before, after, c+"/tab\tname")
}
