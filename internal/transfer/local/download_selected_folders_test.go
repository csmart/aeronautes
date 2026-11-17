package local

import (
	"path/filepath"
	"sort"
	"testing"
)

// ADR 0108 decision 7: a download of selected objects and a selected folder
// plans the objects and everything in the folder, each at its path from the
// source folder, so the folder's name is kept; the ceiling counts what is
// planned; a selected folder with nothing in it refuses the preview, naming it.
func TestADownloadOfFilesAndAFolderPlansEverythingInTheFolder(t *testing.T) {
	s, mock := session(t)
	for _, name := range []string{"scope/a.txt", "scope/b.txt", "scope/docs/x", "scope/docs/deep/y", "scope/docsier/z"} {
		seed(mock, "media", name, "bytes")
	}
	dest := t.TempDir()
	preflight, err := PreflightDownloadMembersInto(t.Context(), s, "media", "scope/", testDestination(t, dest), 0,
		[]string{"scope/a.txt", "scope/docs/"})
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight = %+v %v", preflight, err)
	}
	var planned []string
	for _, item := range preflight.Items {
		planned = append(planned, item.RelPath)
	}
	sort.Strings(planned)
	want := []string{"a.txt", filepath.Join("docs", "deep", "y"), filepath.Join("docs", "x")}
	if len(planned) != len(want) || planned[0] != want[0] || planned[1] != want[1] || planned[2] != want[2] {
		t.Fatalf("planned %v, want %v", planned, want)
	}
	over, err := PreflightDownloadMembersInto(t.Context(), s, "media", "scope/", testDestination(t, dest), 2, []string{"scope/a.txt", "scope/docs/"})
	if err != nil || !over.Rejected() || over.Problems[0].Kind != "selection-ceiling" {
		t.Fatalf("a ceiling of two over three planned objects: %+v %v", over, err)
	}
	missing, err := PreflightDownloadMembersInto(t.Context(), s, "media", "scope/", testDestination(t, dest), 0, []string{"scope/a.txt", "scope/gone/"})
	if err != nil || !missing.Rejected() || missing.Problems[0].Kind != "selection-missing" || missing.Problems[0].Name != "scope/gone/" {
		t.Fatalf("a selected folder with nothing in it: %+v %v", missing, err)
	}
}
