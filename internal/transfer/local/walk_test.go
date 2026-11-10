package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// folderFixture builds a folder with files, an empty directory and two links,
// and returns its root.
func folderFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "photos")
	for rel, content := range map[string]string{"a/image.jpg": "alpha", "b/image.jpg": "bravo", ".hidden": "h"} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"link-to-file": "a/image.jpg", "link-to-dir": "a"} {
		if err := os.Symlink(filepath.Join(root, target), filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// ADR 0103 decisions 2 and 3: the folder arrives as a folder, hidden files are
// files, links are skipped and named, an empty directory is counted, and
// nothing outside the tree is read.
func TestWalkUploadDirectoryNamesAFolderAsAFolder(t *testing.T) {
	root := folderFixture(t)
	selection, err := WalkUploadDirectory(t.Context(), root, MaxDirectoryFiles)
	if err != nil {
		t.Fatal(err)
	}
	want := []UploadSource{
		{Path: filepath.Join(root, ".hidden"), Name: "photos/.hidden"},
		{Path: filepath.Join(root, "a", "image.jpg"), Name: "photos/a/image.jpg"},
		{Path: filepath.Join(root, "b", "image.jpg"), Name: "photos/b/image.jpg"},
	}
	// Directory order is the filesystem's; the comparison sorts both sides.
	sources := append([]UploadSource(nil), selection.Sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	if selection.Folder != "photos" || !reflect.DeepEqual(sources, want) {
		t.Fatalf("sources = %+v (folder %q), want %+v", sources, selection.Folder, want)
	}
	skipped := append([]string(nil), selection.SkippedSample...)
	sort.Strings(skipped)
	if selection.Skipped != 2 || !reflect.DeepEqual(skipped, []string{"link-to-dir", "link-to-file"}) {
		t.Errorf("skipped = %d %q, want the two links named", selection.Skipped, selection.SkippedSample)
	}
	if selection.EmptyDirectories != 1 {
		t.Errorf("empty directories = %d, want the one empty folder", selection.EmptyDirectories)
	}
}

func TestWalkUploadDirectoryRefusals(t *testing.T) {
	root := folderFixture(t)
	cases := []struct {
		name, root string
		limit      int
		says       string
	}{
		{"a relative root", "relative/photos", MaxDirectoryFiles, "absolute UTF-8 path"},
		{"an uncleaned root", root + "/", MaxDirectoryFiles, "absolute UTF-8 path"},
		{"a missing root", filepath.Join(root, "missing"), MaxDirectoryFiles, "could not be read"},
		{"a file where a folder was expected", filepath.Join(root, ".hidden"), MaxDirectoryFiles, "not a folder"},
		{"more files than the limit", root, 2, "more than 2 files"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			selection, err := WalkUploadDirectory(t.Context(), c.root, c.limit)
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("got %+v, %v; want a refusal saying %q", selection, err, c.says)
			}
		})
	}
	t.Run("an unreadable directory rejects the whole selection", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads every directory")
		}
		sealed := filepath.Join(root, "sealed")
		if err := os.MkdirAll(sealed, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sealed, "inside"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sealed, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
		selection, err := WalkUploadDirectory(t.Context(), root, MaxDirectoryFiles)
		if err == nil || !strings.Contains(err.Error(), "could not be read in full") {
			t.Fatalf("got %+v, %v; want the walk refused with the unreadable directory named", selection, err)
		}
	})
	t.Run("a file whose name the store cannot hold rejects the selection by name", func(t *testing.T) {
		scans := filepath.Join(t.TempDir(), "scans")
		if err := os.MkdirAll(scans, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(scans, "r\xe9sum\xe9.pdf"), []byte("x"), 0o600); err != nil {
			t.Skipf("this filesystem refuses a name that is not UTF-8: %v", err)
		}
		selection, err := WalkUploadDirectory(t.Context(), scans, MaxDirectoryFiles)
		named := filepath.Join(scans, "r\uFFFDsum\uFFFD.pdf")
		if err == nil || !strings.Contains(err.Error(), "not valid UTF-8") || !strings.Contains(err.Error(), named) {
			t.Fatalf("got %+v, %v; want the walk refused with %s named", selection, err, named)
		}
	})
}

// The report bounds what it retains: every skipped entry is counted, the
// first maxFailures are named, and the files themselves are unaffected.
func TestWalkUploadDirectoryBoundsTheSkippedSample(t *testing.T) {
	root := filepath.Join(t.TempDir(), "links")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "only"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	const links = maxFailures + 50
	for i := 0; i < links; i++ {
		if err := os.Symlink(filepath.Join(root, "only"), filepath.Join(root, fmt.Sprintf("link-%04d", i))); err != nil {
			t.Fatal(err)
		}
	}
	selection, err := WalkUploadDirectory(t.Context(), root, MaxDirectoryFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Sources) != 1 || selection.Skipped != links || len(selection.SkippedSample) != maxFailures {
		t.Fatalf("sources %d, skipped %d, sample %d; want 1, %d, %d", len(selection.Sources), selection.Skipped, len(selection.SkippedSample), links, maxFailures)
	}
}

// ADR 0103 decision 3: the chosen root is taken as the chooser gave it, an
// alias included, and keeps the chosen spelling as the folder's name.
func TestWalkUploadDirectoryEnumeratesAnAliasedRoot(t *testing.T) {
	root := folderFixture(t)
	alias := filepath.Join(t.TempDir(), "photos-link")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	selection, err := WalkUploadDirectory(t.Context(), alias, MaxDirectoryFiles)
	if err != nil {
		t.Fatal(err)
	}
	var names, paths []string
	for _, source := range selection.Sources {
		names = append(names, source.Name)
		paths = append(paths, source.Path)
	}
	sort.Strings(names)
	sort.Strings(paths)
	if selection.Folder != "photos-link" || !reflect.DeepEqual(names, []string{"photos-link/.hidden", "photos-link/a/image.jpg", "photos-link/b/image.jpg"}) {
		t.Fatalf("an aliased root was not walked under its chosen name: %+v", selection)
	}
	if paths[1] != filepath.Join(alias, "a", "image.jpg") {
		t.Errorf("source path = %q, want it spelt under the alias as chosen", paths[1])
	}
}

// A listed directory that is a link when inspected by handle is skipped, not
// followed: the component opener is what the descent relies on.
func TestOpenFolderComponentRefusesALinkInPlaceOfADirectory(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "linked")); err != nil {
		t.Fatal(err)
	}
	parent, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if _, err := openFolderComponent(parent, "linked", filepath.Join(base, "linked")); !errors.Is(err, errNotOrdinaryDirectory) {
		t.Fatalf("a link that took a directory's name was opened: %v", err)
	}
	opened, err := openFolderComponent(parent, "real", filepath.Join(base, "real"))
	if err != nil {
		t.Fatalf("an ordinary directory was refused: %v", err)
	}
	opened.Close()
}

// The walk counts every empty directory without keeping the tree: a wide
// tree of empty folders is counted in full with no file to upload.
func TestWalkUploadDirectoryCountsEmptyDirectoriesWithoutKeepingThem(t *testing.T) {
	root := filepath.Join(t.TempDir(), "hollow")
	const wide = 1500
	for i := 0; i < wide; i++ {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("d%04d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	selection, err := WalkUploadDirectory(t.Context(), root, MaxDirectoryFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Sources) != 0 || selection.EmptyDirectories != wide || selection.Skipped != 0 {
		t.Fatalf("selection = %+v, want %d empty directories and nothing else", selection, wide)
	}
}

// A cancelled walk ends where it stands with the cancellation, not a
// selection.
func TestWalkUploadDirectoryStopsWhenCancelled(t *testing.T) {
	root := folderFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if selection, err := WalkUploadDirectory(ctx, root, MaxDirectoryFiles); !errors.Is(err, context.Canceled) || selection != nil {
		t.Fatalf("a cancelled walk returned %+v, %v", selection, err)
	}
}
