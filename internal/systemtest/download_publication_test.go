package systemtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

func downloadPublicationCase(t *testing.T, h *Harness, scenario string) {
	t.Helper()
	source := h.Container(h.Primary, "download-publication")
	other := h.Container(h.Secondary, "publication-untouched")
	prefix := "scope\n/"
	names := []string{"first", "second"}
	if scenario == "record_refusal" {
		prefix = "scope/"
		names = []string{"Foo/first", "foo/second", "plain"}
	}
	body := []byte("bytes")
	for _, name := range names {
		h.Seed(h.Primary, source, prefix+name, body, nil)
	}
	h.Seed(h.Primary, source, "outside", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other account"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root := t.TempDir()
	seedListingLocal(t, root, "selected/keep", []byte("destination sentinel"))
	seedListingLocal(t, root, "selected/.aeronautes-stage-other/keep", []byte("other job"))
	seedListingLocal(t, root, "outside/keep", []byte("unselected local"))
	beforeLocal := snapshotListingLocal(t, root)
	destination := filepath.Join(root, "selected")
	var earlyChecks atomic.Int32
	trace := downloadRecordsProxy(t, h, source, prefix, nil, func(name string) {
		if name != names[1] {
			return
		}
		earlyChecks.Add(1)
		var execution, verification string
		err := h.database.Reader().QueryRowContext(t.Context(), `SELECT e.result, v.result FROM job_items i
			JOIN job_item_observations e ON e.item_id=i.id AND e.phase='execution'
			JOIN job_item_observations v ON v.item_id=i.id AND v.phase='verification'
			WHERE i.item_key=?`, prefix+names[0]).Scan(&execution, &verification)
		if err != nil || execution != "copied" || verification != "verified" {
			t.Errorf("next native GET preceded acknowledged early verification: execution=%q verification=%q error=%v", execution, verification, err)
		}
		if scenario == "substituted" {
			path := filepath.Join(destination, names[0])
			if err := os.Remove(path); err != nil {
				t.Errorf("remove first publication: %v", err)
				return
			}
			if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
				t.Errorf("substitute first publication: %v", err)
			}
		}
	})
	if scenario == "late_admission" {
		downloadRecordRefusal(t, h, "native_admission")
	}
	if scenario == "record_refusal" {
		if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER refuse_final_publication BEFORE INSERT ON job_item_publications
				WHEN NEW.item_id IN (SELECT id FROM job_items WHERE item_key='scope/Foo/first')
				BEGIN SELECT RAISE(ABORT,'fixture publication record refusal'); END`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if scenario == "cancelled" {
		app.WireDownloadPlacementBoundary(h.Transfer, func(_ context.Context, jobID string, done, _ int64) {
			if done == 1 {
				if err := h.Transfer.CancelJob(context.Background(), jobID); err != nil {
					t.Errorf("cancel after first publication: %v", err)
				}
			}
		})
	}
	id := downloadCancelStart(t, h, source, prefix, destination)
	copyCompletionAwaitPins(t, h, h.Primary)
	saved := uploadRecoveryRead(t, h.database, id)
	copied, verified, items := int64(2), int64(2), int64(2)
	state := "complete"
	totals := jobs.ItemTotals{Items: 2, Planned: 2, Admitted: 2}
	switch scenario {
	case "substituted":
		state, verified = "partial", 1
		totals.Failed, totals.Unresolved, totals.Unverified, totals.Problems = 1, 1, 1, 1
	case "late_admission", "cancelled":
		state, copied, verified, items = "partial", 1, 1, 1
		totals = jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1}
	case "record_refusal":
		state, items = "partial", 3
		totals = jobs.ItemTotals{Items: 3, Planned: 3, Admitted: 3, Unverified: 1, Problems: 1}
	}
	wantCounters := jobs.Counters{ObjectsPlanned: int64(len(names)), BytesPlanned: int64(len(names) * len(body)),
		ObjectsCopied: copied, ObjectsVerified: verified, BytesCopied: copied * int64(len(body)), NativeRouted: copied, ObjectsFailed: totals.Failed}
	if saved.Job.State != state || saved.Job.Counters != wantCounters || saved.ItemTotals != totals || int64(len(saved.Items)) != items || !saved.ItemsCaptured {
		t.Fatalf("final publication lost exact observed facts: job=%+v totals=%+v items=%+v want=%s %+v %+v", saved.Job, saved.ItemTotals, saved.Items, state, wantCounters, totals)
	}
	wantEarlyChecks := int32(1)
	if copied == 1 {
		wantEarlyChecks = 0
	}
	if earlyChecks.Load() != wantEarlyChecks {
		t.Fatalf("fixture missed the early acknowledgement boundary: %d", earlyChecks.Load())
	}
	byKey := map[string]jobs.ItemRecord{}
	for index, item := range saved.Items {
		if item.Key != prefix+names[index] || item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: source, Object: item.Key}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(destination, names[index])}) || item.ListedBytes != int64(len(body)) || !item.Planned || !item.Admitted {
			t.Fatalf("publication changed exact admitted identity: %+v", item)
		}
		byKey[item.Key] = item
		if item.Staging == nil || item.Staging.Kind != jobs.ItemLocal {
			t.Fatalf("publication lost the admitted staging location: %+v", item)
		}
		if scenario == "record_refusal" && index == 2 {
			if item.Staging.LocalPath != filepath.Join(destination, ".aeronautes-stage-"+id, names[index]) {
				t.Fatalf("queued engine staging identity changed: %+v", item)
			}
			// An engine file requires the final fact too (ADR 0070); one
			// never attempted must have none.
			if !item.PublicationRequired || item.Publication != nil || item.Verification != nil || item.Execution == nil || item.Execution.Result != jobs.ItemNotAttempted || item.Execution.Effect != jobs.EffectNotAttempted {
				t.Fatalf("refused publication allowed engine work or invented final evidence: %+v", item)
			}
			continue
		}
		if filepath.Dir(item.Staging.LocalPath) != filepath.Dir(item.Destination.LocalPath) || !strings.HasPrefix(filepath.Base(item.Staging.LocalPath), ".aeronautes-part-") {
			t.Fatalf("native temporary identity changed: %+v", item)
		}
		if !item.PublicationRequired || item.Execution == nil || item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(body))}) ||
			item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
			t.Fatalf("final check erased or replaced early verification: %+v", item)
		}
		if scenario == "record_refusal" && index == 0 {
			if item.Publication != nil {
				t.Fatalf("refused publication was fabricated: %+v", item)
			}
			continue
		}
		want := jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}
		if scenario == "substituted" && index == 0 {
			want = jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageVerification, Code: jobs.CodeUnverified}}
		}
		if item.Publication == nil || item.Publication.ItemObservation != want {
			t.Fatalf("final publication fact differs: %+v want=%+v", item, want)
		}
	}
	files, gets := map[string][]byte{}, map[string]int{}
	for index := range int(copied) {
		files["selected/"+names[index]] = body
		gets[names[index]] = 1
	}
	if scenario == "substituted" {
		files["selected/"+names[0]] = []byte("other")
	}
	var dirs []string
	if scenario == "record_refusal" {
		dirs = []string{"selected/Foo", "selected/foo"}
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, root), files, dirs...)
	if got := trace.selectedGets(); !reflect.DeepEqual(got, gets) {
		t.Fatalf("publication boundary permitted later GETs: %v want=%v", got, gets)
	}
	result := audit.ResultApplied
	if state == "partial" {
		result = audit.ResultVerifyMismatch
	}
	if len(saved.Audit) != 1 || saved.Audit[0].Result != string(result) ||
		saved.Audit[0].Operation != "transfer.local."+state || saved.Audit[0].Resource != "job "+id ||
		!strings.HasSuffix(saved.Audit[0].Summary, fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, 0 refusals, %d unresolved, %d unverified, %d with problems", totals.Failed, totals.Unresolved, totals.Unverified, totals.Problems)) {
		t.Fatalf("publication audit differs from retained evidence: %+v", saved.Audit)
	}
	var bookmarkID string
	if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT bookmark_id FROM audit_events WHERE id=?", saved.Audit[0].ID).Scan(&bookmarkID); err != nil || bookmarkID != h.Primary.BookmarkID {
		t.Fatalf("publication audit binding differs: %q %v", bookmarkID, err)
	}
	available := scenario != "late_admission" && scenario != "record_refusal"
	assertUploadFaultAvailability(t, h, id, available)
	if available {
		assertDownloadItemPages(t, h, id, byKey, totals)
	}
	if len(trace.traffic.writeSnapshot()) != 0 {
		t.Fatal("publication checking changed remote data")
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	requests := trace.traffic.requestCount()
	uploadRecoveryReopen(t, h, id, saved)
	if trace.traffic.requestCount() != requests {
		t.Fatal("publication facts contacted Swift during reopen")
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, root), files, dirs...)
}

func downloadPublicationCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, scenario := range []string{"control", "substituted", "late_admission", "cancelled", "record_refusal"} {
		t.Run(scenario, func(t *testing.T) { downloadPublicationCase(t, makeHarness(t), scenario) })
	}
}

func TestDownloadPublicationMock(t *testing.T) {
	t.Parallel()
	downloadPublicationCases(t, findMockHarness)
}
func TestDownloadPublicationRealSwift(t *testing.T) { t.Parallel(); downloadPublicationCases(t, New) }
