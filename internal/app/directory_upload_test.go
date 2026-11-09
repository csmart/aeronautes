package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// directoryPicker answers the directory chooser with one folder and keeps the
// titles it was given.
type directoryPicker struct {
	dir    string
	titles []string
	// chosen runs when the chooser answers, where a caller can give up.
	chosen func()
}

func (p *directoryPicker) PickFiles(string) ([]string, error) { return nil, nil }
func (p *directoryPicker) PickDirectory(title string) (string, error) {
	p.titles = append(p.titles, title)
	if p.chosen != nil {
		p.chosen()
	}
	return p.dir, nil
}

// uploadFolder writes a small tree: two files of one base name in different
// folders, a hidden file, an empty directory and a link.
func uploadFolder(t *testing.T) string {
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
	if err := os.Symlink(filepath.Join(root, "a", "image.jpg"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	return root
}

// ADR 0103 end to end on the mock: a folder is picked under the backend's own
// title, bound to the scope, named as a folder under the prefix, its walk's
// omissions disclosed as warnings that reject nothing, and uploaded through
// the unchanged route with each file at its own name.
func TestPickUploadDirectoryUploadsAFolderAsAFolder(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.installEngine(nil)
	h.mock.AddContainer("media", "standard")
	bookmark := h.transfer.connections.ActiveBookmarkID()
	picker := &directoryPicker{dir: uploadFolder(t)}
	h.transfer.setFilePicker(picker)
	ctx := context.Background()

	picked, err := h.transfer.PickUploadDirectory(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "inbox/"})
	if err != nil || picked == nil || picked.Token == "" {
		t.Fatalf("pick = %+v, %v", picked, err)
	}
	if len(picker.titles) != 1 || !strings.Contains(picker.titles[0], "Upload into") || !strings.Contains(picker.titles[0], "choose a folder") {
		t.Errorf("the chooser's title does not state the binding: %q", picker.titles)
	}
	// Names come in directory order; the comparisons sort.
	wantNames := []string{"photos/.hidden", "photos/a/image.jpg", "photos/b/image.jpg"}
	files := append([]string(nil), picked.Files...)
	sort.Strings(files)
	if picked.Folder != "photos" || !reflect.DeepEqual(files, wantNames) || picked.Skipped != 1 ||
		!reflect.DeepEqual(picked.SkippedSample, []string{"link"}) || picked.EmptyDirectories != 1 {
		t.Fatalf("picked = %+v, want the folder's names and counts", picked)
	}

	req := UploadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "inbox/", SelectionToken: picked.Token}
	report, err := h.transfer.PreflightUpload(ctx, req)
	if err != nil || report == nil || report.Rejected() || len(report.Items) != 3 {
		t.Fatalf("preflight = %+v, %v", report, err)
	}
	var itemNames []string
	for _, item := range report.Items {
		itemNames = append(itemNames, item.Name)
		if item.Path != "" {
			t.Errorf("item %+v carries a local path across the bridge", item)
		}
	}
	sort.Strings(itemNames)
	for i, name := range wantNames {
		if itemNames[i] != "inbox/"+name {
			t.Errorf("items = %q, want them named under the prefix", itemNames)
		}
	}
	// The walk's omissions are warnings beside the items, named under the
	// folder, and none of them rejects.
	var kinds []string
	for _, p := range report.Problems {
		kinds = append(kinds, p.Kind+":"+p.Name)
		if p.Reject {
			t.Errorf("a disclosure rejects: %+v", p)
		}
	}
	if !reflect.DeepEqual(kinds, []string{"skipped:photos/link", "empty-directories:photos"}) {
		t.Errorf("disclosures = %q", kinds)
	}

	id, err := h.transfer.StartUpload(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, id); job.State != "complete" || job.Counters.ObjectsCopied != 3 {
		t.Fatalf("the folder did not upload whole: %s %+v", job.State, job.Counters)
	}
	for name, want := range map[string]int64{"inbox/photos/a/image.jpg": 5, "inbox/photos/b/image.jpg": 5, "inbox/photos/.hidden": 1} {
		detail, err := h.inspector.Object(ctx, bookmark, "media", name)
		if err != nil || detail.ContentLength != want {
			t.Errorf("%s = %+v, %v; want %d bytes", name, detail, err, want)
		}
	}
	// The selection was consumed by the start, as a file selection is.
	if _, err := h.transfer.PreflightUpload(ctx, req); err == nil {
		t.Error("the folder selection survived its start")
	}
}

// A folder with no regular file returns its summary and no token: the dialog
// shows the folder and its counts with nothing to start.
func TestPickUploadDirectoryWithNoFileReturnsItsSummary(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	root := filepath.Join(t.TempDir(), "hollow")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "empty"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	h.transfer.setFilePicker(&directoryPicker{dir: root})
	picked, err := h.transfer.PickUploadDirectory(context.Background(), SelectionScopeDTO{BookmarkID: bookmark, Container: "fixtures"})
	if err != nil || picked == nil {
		t.Fatalf("pick = %+v, %v", picked, err)
	}
	if picked.Token != "" || len(picked.Files) != 0 || picked.Folder != "hollow" || picked.Skipped != 1 || picked.EmptyDirectories != 1 {
		t.Fatalf("picked = %+v, want the summary alone", picked)
	}
}

// The folder selection is the same scoped capability as a file selection
// (ADR 0041): another prefix is refused.
func TestPickUploadDirectoryIsBoundToItsScope(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.transfer.setFilePicker(&directoryPicker{dir: uploadFolder(t)})
	ctx := context.Background()
	picked, err := h.transfer.PickUploadDirectory(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "fixtures", Prefix: "up/"})
	if err != nil || picked == nil || picked.Token == "" {
		t.Fatalf("pick = %+v, %v", picked, err)
	}
	_, err = h.transfer.PreflightUpload(ctx, UploadRequestDTO{BookmarkID: bookmark, Container: "fixtures", Prefix: "elsewhere/", SelectionToken: picked.Token})
	if !errors.Is(err, errSelectionScope) {
		t.Fatalf("another prefix used the folder selection: %v", err)
	}
}

// A pick whose caller gave up while the chooser was open issues no selection:
// the walk and the token both observe the cancellation.
func TestPickUploadDirectoryIssuesNothingWhenCancelled(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	ctx, cancel := context.WithCancel(context.Background())
	h.transfer.setFilePicker(&directoryPicker{dir: uploadFolder(t), chosen: cancel})
	picked, err := h.transfer.PickUploadDirectory(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "fixtures", Prefix: "up/"})
	if err == nil || picked != nil {
		t.Fatalf("a cancelled pick issued a selection: %+v, %v", picked, err)
	}
}
