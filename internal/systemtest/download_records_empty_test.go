package systemtest

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

func downloadEmptyRecords(t *testing.T, h *Harness, markers bool) {
	t.Helper()
	source := h.Container(h.Primary, "download-empty-records")
	other := h.Container(h.Secondary, "download-empty-other")
	h.Seed(h.Primary, source, "outside", []byte("unselected source"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other account"), nil)
	prefix := "scope\n/"
	names := map[string]string{}
	if markers {
		names = map[string]string{prefix: "", prefix + "nested/": "nested", prefix + "existing/": "existing"}
		for name := range names {
			h.Seed(h.Primary, source, name, nil, nil)
		}
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root := t.TempDir()
	seedListingLocal(t, root, "selected/keep", []byte("local sentinel"))
	seedListingLocal(t, root, "selected/existing/keep", []byte("existing directory contents"))
	seedListingLocal(t, root, "outside/keep", []byte("outside destination"))
	beforeLocal := snapshotListingLocal(t, root)
	destination := filepath.Join(root, "selected")
	trace := downloadRecordsProxy(t, h, source, prefix, nil)
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: destination})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.Transfer.StartDownload(t.Context(), app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix, DestDirToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h, h.Primary)
	saved := uploadRecoveryRead(t, h.database, id)
	count := int64(len(names))
	expected := jobs.Counters{ObjectsPlanned: count, ObjectsCopied: count, ObjectsVerified: count}
	totals := jobs.ItemTotals{Items: count, Planned: count, Admitted: count}
	if saved.Job.State != "complete" || saved.Job.Counters != expected || saved.Job.Error != "" ||
		!saved.ItemsCaptured || len(saved.Items) != len(names) || saved.ItemTotals != totals || len(saved.Rows) != 0 {
		t.Fatalf("empty scope or directory markers lost exact recorded outcomes: %+v", saved)
	}
	byKey := map[string]jobs.ItemRecord{}
	for _, item := range saved.Items {
		relative, exists := names[item.Key]
		if !exists || byKey[item.Key].ID != 0 || !item.Planned || !item.Admitted || item.ListedBytes != 0 || item.Staging != nil ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: source, Object: item.Key}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(destination, relative)}) ||
			item.Execution == nil || item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied}) ||
			item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
			t.Fatalf("directory marker identity or observed result differs: %+v", item)
		}
		byKey[item.Key] = item
	}
	assertDownloadItemPages(t, h, id, byKey, totals)
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.local.complete" || saved.Audit[0].Result != "applied" ||
		saved.Audit[0].Summary != " · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems" {
		t.Fatalf("empty download lost terminal item accounting: %+v", saved.Audit)
	}
	var dirs []string
	if markers {
		dirs = []string{"selected/nested"}
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, root), nil, dirs...)
	if len(trace.selectedGets()) != 0 || len(trace.traffic.writeSnapshot()) != 0 {
		t.Fatal("empty download or directory marker transferred remote bodies")
	}
	checkHistoricalDownloadRecordVersion(t, h)
	if after := uploadRecoveryRead(t, h.database, id); !reflect.DeepEqual(after, saved) {
		t.Fatal("historical fixture changed the new download")
	}
	requests := trace.traffic.requestCount()
	if next, err := h.Transfer.RerunJob(t.Context(), id); next != "" || err == nil {
		t.Fatal("recorded empty or directory history gained selection authority")
	}
	uploadRecoveryReopen(t, h, id, saved)
	if trace.traffic.requestCount() != requests {
		t.Fatal("orderly empty download reopen contacted Swift")
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, root), nil, dirs...)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}

func checkHistoricalDownloadRecordVersion(t *testing.T, h *Harness) {
	t.Helper()
	historical, err := h.jobStore.Create(t.Context(), jobs.Job{Type: jobs.TypeDownload, ItemRecordsVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.jobStore.Transition(t.Context(), historical.ID, jobs.StateCancelled); err != nil {
		t.Fatal(err)
	}
	page, err := h.Jobs.Items(t.Context(), historical.ID, "", 100)
	if err != nil || page.Status != "not_captured" || len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("historical version-zero download was reinterpreted as available empty history: %+v %v", page, err)
	}
	oldTotals, err := h.Jobs.ItemTotals(t.Context(), historical.ID)
	if err != nil || oldTotals != (app.JobItemTotalsDTO{Status: "not_captured"}) {
		t.Fatalf("historical totals changed: %+v %v", oldTotals, err)
	}
}

func TestDownloadEmptyRecordsMock(t *testing.T) {
	t.Parallel()
	t.Run("empty", func(t *testing.T) { downloadEmptyRecords(t, findMockHarness(t), false) })
	t.Run("markers", func(t *testing.T) { downloadEmptyRecords(t, findMockHarness(t), true) })
}

func TestDownloadEmptyRecordsRealSwift(t *testing.T) {
	t.Parallel()
	t.Run("empty", func(t *testing.T) { downloadEmptyRecords(t, New(t), false) })
	t.Run("markers", func(t *testing.T) { downloadEmptyRecords(t, New(t), true) })
}
