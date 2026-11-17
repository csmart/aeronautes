package systemtest

import (
	"fmt"
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

// ADR 0074 decision 3 with the real engine: a selection one name past the
// batch bound downloads through the pinned rclone with every file in the
// chosen folder with its bytes, nothing else there, and every record
// complete. Where the names are split is pinned in the local package; this
// is the run across the bound with real children.
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
	localBefore := snapshotListingLocal(t, root)
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
	files := map[string][]byte{}
	for i := range count {
		files[fmt.Sprintf("o-%04d.bin", i)] = body
	}
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), files)
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
