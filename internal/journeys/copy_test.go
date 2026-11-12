//go:build realcloud

package journeys

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/realcloud"
)

// repeats is how many times each journey runs, so an intermittent fault in
// a store shows up as a count (docs/journeys.md).
func repeats(t *testing.T) int {
	n, err := strconv.Atoi(os.Getenv("JOURNEY_REPEAT"))
	if err != nil || n < 1 {
		return 3
	}
	return n
}

// TestCopyJourney: upload a folder, one file large enough to be segmented,
// to one cloud; copy the container to the other cloud; copy two selected
// files; download the copy back. Every step is checked byte for byte by an
// independent read, in both directions, repeatedly (journeys 1 to 3).
func TestCopyJourney(t *testing.T) {
	w := newWorld(t)
	for round := 1; round <= repeats(t); round++ {
		for _, pair := range [][2]string{{"ceph", "swift"}, {"swift", "ceph"}} {
			t.Run(fmt.Sprintf("%s_to_%s/%d", pair[0], pair[1], round), func(t *testing.T) {
				w.t = t
				copyJourney(w, w.accounts[pair[0]], w.accounts[pair[1]])
			})
		}
	}
}

func copyJourney(w *world, from, to *account) {
	t := w.t
	// Three small files and one of 3 MiB, segmented at 1 MiB.
	folder, digests := w.fixture(1024, 64*1024, 200*1024, 3<<20)
	src := w.container(from, "src")
	if err := from.owned.ClaimSidecar(w.ctx, src); err != nil {
		t.Fatal(err)
	}
	w.picker.dir = folder
	picked, err := w.transfer.PickUploadDirectory(w.ctx, app.SelectionScopeDTO{BookmarkID: from.bookmarkID, Container: src})
	if err != nil || picked == nil {
		t.Fatalf("pick the folder: %v", failure(err))
	}
	id, err := w.transfer.StartUpload(w.ctx, app.UploadRequestDTO{
		BookmarkID: from.bookmarkID, Container: src, SelectionToken: picked.Token,
		Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"},
	})
	if err != nil {
		t.Fatalf("upload: %s", failure(err))
	}
	if job := w.await(from, id); job.State != "complete" {
		t.Fatalf("upload ended %s: %s", job.State, job.Error)
	}
	prefix := filepath.Base(folder) + "/"
	if diff := same(w.stored(from, src, prefix), digests); diff != "" {
		t.Fatalf("uploaded files differ from the local ones: %s", diff)
	}

	// The whole container, across clouds.
	dst := w.container(to, "dst")
	if err := to.owned.ClaimSidecar(w.ctx, dst); err != nil {
		t.Fatal(err)
	}
	copyRequest := app.CopyRequestDTO{SourceBookmarkID: from.bookmarkID, SourceContainer: src, DestBookmarkID: to.bookmarkID, DestContainer: dst}
	if _, err := w.transfer.PlanCopy(w.ctx, copyRequest); err != nil {
		t.Fatalf("preview the copy: %s", failure(err))
	}
	if id, err = w.transfer.StartCopy(w.ctx, copyRequest); err != nil {
		t.Fatalf("copy: %s", failure(err))
	}
	if job := w.await(to, id); job.State != "complete" {
		t.Fatalf("copy ended %s: %s", job.State, job.Error)
	}
	if diff := same(w.stored(to, dst, prefix), digests); diff != "" {
		t.Fatalf("copied files differ from the source: %s", diff)
	}

	// Two selected files, across clouds.
	chosen := w.container(to, "sel")
	members := []string{prefix + "file00.bin", prefix + "file02.bin"}
	selection := app.CopyRequestDTO{SourceBookmarkID: from.bookmarkID, SourceContainer: src, SourcePrefix: prefix,
		DestBookmarkID: to.bookmarkID, DestContainer: chosen, Members: members}
	if id, err = w.transfer.StartCopy(w.ctx, selection); err != nil {
		t.Fatalf("copy the selection: %s", failure(err))
	}
	if job := w.await(to, id); job.State != "complete" || job.SelectedMembers != 2 {
		t.Fatalf("selection copy ended %s with %d members: %s", job.State, job.SelectedMembers, job.Error)
	}
	want := map[string][32]byte{"file00.bin": digests["file00.bin"], "file02.bin": digests["file02.bin"]}
	if diff := same(w.stored(to, chosen, ""), want); diff != "" {
		t.Fatalf("the selection copy differs: %s", diff)
	}

	// Download the copy back and compare with the originals.
	local := filepath.Join(w.dir, "download-"+dst)
	if err := os.MkdirAll(local, 0o700); err != nil {
		t.Fatal(err)
	}
	w.picker.dir = local
	dest, err := w.transfer.PickDownloadDir(w.ctx, app.SelectionScopeDTO{BookmarkID: to.bookmarkID, Container: dst, Prefix: prefix})
	if err != nil || dest == nil {
		t.Fatalf("pick the download folder: %v", failure(err))
	}
	if id, err = w.transfer.StartDownload(w.ctx, app.DownloadRequestDTO{BookmarkID: to.bookmarkID, Container: dst, Prefix: prefix, DestDirToken: dest.Token}); err != nil {
		t.Fatalf("download: %s", failure(err))
	}
	if job := w.await(to, id); job.State != "complete" {
		t.Fatalf("download ended %s: %s", job.State, job.Error)
	}
	got := map[string][32]byte{}
	for name := range digests {
		body, err := os.ReadFile(filepath.Join(local, name))
		if err != nil {
			t.Errorf("downloaded %s missing", name)
			continue
		}
		got[name] = sha256.Sum256(body)
	}
	if diff := same(got, digests); diff != "" {
		t.Fatalf("downloaded files differ from the originals: %s", diff)
	}
}

// failure is realcloud.Failure that tolerates no error.
func failure(err error) string {
	if err == nil {
		return "no error"
	}
	return realcloud.Failure(err)
}
