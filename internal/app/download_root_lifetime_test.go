package app

import (
	"github.com/csmart/aeronautes/internal/transfer/local"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A directory pick is held by handle from the pick. Each lease derives a
// handle its owner closes; evicting the token closes only the pick's own
// handle, and the exact spelling survives whatever the pathname names
// later.
func TestDirectorySelectionLifetime(t *testing.T) {
	base := t.TempDir()
	selected := filepath.Join(base, "selected")
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	store := &selectionStore{}
	// One scope throughout: this test is about handle lifetime, not the
	// ADR 0041 binding, which has its own regressions.
	scope := selectionScope{bookmarkID: "b", generation: 1, container: "media", prefix: "scope/"}
	token := store.putDir(scope, selected)
	if dir, err := store.getDir(token, scope); err != nil || dir != selected {
		t.Fatalf("spelling changed: %q %v", dir, err)
	}
	first, path, err := store.leaseDir(token, scope)
	if err != nil || first == nil || path != selected {
		t.Fatalf("lease failed: %v %q %v", first, path, err)
	}
	second, _, err := store.leaseDir(token, scope)
	if err != nil || second == nil {
		t.Fatalf("second lease failed: %v %v", second, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Renaming the directory keeps the second lease bound to it.
	moved := filepath.Join(base, "moved")
	if err := os.Rename(selected, moved); err != nil {
		t.Fatal(err)
	}
	if displaced, err := second.Displaced(); err != nil || !displaced {
		t.Fatalf("renamed pick not reported displaced: %v %v", displaced, err)
	}
	// Evicting the token closes the pick's handle, not the lease.
	for range maxSelections {
		store.putSources(scope, []local.UploadSource{{Path: "filler", Name: "filler"}}, nil)
	}
	if _, err := store.getDir(token, scope); err == nil {
		t.Fatal("evicted token still resolves")
	}
	if _, _, err := store.leaseDir(token, scope); err == nil {
		t.Fatal("evicted token still leases")
	}
	if removed, err := second.Removed(); err != nil || removed {
		t.Fatalf("lease died with the evicted pick: %v %v", removed, err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	// A pick that cannot be held keeps its spelling and yields no handle,
	// so the preflight report says why instead of any effect.
	unusable := store.putDir(scope, selected+string(os.PathSeparator))
	root, path, err := store.leaseDir(unusable, scope)
	if err != nil || root != nil || !strings.HasSuffix(path, string(os.PathSeparator)) {
		t.Fatalf("unusable pick was held or lost its spelling: %v %q %v", root, path, err)
	}
	absent := store.putDir(scope, filepath.Join(base, "absent"))
	if root, _, err := store.leaseDir(absent, scope); err != nil || root != nil {
		t.Fatalf("absent directory was held: %v %v", root, err)
	}
}
