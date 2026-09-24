package systemtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

func TestDownloadBatchesRealChildrenMock(t *testing.T) {
	downloadBatchesRealChildren(t, findMockHarness(t))
}

func TestDownloadBatchesRealChildrenRealSwift(t *testing.T) {
	downloadBatchesRealChildren(t, New(t))
}

// ADR 0074 decision 3 on real engine children: a selection one name past
// the batch bound runs two children through the actual pinned rclone,
// every name is placed and verified, the stage is gone, and the records
// are complete.
func downloadBatchesRealChildren(t *testing.T, h *Harness) {
	t.Helper()
	count := transfercopy.DefaultBatchNames + 1
	source := h.Container(h.Primary, "download-batches")
	body := []byte("batched bytes")
	for i := range count {
		h.Seed(h.Primary, source, fmt.Sprintf("scope/o-%04d.bin", i), body, nil)
	}
	h.Seed(h.Primary, source, "outside", []byte("unselected"), nil)
	before := h.SnapshotNamespace(h.Primary)
	root := t.TempDir()
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: root})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: "scope/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	var children atomic.Int32
	app.WireDownloadStageBoundary(h.Transfer, func(context.Context, string) error { children.Add(1); return nil })
	request := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: "scope/", DestDirToken: picked.Token}
	id, err := h.Transfer.StartDownload(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h, h.Primary)
	job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
	if err != nil {
		t.Fatal(err)
	}
	n := int64(count)
	want := jobs.Counters{ObjectsPlanned: n, BytesPlanned: n * int64(len(body)), ObjectsCopied: n, BytesCopied: n * int64(len(body)), ObjectsVerified: n}
	if job.State != "complete" || job.Error != "" || job.Counters != want {
		t.Fatalf("batched download: state=%s error=%q counters=%+v want %+v", job.State, job.Error, job.Counters, want)
	}
	if children.Load() != 2 {
		t.Fatalf("engine children: %d, want 2 for %d names", children.Load(), count)
	}
	for i := range count {
		data, err := os.ReadFile(filepath.Join(root, fmt.Sprintf("o-%04d.bin", i)))
		if err != nil || string(data) != string(body) {
			t.Fatalf("o-%04d.bin after the batched download: %q %v", i, data, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, ".aeronautes-stage-"+id)); !os.IsNotExist(err) {
		t.Fatalf("stage left behind: %v", err)
	}
	saved := uploadRecoveryRead(t, h.database, id)
	if !saved.ItemsCaptured || int64(len(saved.Items)) != n || saved.ItemTotals != (jobs.ItemTotals{Items: n, Planned: n, Admitted: n}) {
		t.Fatalf("records after the batched download: captured=%v items=%d totals=%+v", saved.ItemsCaptured, len(saved.Items), saved.ItemTotals)
	}
	for _, item := range saved.Items {
		if item.Execution == nil || item.Execution.Effect != jobs.EffectReportedApplied || item.Verification == nil || item.Verification.Result != jobs.ItemVerified {
			t.Fatalf("item %s after the batched download: %+v", item.Key, item)
		}
	}
	h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary))
}
