package systemtest

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// ADR 0103 through the public services on both fixtures: a folder with two
// files of one base name in different subfolders and one name already live
// at the destination is picked, acknowledged and uploaded; every file lands
// at its own name under the folder, the live name is replaced through the
// unchanged overwrite protocol, nothing else in the account changes, and no
// stage is left behind.
func TestDirectoryUploadMock(t *testing.T)      { directoryUploadCases(t, findMockHarness) }
func TestDirectoryUploadRealSwift(t *testing.T) { directoryUploadCases(t, New) }

func directoryUploadCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Helper()
	h := makeHarness(t)
	container := h.Container(h.Primary, "folder-upload")
	other := h.Container(h.Secondary, "folder-untouched")
	h.Seed(h.Primary, container, "inbox/photos/b/image.jpg", []byte("the earlier bytes"), nil)
	h.Seed(h.Primary, container, "inbox/keep.txt", []byte("a neighbour"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
	beforeOther := h.SnapshotNamespace(h.Secondary)

	root := filepath.Join(t.TempDir(), "photos")
	for rel, content := range map[string]string{"a/image.jpg": "alpha", "b/image.jpg": "bravo-replaces"} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: root})
	app.WireOverwriteConfirmer(h.Transfer, &serviceConfirmer{yes: true})
	if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	scope := app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "inbox/"}
	picked, err := h.Transfer.PickUploadDirectory(t.Context(), scope)
	if err != nil || picked == nil || picked.Token == "" || len(picked.Files) != 2 {
		t.Fatalf("pick = %+v, %v", picked, err)
	}
	req := app.UploadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "inbox/",
		SelectionToken: picked.Token, AcknowledgeOverwrite: true}
	report, receipt := h.PreflightUpload(t.Context(), req)
	if receipt.err != nil || report.Rejected() || len(report.Items) != 2 {
		t.Fatalf("preflight = %+v, %v", report, receipt.err)
	}
	start := h.StartUpload(t.Context(), req)
	if start.err != nil {
		t.Fatalf("start: %v", start.err)
	}
	job := h.AwaitJob(start.jobID)
	if job.State != "complete" || job.Counters.ObjectsCopied != 2 || job.Counters.Overwrites != 1 {
		t.Fatalf("the folder did not upload whole: %s %+v", job.State, job.Counters)
	}

	// The bytes themselves, by digest, not only the names and lengths.
	after := h.Snapshot(h.Primary, container).Containers[container].Objects
	for name, want := range map[string]string{"inbox/photos/a/image.jpg": "alpha", "inbox/photos/b/image.jpg": "bravo-replaces", "inbox/keep.txt": "a neighbour"} {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(want)))
		if got, ok := after[name]; !ok || got.Size != int64(len(want)) || got.Digest != digest {
			t.Errorf("%s = %+v ok=%v, want %d bytes with digest %s", name, got, ok, len(want), digest)
		}
	}
	if len(after) != 3 {
		t.Errorf("the container holds %d objects after the upload, want the three named and no stage: %v", len(after), after)
	}
	h.AssertNoCollateral(beforeOther, h.SnapshotNamespace(h.Secondary))
}
