package systemtest

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
)

// ADR 0104 through the public services on both fixtures: an upload of two
// folders, so two engine units, is asked to pause as soon as it has started.
// It parks at a unit head, with the engine's real child either not yet
// started or ended normally; nothing arrives at the destination while it is
// paused; a resume carries the same job to the end; and every file is at its
// own name with its own bytes.
func TestPauseResumeUploadMock(t *testing.T)      { pauseResumeUploadCases(t, findMockHarness) }
func TestPauseResumeUploadRealSwift(t *testing.T) { pauseResumeUploadCases(t, New) }

func pauseResumeUploadCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Helper()
	h := makeHarness(t)
	container := h.Container(h.Primary, "pause-resume")
	other := h.Container(h.Secondary, "pause-untouched")
	h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
	beforeOther := h.SnapshotNamespace(h.Secondary)

	root := filepath.Join(t.TempDir(), "photos")
	bodies := map[string]string{"a/image.jpg": "alpha", "b/image.jpg": "bravo"}
	for rel, content := range bodies {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: root})
	if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	scope := app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "inbox/"}
	picked, err := h.Transfer.PickUploadDirectory(t.Context(), scope)
	if err != nil || picked == nil || picked.Token == "" || len(picked.Files) != 2 {
		t.Fatalf("pick = %+v, %v", picked, err)
	}
	start := h.StartUpload(t.Context(), app.UploadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "inbox/", SelectionToken: picked.Token})
	if start.err != nil {
		t.Fatalf("start: %v", start.err)
	}
	// Asked before the job can have reached its second unit: it parks at the
	// head of the first or of the second.
	if err := h.Transfer.PauseJob(t.Context(), start.jobID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, start.jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == "paused" {
			break
		}
		if job.State == "complete" || job.State == "partial" || job.State == "failed" || job.State == "cancelled" || time.Now().After(deadline) {
			t.Fatalf("the job is %s (%q), want it paused at a unit head", job.State, job.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	paused := h.Snapshot(h.Primary, container).Containers[container].Objects
	published := 0
	for rel := range bodies {
		if _, ok := paused["inbox/photos/"+rel]; ok {
			published++
		}
	}
	if published > 1 {
		t.Fatalf("both units ran before the pause took effect: %v", paused)
	}
	time.Sleep(500 * time.Millisecond)
	if still := h.Snapshot(h.Primary, container).Containers[container].Objects; len(still) != len(paused) {
		t.Fatalf("the destination changed while the job was paused: %v then %v", paused, still)
	}

	if err := h.Transfer.ResumeJob(t.Context(), start.jobID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	job := h.AwaitJob(start.jobID)
	if job.State != "complete" || job.Counters.ObjectsCopied != 2 {
		t.Fatalf("the resumed upload did not finish whole: %s %+v %q", job.State, job.Counters, job.Error)
	}
	after := h.Snapshot(h.Primary, container).Containers[container].Objects
	for rel, want := range bodies {
		name := "inbox/photos/" + rel
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(want)))
		if got, ok := after[name]; !ok || got.Size != int64(len(want)) || got.Digest != digest {
			t.Errorf("%s = %+v ok=%v, want %d bytes with digest %s", name, got, ok, len(want), digest)
		}
	}
	if len(after) != 2 {
		t.Errorf("the container holds %d objects after the upload, want the two named and no stage: %v", len(after), after)
	}
	h.AssertNoCollateral(beforeOther, h.SnapshotNamespace(h.Secondary))
}
