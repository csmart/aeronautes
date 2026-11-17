package systemtest

import (
	"crypto/md5"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

func downloadRecordRefusal(t *testing.T, h *Harness, mode string) {
	t.Helper()
	condition := "NEW.phase='execution'"
	table := "job_item_observations"
	if strings.HasSuffix(mode, "verification") {
		condition = "NEW.phase='verification' AND EXISTS (SELECT 1 FROM job_item_observations WHERE item_id=NEW.item_id AND phase='execution')"
	} else if strings.HasSuffix(mode, "admission") {
		table = "job_items"
		name := "second"
		if strings.Contains(mode, "_first_") {
			name = "first"
		}
		if strings.HasPrefix(mode, "directory_") || mode == "mixed_admission" {
			name += "/"
		}
		key := "'scope/" + name + "'"
		if strings.HasPrefix(mode, "native_") {
			key = "'scope'||char(10)||'/" + name + "'"
		}
		condition = "NEW.item_key=" + key
	}
	query := "CREATE TRIGGER refuse_download_item BEFORE INSERT ON " + table + " WHEN " + condition + " BEGIN SELECT RAISE(ABORT,'fixture required download record refusal'); END"
	if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
		t.Fatal(err)
	}
}

func downloadRecordFault(t *testing.T, h *Harness, mode string) {
	t.Helper()
	native, directory := strings.HasPrefix(mode, "native_"), strings.HasPrefix(mode, "directory_")
	mixed := mode == "mixed_admission"
	control := strings.HasSuffix(mode, "control")
	source := h.Container(h.Primary, "download-record-fault")
	segments := h.Container(h.Primary, "record-shared-segments")
	aliases := h.Container(h.Primary, "record-shared-aliases")
	other := h.Container(h.Secondary, "record-untouched")
	h.Seed(h.Primary, source, "outside", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other account"), nil)
	shared := []byte("shared bytes")
	h.Seed(h.Primary, segments, "shared/0", shared, nil)
	h.Seed(h.Primary, aliases, "dlo", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	manifest := fmt.Sprintf("[{\"path\":%q,\"etag\":\"%x\",\"size_bytes\":%d}]", "/"+segments+"/shared/0", md5.Sum(shared), len(shared))
	h.SeedSLO(h.Primary, aliases, "slo", manifest)
	prefix := "scope/"
	if native {
		prefix = "scope\n/"
	}
	names := []string{"first", "second", "third"}
	body := []byte("bytes")
	if directory {
		body = nil
	}
	for i := range names {
		selectedBody := body
		if directory || mixed && i == 1 {
			names[i] += "/"
			selectedBody = nil
		}
		h.Seed(h.Primary, source, prefix+names[i], selectedBody, nil)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root := t.TempDir()
	seedListingLocal(t, root, "selected/keep", []byte("existing destination"))
	seedListingLocal(t, root, "outside/keep", []byte("unselected"))
	beforeLocal := snapshotListingLocal(t, root)
	destination := filepath.Join(root, "selected")
	trace := downloadRecordsProxy(t, h, source, prefix, nil)
	if !control {
		downloadRecordRefusal(t, h, mode)
	}
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: destination})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.Transfer.StartDownload(t.Context(), app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix, DestDirToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	copyCompletionAwaitPins(t, h, h.Primary)
	saved := uploadRecoveryRead(t, h.database, id)
	count, done := int64(3), int64(1)
	state := "partial"
	firstRefusal := strings.Contains(mode, "_first_")
	admission := strings.HasSuffix(mode, "admission")
	if control {
		done, state = 3, "complete"
	} else if firstRefusal {
		count, done, state = 0, 0, "failed"
	} else if admission {
		count = 1
		if !native && !directory {
			done, state = 0, "failed"
		}
	} else if native || directory {
		count = 1
	}
	totals := jobs.ItemTotals{Items: count, Planned: count, Admitted: count}
	if !control && !admission {
		totals.Unresolved = count
		if strings.HasSuffix(mode, "verification") {
			totals.Unresolved, totals.Unverified = count-1, 1
		}
	} else if mode == "engine_admission" || mixed {
		totals.Problems = 1
	}
	counters := jobs.Counters{ObjectsPlanned: 3, BytesPlanned: int64(3 * len(body)), ObjectsCopied: done, ObjectsVerified: done, BytesCopied: done * int64(len(body))}
	if mixed {
		counters.BytesPlanned = 10
	}
	if native {
		counters.NativeRouted = done
	}
	if saved.Job.State != state || saved.Job.Counters != counters || saved.Job.FinishedAt == "" ||
		!saved.ItemsCaptured || saved.ItemTotals != totals || int64(len(saved.Items)) != count || len(saved.Rows) != 0 {
		t.Fatalf("download SQL refusal lost retained facts: %+v want=%s %+v %+v", saved, state, counters, totals)
	}
	var dirs []string
	files := map[string][]byte{}
	gets := map[string]int{}
	// The engine is given its whole batch before any of its files is
	// recorded, so a record refused after it ran leaves every file it wrote
	// in the folder, under its name (ADR 0122 decision 5).
	engineRan := !native && !directory && !admission
	for i, name := range names {
		if int64(i) < done || engineRan {
			if directory {
				dirs = append(dirs, "selected/"+strings.TrimSuffix(name, "/"))
			} else {
				files["selected/"+name] = body
			}
		}
		if !directory && (native && int64(i) < done || engineRan) {
			gets[name] = 1
		}
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, root), files, dirs...)
	if got := trace.selectedGets(); !reflect.DeepEqual(got, gets) {
		t.Fatalf("required download recording refusal allowed later object reads: got=%v want=%v", got, gets)
	}
	byKey := map[string]jobs.ItemRecord{}
	for i, item := range saved.Items {
		name := names[i]
		if item.Key != prefix+name || !item.Admitted || !item.Planned || item.ListedBytes != int64(len(body)) ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: source, Object: prefix + name}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(destination, strings.TrimSuffix(name, "/"))}) {
			t.Fatalf("download SQL refusal changed exact admitted locations: %+v", item)
		}
		if mode == "engine_admission" || mixed {
			if item.Execution == nil || item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemNotAttempted, Effect: jobs.EffectNotAttempted,
				Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeIncomplete}}) || item.Verification != nil {
				t.Fatalf("undispatched download lost positive no-effect knowledge: %+v", item)
			}
		} else {
			copied := control || admission || strings.HasSuffix(mode, "verification") && i == 0
			verified := control || admission
			if (item.Execution != nil) != copied || (item.Verification != nil) != verified {
				t.Fatalf("download SQL refusal invented or lost acknowledgement: %+v", item)
			}
			if copied && item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(body))}) ||
				verified && item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
				t.Fatalf("download SQL refusal erased earlier copied or verified facts: %+v", item)
			}
		}
		byKey[item.Key] = item
	}
	summary := fmt.Sprintf(" · item outcomes: 0 failed names, 0 conflicts, 0 refusals, %d unresolved, %d unverified, %d with problems", totals.Unresolved, totals.Unverified, totals.Problems)
	result := "verify_mismatch"
	if control {
		result = "applied"
	} else if state == "failed" {
		result = "failed"
	}
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.local."+state || saved.Audit[0].Resource != "job "+id ||
		saved.Audit[0].Result != result || saved.Audit[0].Summary != summary {
		t.Fatalf("download refusal audit differs from retained outcome: %+v want=%q", saved.Audit, summary)
	}
	var bookmark string
	if err := h.database.Reader().QueryRow("SELECT bookmark_id FROM audit_events WHERE id=?", saved.Audit[0].ID).Scan(&bookmark); err != nil || bookmark != h.Primary.BookmarkID {
		t.Fatalf("download refusal changed audit authority: %q %v", bookmark, err)
	}
	assertUploadFaultAvailability(t, h, id, control)
	if control {
		assertDownloadItemPages(t, h, id, byKey, totals)
	}
	if len(trace.traffic.writeSnapshot()) != 0 {
		t.Fatal("download changed remote data")
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec("DROP TRIGGER IF EXISTS refuse_download_item"); return err }); err != nil {
		t.Fatal(err)
	}
	if !control && h.jobStore.Available() == nil {
		t.Fatal("repairing SQL cleared the session refusal")
	}
	requests := trace.traffic.requestCount()
	uploadRecoveryReopen(t, h, id, saved)
	if trace.traffic.requestCount() != requests {
		t.Fatal("download reopen contacted Swift")
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, root), files, dirs...)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}

func downloadRecordFaultCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Run("mixed_admission", func(t *testing.T) { downloadRecordFault(t, makeHarness(t), "mixed_admission") })
	for _, route := range []string{"native", "engine", "directory"} {
		for _, fault := range []string{"first_admission", "admission", "execution", "verification", "control"} {
			t.Run(route+"_"+fault, func(t *testing.T) { downloadRecordFault(t, makeHarness(t), route+"_"+fault) })
		}
	}
}

func TestDownloadRecordRefusalMock(t *testing.T) {
	t.Parallel()
	downloadRecordFaultCases(t, findMockHarness)
}

func TestDownloadRecordRefusalRealSwift(t *testing.T) {
	t.Parallel()
	downloadRecordFaultCases(t, New)
}
