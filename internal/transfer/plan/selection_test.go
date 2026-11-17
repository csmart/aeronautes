package plan

import "testing"

// ADR 0108 decision 7: an object member takes its own name, a folder member
// everything under it, its marker included, and a member nothing listed under
// is missing.
func TestASelectionTakesItsObjectsAndEverythingUnderItsFolders(t *testing.T) {
	s := NewSelection([]string{"scope/a.txt", "scope/docs/", "scope/empty/"})
	for name, want := range map[string]bool{
		"scope/a.txt": true, "scope/docs/": true, "scope/docs/one.txt": true, "scope/docs/deep/two.txt": true,
		"scope/b.txt": false, "scope/a.txt.bak": false, "scope/docs-old/one.txt": false, "scope/": false,
	} {
		if got := s.Take(name); got != want {
			t.Errorf("Take(%q) = %t, want %t", name, got, want)
		}
	}
	if s.Missing() != 1 || s.FirstMissing() != "scope/empty/" {
		t.Fatalf("missing %d, first %q; want the empty folder alone", s.Missing(), s.FirstMissing())
	}
	if !s.Holds("scope/docs/") || !s.Holds("scope/") || s.Holds("scope/other/") {
		t.Fatal("Holds does not say which prefixes hold a member")
	}
	var whole *Selection
	if !whole.Take("anything") || whole.Missing() != 0 || whole.FirstMissing() != "" || NewSelection(nil) != nil {
		t.Fatal("no members is not the whole scope")
	}
}

func TestAMemberUnderASelectedFolderIsNested(t *testing.T) {
	if member, folder := Nested([]string{"scope/docs/", "scope/docs/one.txt"}); member != "scope/docs/one.txt" || folder != "scope/docs/" {
		t.Fatalf("Nested = %q, %q", member, folder)
	}
	if member, _ := Nested([]string{"scope/docs/", "scope/docs-old/", "scope/a.txt"}); member != "" {
		t.Fatalf("siblings were taken as nested: %q", member)
	}
}
