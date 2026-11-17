package systemtest

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

func TestDownloadDestinationOverlapMock(t *testing.T) {
	downloadDestinationOverlap(t, findMockHarness(t))
}

func TestDownloadDestinationOverlapRealSwift(t *testing.T) {
	downloadDestinationOverlap(t, New(t))
}

// ADR 0078 through the public services with a real engine child held at
// its read of the source: the running job's directory, a subdirectory and the
// parent are reported by preflight and refused by the start, a sibling is
// admitted and queues, the queued job's directory is refused in turn, and
// once both jobs have ended the directory is free again.
func downloadDestinationOverlap(t *testing.T, h *Harness) {
	t.Helper()
	// One slot, so the sibling queues behind the running job.
	settings, err := h.Settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.MaxActiveJobs = 1
	if _, err := h.Settings.Set(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	source := h.Container(h.Primary, "download-overlap")
	body := []byte("overlap bytes")
	h.Seed(h.Primary, source, "scope/one.bin", body, nil)
	h.Seed(h.Primary, source, "other/two.bin", body, nil)
	before := h.SnapshotNamespace(h.Primary)
	base := t.TempDir()
	dirA, dirB := filepath.Join(base, "a"), filepath.Join(base, "b")
	for _, dir := range []string{dirA, filepath.Join(dirA, "sub"), dirB} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pick := func(dir, prefix string) app.DownloadRequestDTO {
		t.Helper()
		app.WireFilePicker(h.Transfer, listingFilePicker{dir: dir})
		picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix})
		if err != nil || picked == nil {
			t.Fatalf("pick %s: %+v %v", dir, picked, err)
		}
		return app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix, DestDirToken: picked.Token}
	}
	refused := func(dir, prefix, job string) {
		t.Helper()
		req := pick(dir, prefix)
		view, err := h.Transfer.PreflightDownload(t.Context(), req)
		if err != nil || view == nil || !view.Rejected || len(view.ProblemSamples) == 0 || view.ProblemSamples[0].Kind != "destination-overlap" ||
			!strings.Contains(view.ProblemSamples[0].Detail, "download job "+job) {
			t.Fatalf("preflight into %s did not report the overlap with %s: %+v %v", dir, job, view, err)
		}
		if id, err := h.Transfer.StartDownload(t.Context(), req); id != "" || err == nil || !strings.Contains(err.Error(), "download job "+job) {
			t.Fatalf("start into %s was not refused naming %s: id=%q err=%v", dir, job, id, err)
		}
	}
	// The running job's engine is held at its read of the one file, so the
	// job is running while the other starts are tried.
	barrier, _, _, _ := interruptionProxy(t, h, source, http.MethodGet, "scope/one.bin", nil)
	running, err := h.Transfer.StartDownload(t.Context(), pick(dirA, "scope/"))
	if err != nil {
		t.Fatal(err)
	}
	if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
		t.Fatalf("the running job's read was not held: %+v %v", event, err)
	}
	refused(dirA, "other/", running)
	refused(filepath.Join(dirA, "sub"), "other/", running)
	refused(base, "other/", running)
	queued, err := h.Transfer.StartDownload(t.Context(), pick(dirB, "other/"))
	if err != nil {
		t.Fatalf("a sibling directory was refused: %v", err)
	}
	refused(dirB, "scope/", queued)
	if err := h.Transfer.CancelJob(t.Context(), queued); err != nil {
		t.Fatal(err)
	}
	h.AwaitJob(queued)
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	h.AwaitJob(running)
	for _, pair := range [][2]string{{running, "complete"}, {queued, "cancelled"}} {
		if job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, pair[0]); err != nil || job.State != pair[1] {
			t.Fatalf("job %s = %+v %v, want %s", pair[0], job, err, pair[1])
		}
	}
	again, err := h.Transfer.StartDownload(t.Context(), pick(dirA, "other/"))
	if err != nil {
		t.Fatalf("the finished job's directory was still refused: %v", err)
	}
	h.AwaitJob(again)
	if job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, again); err != nil || job.State != "complete" {
		t.Fatalf("download after release = %+v %v", job, err)
	}
	for _, name := range []string{"one.bin", "two.bin"} {
		if data, err := os.ReadFile(filepath.Join(dirA, name)); err != nil || string(data) != string(body) {
			t.Fatalf("%s after the downloads: %q %v", name, data, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(dirB, "two.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the cancelled queued job placed a file: %v", err)
	}
	h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary))
}
