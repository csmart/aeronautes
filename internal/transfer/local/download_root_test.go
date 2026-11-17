package local

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testDestination captures a directory the way the service does at the
// pick, closing the handle with the test.
func testDestination(t *testing.T, path string) *DestinationRoot {
	t.Helper()
	destination, err := OpenDestinationRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destination.Close() })
	return destination
}

// testDirectory binds a handle to an existing directory beneath the held
// destination, closing it with the test.
func testDirectory(t *testing.T, destination *DestinationRoot, rel string) *os.Root {
	t.Helper()
	dir, _, err := destination.openDirectory(rel, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	return dir
}

func TestDestinationRootIdentity(t *testing.T) {
	base := t.TempDir()
	selected := filepath.Join(base, "selected")
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := testDestination(t, selected)
	if destination.Path() != selected {
		t.Fatalf("spelling changed: %q", destination.Path())
	}
	derived, err := destination.Derive()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = derived.Close() })
	for _, r := range []*DestinationRoot{destination, derived} {
		if displaced, err := r.Displaced(); err != nil || displaced {
			t.Fatalf("held directory reported displaced at its name: %v %v", displaced, err)
		}
		if removed, err := r.Removed(); err != nil || removed {
			t.Fatalf("held directory reported removed: %v %v", removed, err)
		}
	}
	// The derived handle outlives the one it came from.
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	if err := derived.root.Mkdir("after-close", 0o755); err != nil {
		t.Fatalf("derived handle died with its origin: %v", err)
	}
	// Renaming the directory keeps the handle bound to it and makes the
	// captured pathname a displacement; a link planted at the name stays
	// a displacement even when it points back at the directory.
	moved := filepath.Join(base, "moved")
	if err := os.Rename(selected, moved); err != nil {
		t.Fatal(err)
	}
	if displaced, err := derived.Displaced(); err != nil || !displaced {
		t.Fatalf("renamed directory not reported displaced: %v %v", displaced, err)
	}
	if err := os.Symlink(moved, selected); err != nil {
		t.Fatal(err)
	}
	if displaced, err := derived.Displaced(); err != nil || !displaced {
		t.Fatalf("link at the captured name not reported displaced: %v %v", displaced, err)
	}
	if err := derived.root.Mkdir("after-rename", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "after-rename")); err != nil {
		t.Fatalf("effect through the handle did not land in the directory picked: %v", err)
	}
	if err := os.RemoveAll(moved); err != nil {
		t.Fatal(err)
	}
	if removed, err := derived.Removed(); err != nil || !removed {
		t.Fatalf("deleted directory not reported removed: %v %v", removed, err)
	}
	if _, err := OpenDestinationRoot(base + string(os.PathSeparator) + "relative" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "selected"); err == nil || err.Error() != downloadPathRefusal {
		t.Fatalf("unclean spelling was held: %v", err)
	}
	if _, err := OpenDestinationRoot(filepath.Join(base, "absent")); err == nil {
		t.Fatal("absent directory was held")
	}
}

func TestDestinationRootOpenDirectory(t *testing.T) {
	base := t.TempDir()
	selected := filepath.Join(base, "selected")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{filepath.Join(selected, "real", "inner"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(selected, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(selected, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(selected, "alias")); err != nil {
		t.Fatal(err)
	}
	destination := testDestination(t, selected)
	for _, c := range []struct {
		rel, refusal string
		create       bool
	}{
		{rel: "real/inner"},
		{rel: "real/created/deep", create: true},
		{rel: "escape/inner", refusal: fmt.Sprintf("destination component %q is not an inspectable ordinary directory", filepath.Join(selected, "escape"))},
		{rel: "alias/inner", refusal: fmt.Sprintf("destination component %q is not an inspectable ordinary directory", filepath.Join(selected, "alias"))},
		{rel: "file/inner", refusal: fmt.Sprintf("destination component %q is not an inspectable ordinary directory", filepath.Join(selected, "file"))},
		{rel: "file/inner", create: true, refusal: fmt.Sprintf("destination component %q is not an inspectable ordinary directory", filepath.Join(selected, "file"))},
		{rel: "missing/inner", refusal: "lstat " + filepath.Join(selected, "missing") + ": no such file or directory"},
		{rel: "real/../inner", refusal: "is not a clean relative path"},
		{rel: filepath.Join(base, "outside"), refusal: "is not a clean relative path"},
	} {
		t.Run(c.rel, func(t *testing.T) {
			dir, _, err := destination.openDirectory(c.rel, c.create)
			if c.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), c.refusal) {
					t.Fatalf("expected refusal %q, got %v", c.refusal, err)
				}
				if dir != nil {
					t.Fatal("refusal returned a handle")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			info, err := dir.Stat(".")
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.Stat(filepath.Join(selected, filepath.FromSlash(c.rel)))
			if err != nil || !os.SameFile(info, want) {
				t.Fatalf("handle is not bound to %q: %v", c.rel, err)
			}
		})
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("a refused walk changed the outside directory: %v %v", entries, err)
	}
}

// The application's own download removes its temporary by name through the
// held folder, and never removes a directory that took that name.
func TestTemporaryRemovalNeverRemovesADirectory(t *testing.T) {
	base := t.TempDir()
	destination := testDestination(t, base)
	if err := os.Mkdir(filepath.Join(base, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, base, "file", "bytes")
	root := testDirectory(t, destination, ".")
	if err := unlinkFileAt(root, "empty", filepath.Join(base, "empty")); err == nil || !strings.Contains(err.Error(), filepath.Join(base, "empty")) {
		t.Fatalf("file removal removed a directory or lost its pathname: %v", err)
	}
	if err := unlinkFileAt(root, "file", filepath.Join(base, "file")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 1 || entries[0].Name() != "empty" {
		t.Fatalf("unexpected entries after removal: %v %v", entries, err)
	}
}
