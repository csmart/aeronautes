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
	"github.com/csmart/aeronautes/internal/swiftcontrol"
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
// independent read, in both directions, repeatedly (journeys 1 to 3). Odd
// rounds choose static for the segmented file, the default, and even rounds
// dynamic, so two rounds or more cover both choices on both clouds; a
// static choice is published static where the store returns static
// manifests and dynamic where it does not (ADR 0111).
func TestCopyJourney(t *testing.T) {
	w := newWorld(t)
	for round := 1; round <= repeats(t); round++ {
		for _, pair := range [][2]string{{"ceph", "swift"}, {"swift", "ceph"}} {
			t.Run(fmt.Sprintf("%s_to_%s/%d", pair[0], pair[1], round), func(t *testing.T) {
				w.t = t
				copyJourney(w, w.accounts[pair[0]], w.accounts[pair[1]], round)
			})
		}
	}
}

// copyJourney names its containers for the round, because the run's suffix
// is shared by every round and a name can be created only once.
func copyJourney(w *world, from, to *account, round int) {
	t := w.t
	// Three small files and one of 3 MiB, segmented at 1 MiB.
	folder, digests := w.fixture(1024, 64*1024, 200*1024, 3<<20)
	src := w.container(from, fmt.Sprintf("src%d", round))
	if err := from.owned.ClaimSidecar(w.ctx, src); err != nil {
		t.Fatalf("claim the sidecar: %s", failure(err))
	}
	w.picker.dir = folder
	picked, err := w.transfer.PickUploadDirectory(w.ctx, app.SelectionScopeDTO{BookmarkID: from.bookmarkID, Container: src})
	if err != nil || picked == nil {
		t.Fatalf("pick the folder: %v", failure(err))
	}
	settings, kind := app.TransferSettingsDTO{ChunkSize: "1Mi"}, swiftcontrol.ClassSLO
	switch {
	case round%2 == 0:
		settings.LargeObjects, kind = "dlo", swiftcontrol.ClassDLO
	case from.label == "ceph":
		// The Ceph RGW cloud does not return static manifests, so a static
		// choice is published dynamic there (ADR 0111 decision 10).
		kind = swiftcontrol.ClassDLO
	}
	id, err := w.transfer.StartUpload(w.ctx, app.UploadRequestDTO{
		BookmarkID: from.bookmarkID, Container: src, SelectionToken: picked.Token, Settings: settings,
	})
	if err != nil {
		t.Fatalf("upload: %s", failure(err))
	}
	w.ended("upload", w.await(from, id), "complete")
	prefix := filepath.Base(folder) + "/"
	if diff := w.settled(func() string { return same(w.stored(from, src, prefix), digests) }); diff != "" {
		t.Fatalf("uploaded files differ from the local ones: %s", diff)
	}
	detail, err := from.check.ObjectDetail(w.ctx, src, prefix+"file03.bin")
	if err != nil {
		t.Fatalf("read the segmented file: %s", failure(err))
	}
	if detail.Class != kind {
		t.Fatalf("the segmented file is a %s object, want %s", detail.Class, kind)
	}

	// Two of the files get metadata of their own, one of them the segmented
	// file. The engine does not carry it, so the copy sets it in a request
	// of its own, which for an object the engine wrote in segments has to
	// leave that object whole.
	tagged := []string{"file00.bin", "file03.bin"}
	for _, name := range tagged {
		before, err := from.check.ObjectDetail(w.ctx, src, prefix+name)
		if err != nil {
			t.Fatalf("read %s before tagging it: %s", name, failure(err))
		}
		meta := map[string]string{"Journey": "copied"}
		for key, value := range before.Meta {
			meta[key] = value
		}
		if _, err := from.check.UpdateObjectMetadata(w.ctx, src, prefix+name, "", swiftcontrol.ObjectMetadataEdit{Meta: meta}); err != nil {
			t.Fatalf("tag %s: %s", name, failure(err))
		}
	}

	// The whole container, across clouds, at a chunk size that makes the
	// engine write the 3 MiB file in segments at the destination too.
	dst := w.container(to, fmt.Sprintf("dst%d", round))
	if err := to.owned.ClaimSidecar(w.ctx, dst); err != nil {
		t.Fatalf("claim the sidecar: %s", failure(err))
	}
	copyRequest := app.CopyRequestDTO{SourceBookmarkID: from.bookmarkID, SourceContainer: src, DestBookmarkID: to.bookmarkID, DestContainer: dst,
		Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"}}
	if _, err := w.transfer.PlanCopy(w.ctx, copyRequest); err != nil {
		t.Fatalf("preview the copy: %s", failure(err))
	}
	if id, err = w.transfer.StartCopy(w.ctx, copyRequest); err != nil {
		t.Fatalf("copy: %s", failure(err))
	}
	w.ended("copy", w.await(to, id), "complete")
	if diff := w.settled(func() string { return same(w.stored(to, dst, prefix), digests) }); diff != "" {
		t.Fatalf("copied files differ from the source: %s", diff)
	}
	for _, name := range tagged {
		if problem := w.settled(func() string {
			copied, err := to.check.ObjectDetail(w.ctx, dst, prefix+name)
			if err != nil {
				return failure(err)
			}
			if copied.Meta["Journey"] != "copied" {
				return fmt.Sprintf("%s arrived with metadata %v", name, copied.Meta)
			}
			return ""
		}); problem != "" {
			t.Fatal(problem)
		}
	}
	copiedLarge, err := to.check.ObjectDetail(w.ctx, dst, prefix+"file03.bin")
	if err != nil {
		t.Fatalf("read the copied segmented file: %s", failure(err))
	}
	if copiedLarge.Class != swiftcontrol.ClassDLO {
		t.Fatalf("the copied segmented file is a %s object, want the dynamic one the engine writes", copiedLarge.Class)
	}

	// The same copy again finds nothing to do: the plain files by the two
	// listings, and the segmented one by what the store says of both names,
	// which are cut at the same places.
	if id, err = w.transfer.StartCopy(w.ctx, copyRequest); err != nil {
		t.Fatalf("copy again: %s", failure(err))
	}
	again := w.await(to, id)
	w.ended("second copy", again, "complete")
	if again.Counters.ObjectsCopied != 0 {
		t.Fatalf("the second copy wrote %d objects, want none", again.Counters.ObjectsCopied)
	}

	// Two selected files, across clouds.
	chosen := w.container(to, fmt.Sprintf("sel%d", round))
	if err := to.owned.ClaimSidecar(w.ctx, chosen); err != nil {
		t.Fatalf("claim the sidecar: %s", failure(err))
	}
	members := []string{prefix + "file00.bin", prefix + "file02.bin"}
	selection := app.CopyRequestDTO{SourceBookmarkID: from.bookmarkID, SourceContainer: src, SourcePrefix: prefix,
		DestBookmarkID: to.bookmarkID, DestContainer: chosen, Members: members}
	if id, err = w.transfer.StartCopy(w.ctx, selection); err != nil {
		t.Fatalf("copy the selection: %s", failure(err))
	}
	job := w.await(to, id)
	w.ended("selection copy", job, "complete")
	if job.SelectedMembers != 2 {
		t.Fatalf("the selection copy recorded %d members, want 2", job.SelectedMembers)
	}
	want := map[string][32]byte{"file00.bin": digests["file00.bin"], "file02.bin": digests["file02.bin"]}
	if diff := w.settled(func() string { return same(w.stored(to, chosen, ""), want) }); diff != "" {
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
	w.ended("download", w.await(to, id), "complete")
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
