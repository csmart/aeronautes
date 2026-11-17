package systemtest

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

type listingFilePicker struct {
	files []string
	dir   string
}

func (p listingFilePicker) PickFiles(string) ([]string, error) {
	return append([]string(nil), p.files...), nil
}

func (p listingFilePicker) PickDirectory(string) (string, error) { return p.dir, nil }

type listingLocalState struct {
	mode   fs.FileMode
	size   int64
	digest [32]byte
	link   string
}

// Observe the owned filesystem directly so local transfer code cannot
// certify its own bytes or hide changes outside the selected directory.
func snapshotListingLocal(t *testing.T, root string) map[string]listingLocalState {
	t.Helper()
	result := map[string]listingLocalState{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		state := listingLocalState{mode: info.Mode()}
		switch {
		case info.Mode().IsRegular():
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state.size, state.digest = int64(len(body)), sha256.Sum256(body)
		case info.IsDir():
		case info.Mode()&os.ModeSymlink != 0:
			state.link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unmodelled owned fixture type at %q: %v", relative, info.Mode())
		}
		result[filepath.ToSlash(relative)] = state
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertListingLocalDelta(t *testing.T, before, after map[string]listingLocalState, files map[string][]byte, dirs ...string) {
	t.Helper()
	remaining := maps.Clone(after)
	for name, body := range files {
		if _, exists := before[name]; exists {
			t.Fatalf("new-file fixture %q already existed", name)
		}
		got, found := remaining[name]
		if !found || !got.mode.IsRegular() || got.size != int64(len(body)) || got.digest != sha256.Sum256(body) {
			t.Fatalf("local selected file %q is absent or differs: %+v", name, got)
		}
		delete(remaining, name)
	}
	for _, name := range dirs {
		if _, exists := before[name]; exists {
			t.Fatalf("new-directory fixture %q already existed", name)
		}
		if got, found := remaining[name]; !found || !got.mode.IsDir() {
			t.Fatalf("new local directory %q is absent or not a directory", name)
		}
		delete(remaining, name)
	}
	if !reflect.DeepEqual(remaining, before) {
		t.Fatalf("unexpected local filesystem delta: before=%+v after=%+v", before, remaining)
	}
}

func seedListingLocal(t *testing.T, root, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func shortListingUpload(t *testing.T, h *Harness) {
	t.Helper()
	defer h.CloseFind()
	dest := h.Container(h.Secondary, "paged-upload")
	sentinel := h.Container(h.Primary, "paged-upload-sentinel")
	h.Seed(h.Secondary, dest, "outside", []byte("destination sentinel"), nil)
	h.Seed(h.Primary, sentinel, "outside", []byte("other account"), nil)
	root := t.TempDir()
	items := []struct{ path, body string }{
		{"source/a plain.txt", "abc"}, {"source/b%2F +日本語", "defg"},
		{"second/c.txt", "hijkl"}, {"second/z-empty", ""},
	}
	var paths []string
	for _, item := range items {
		paths = append(paths, seedListingLocal(t, root, item.path, []byte(item.body)))
	}
	seedListingLocal(t, root, "source/unselected", []byte("not selected"))
	seedListingLocal(t, root, "outside/sentinel", []byte("outside both source directories"))
	localBefore := snapshotListingLocal(t, root)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	limitListingPages(t, h, h.Secondary, dest, "short", "")
	// The dialog uploads into the connected account, and a selection is
	// bound to it at the pick (ADR 0041), so connect where the upload
	// is going before choosing the files.
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatalf("connect the upload destination account: %v", err)
	}
	app.WireFilePicker(h.Transfer, listingFilePicker{files: paths})
	selection, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Secondary.BookmarkID, Container: dest, Prefix: "scope/"})
	if err != nil || selection == nil || selection.Token == "" || len(selection.Files) != 4 {
		t.Fatalf("owned upload selection: %+v, %v", selection, err)
	}
	request := app.UploadRequestDTO{BookmarkID: h.Secondary.BookmarkID, Container: dest, Prefix: "scope/", SelectionToken: selection.Token}
	plan, preflight := h.PreflightUpload(t.Context(), request)
	if preflight.Err() != nil || plan == nil || plan.Rejected() || len(plan.Items) != 4 || plan.TotalBytes != 12 {
		t.Fatalf("upload preflight changed the selection: %+v, %v", plan, preflight.Err())
	}
	for index, item := range plan.Items {
		if item.Name != "scope/"+filepath.Base(paths[index]) || item.Path != "" || item.Route != "rclone" || item.Size != int64(len(items[index].body)) {
			t.Fatalf("upload preflight item %d = %+v", index, item)
		}
	}
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), nil)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	receipt := h.StartUpload(t.Context(), request)
	if receipt.Err() != nil {
		t.Fatal(receipt.Err())
	}
	assertListingLocalJob(t, h.AwaitJob(receipt.JobID()))
	allowed := make([]string, 0, len(items))
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	for index, item := range items {
		name := "scope/" + filepath.Base(paths[index])
		got, found := afterB.Containers[dest].Objects[name]
		wantDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(item.body)))
		if !found || got.Size != int64(len(item.body)) || got.Digest != wantDigest {
			t.Fatalf("uploaded %q is absent or differs: %+v", name, got)
		}
		allowed = append(allowed, dest+"/"+name)
	}
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), nil)
	h.AssertNoCollateral(beforeA, afterA)
	h.AssertNoCollateral(beforeB, afterB, allowed...)
	preflight.CreditRead()
	receipt.CreditHappy()
}

func shortListingDownload(t *testing.T, h *Harness) {
	t.Helper()
	defer h.CloseFind()
	source := h.Container(h.Primary, "paged-download")
	sentinel := h.Container(h.Secondary, "paged-download-sentinel")
	files := map[string][]byte{
		"a plain.txt": []byte("abc"), "b%2F +日本語": []byte("defg"),
		"nested/c.txt": []byte("hijkl"), "z-empty": {},
	}
	for name, body := range files {
		h.Seed(h.Primary, source, "scope/"+name, body, nil)
	}
	h.Seed(h.Primary, source, "outside", []byte("unselected source"), nil)
	h.Seed(h.Secondary, sentinel, "outside", []byte("other account"), nil)
	root := t.TempDir()
	seedListingLocal(t, root, "download/sentinel", []byte("existing destination file"))
	seedListingLocal(t, root, "outside/sentinel", []byte("outside destination"))
	localBefore := snapshotListingLocal(t, root)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	trace := limitListingPages(t, h, h.Primary, source, "short", "")
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: filepath.Join(root, "download")})
	selection, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: "scope/"})
	if err != nil || selection == nil || selection.Token == "" {
		t.Fatalf("owned download selection: %+v, %v", selection, err)
	}
	request := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: "scope/", DestDirToken: selection.Token}
	plan, preflight := h.PreflightDownload(t.Context(), request)
	if preflight.Err() != nil || plan == nil || plan.Rejected || plan.Objects != 4 || plan.TotalBytes != 12 {
		t.Fatalf("short pages truncated download preflight: %+v, %v", plan, preflight.Err())
	}
	seen := map[string]bool{}
	for _, item := range plan.ItemSamples {
		relative := filepath.ToSlash(item.RelPath)
		body, found := files[relative]
		if !found || seen[relative] || item.Name != "scope/"+relative || item.Dir || item.Route != "rclone" || item.Size != int64(len(body)) {
			t.Fatalf("download preflight item = %+v", item)
		}
		seen[relative] = true
	}
	trace.assertMarkers(t, "scope/", "", "", "scope/a plain.txt", "scope/b%2F +日本語", "scope/nested/c.txt", "scope/z-empty")
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), nil)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	receipt := h.StartDownload(t.Context(), request)
	if receipt.Err() != nil {
		t.Fatal(receipt.Err())
	}
	assertListingLocalJob(t, h.AwaitJob(receipt.JobID()))
	expected := map[string][]byte{}
	for name, body := range files {
		expected["download/"+name] = body
	}
	// Exactly the files, by digest, and their folder: the engine writes each
	// file at its name, and nothing of its own or of a stage is left (ADR 0122
	// decision 5).
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), expected, "download/nested")
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	preflight.CreditRead()
	receipt.CreditHappy()
}

func assertListingLocalJob(t *testing.T, job *app.JobDTO) {
	t.Helper()
	want := jobs.Counters{
		ObjectsPlanned: 4, ObjectsCopied: 4, ObjectsVerified: 4,
		BytesPlanned: 12, BytesCopied: 12,
	}
	if job.State != "complete" || job.Error != "" || job.Counters != want {
		t.Fatalf("selected local transfer was incomplete: %+v", job)
	}
}

func TestShortListingUploadMock(t *testing.T)        { shortListingUpload(t, findMockHarness(t)) }
func TestShortListingUploadRealSwift(t *testing.T)   { shortListingUpload(t, New(t)) }
func TestShortListingDownloadMock(t *testing.T)      { shortListingDownload(t, findMockHarness(t)) }
func TestShortListingDownloadRealSwift(t *testing.T) { shortListingDownload(t, New(t)) }
