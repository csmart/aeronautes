package app

import (
	"fmt"
	"os"
	"testing"
)

func TestDownloadPreflightViewDoesNotBecomeThePlan(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.connect(t)
	h.installEngine(nil)
	h.mock.AddContainer("media", "standard")
	const n = 205
	for i := range n {
		seedObject(h.mock, "media", fmt.Sprintf("scope/o-%03d.bin", i), "x", nil)
	}
	dest := t.TempDir()
	h.transfer.setFilePicker(fakePicker{dir: dest})
	bookmark := h.transfer.connections.ActiveBookmarkID()
	picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	req := DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/", DestDirToken: picked.Token}
	view, err := h.transfer.PreflightDownload(ctx, req)
	if err != nil || view == nil || view.Rejected || view.Objects != n || view.TotalBytes != n || view.ProblemCount != 0 {
		t.Fatalf("bridge view lost the complete counts: %+v %v", view, err)
	}
	if len(view.ItemSamples) != 200 || int64(len(view.ItemSamples)) == view.Objects {
		t.Fatalf("bridge view did not bound the sample: samples=%d objects=%d", len(view.ItemSamples), view.Objects)
	}
	view.ItemSamples = view.ItemSamples[:1]
	id, err := h.transfer.StartDownload(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.Counters.ObjectsPlanned != n || job.Counters.ObjectsCopied != n || job.Counters.ObjectsVerified != n {
		t.Fatalf("start executed the view sample: %+v", job.Counters)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	var files int
	for _, entry := range entries {
		if !entry.IsDir() {
			files++
		}
	}
	if files != n {
		t.Fatalf("placed %d files, want the complete inventory %d", files, n)
	}
}
