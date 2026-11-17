package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// downloadReplaceFixture is a connected harness with one object to download
// into a picked folder that already holds a file of that name.
type downloadReplaceFixture struct {
	h        *harness
	bookmark string
	dest     string
	seen     *[]rclone.Options
}

func newDownloadReplaceFixture(t *testing.T) *downloadReplaceFixture {
	t.Helper()
	h := newHarness(t)
	h.connect(t)
	f := &downloadReplaceFixture{h: h, bookmark: h.transfer.connections.ActiveBookmarkID(), dest: t.TempDir()}
	f.seen = h.installEngine(nil)
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "docs/report.txt", "the object's bytes", nil)
	if err := os.WriteFile(filepath.Join(f.dest, "report.txt"), []byte("the operator's own file"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *downloadReplaceFixture) request(t *testing.T, acknowledge bool) DownloadRequestDTO {
	t.Helper()
	f.h.transfer.setFilePicker(fakePicker{dir: f.dest})
	picked, err := f.h.transfer.PickDownloadDir(t.Context(), SelectionScopeDTO{BookmarkID: f.bookmark, Container: "media", Prefix: "docs/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	return DownloadRequestDTO{BookmarkID: f.bookmark, Container: "media", Prefix: "docs/", DestDirToken: picked.Token, AcknowledgeOverwrite: acknowledge}
}

func (f *downloadReplaceFixture) local(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dest, "report.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// ADR 0122 decision 12: without the acknowledgement a download still never
// replaces a local file: the preview refuses and names it, and nothing asks.
func TestADownloadWithoutTheAcknowledgementStillRefusesALocalFile(t *testing.T) {
	f := newDownloadReplaceFixture(t)
	confirmer := &recordingOverwriteConfirmer{yes: true}
	f.h.transfer.setOverwriteConfirmer(confirmer)
	req := f.request(t, false)
	view, err := f.h.transfer.PreflightDownload(t.Context(), req)
	if err != nil || !view.Rejected || view.Replaceable != 1 || len(view.ProblemSamples) != 1 || view.ProblemSamples[0].Name != "docs/report.txt" {
		t.Fatalf("preflight = %+v %v, want the one file refused, named, and offered for replacement", view, err)
	}
	if id, err := f.h.transfer.StartDownload(t.Context(), req); err == nil || id != "" {
		t.Fatalf("an unacknowledged download started: %q %v", id, err)
	}
	if len(confirmer.questions()) != 0 || len(*f.seen) != 0 || f.local(t) != "the operator's own file" {
		t.Fatalf("asked %d, engines %d, file %q: want nothing asked, run or changed", len(confirmer.questions()), len(*f.seen), f.local(t))
	}
}

// The one question names the folder, where the files come from and the files;
// declined, nothing is written and no job exists.
func TestADeclinedDownloadReplacementWritesNothing(t *testing.T) {
	f := newDownloadReplaceFixture(t)
	confirmer := &recordingOverwriteConfirmer{yes: false}
	f.h.transfer.setOverwriteConfirmer(confirmer)
	id, err := f.h.transfer.StartDownload(t.Context(), f.request(t, true))
	if err == nil || id != "" || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("a declined replacement started: %q %v", id, err)
	}
	asked := confirmer.questions()
	if len(asked) != 1 || asked[0].Folder != f.dest || asked[0].SourceAccount == "" || asked[0].Container != "media" ||
		len(asked[0].Names) != 1 || asked[0].Names[0] != "report.txt" {
		t.Fatalf("asked %+v, want one question naming the folder, the source account and container, and the file", asked)
	}
	jobs, err := f.h.jobsSvc.List(t.Context(), f.bookmark, false, 10)
	if err != nil || len(jobs) != 0 || len(*f.seen) != 0 || f.local(t) != "the operator's own file" {
		t.Fatalf("jobs %d (%v), engines %d, file %q: want none and the file as it was", len(jobs), err, len(*f.seen), f.local(t))
	}
}

// Confirmed, the engine is told to compare as rclone copy does, not to leave
// existing files alone, and a file it wrote over is counted as replaced.
func TestAConfirmedDownloadLetsTheEngineCompareAndCountsWhatItReplaced(t *testing.T) {
	f := newDownloadReplaceFixture(t)
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})
	id, err := f.h.transfer.StartDownload(t.Context(), f.request(t, true))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, f.h, id)
	if job.State != "complete" || job.Counters.ObjectsCopied != 1 || job.Counters.Overwrites != 1 {
		t.Fatalf("job = %+v, want complete with one file replaced", job)
	}
	if len(*f.seen) != 1 || !(*f.seen)[0].CompareExisting || (*f.seen)[0].LeaveExisting {
		t.Fatalf("engine options = %+v, want compare and not leave existing", *f.seen)
	}
	if f.local(t) != "the object's bytes" {
		t.Fatalf("the file holds %q", f.local(t))
	}
}
