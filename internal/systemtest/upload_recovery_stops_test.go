package systemtest

import (
	"crypto/md5"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

// uploadRecoveryStop uploads three files that the engine segments, two from
// one folder and one from another, so the engine is started once for each
// folder. With the "captured" fault the record of a segment path the engine
// reported cannot be written, which stops the run (ADR 0122 decision 8): the
// engine is not started for the second folder.
func uploadRecoveryStop(t *testing.T, h *Harness, fault string) {
	t.Helper()
	dst := h.Container(h.Primary, "upload-recovery-stop")
	segments := dst + "_segments"
	h.rawPut(h.Primary, "/"+segments, nil, nil)
	t.Cleanup(func() { h.purge(h.Primary, segments) })
	aliases := h.Container(h.Primary, "shared-aliases")
	other := h.Container(h.Secondary, "untouched")
	h.Seed(h.Primary, dst, "keep", []byte("destination sentinel"), nil)
	h.Seed(h.Primary, dst, ".aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
	shared := []byte("shared bytes")
	h.Seed(h.Primary, segments, "outside/0", shared, nil)
	h.Seed(h.Primary, aliases, "dlo", nil, map[string]string{"X-Object-Manifest": segments + "/outside/"})
	h.SeedSLO(h.Primary, aliases, "slo", fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/outside/0", md5.Sum(shared), len(shared)))
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root := t.TempDir()
	body := []byte(strings.Repeat("abcdefg", 160000))
	paths := []string{seedListingLocal(t, root, "group-a/first", body), seedListingLocal(t, root, "group-a/second", body), seedListingLocal(t, root, "group-b/third", body)}
	seedListingLocal(t, root, "unselected", []byte("local sentinel"))
	localBefore := snapshotListingLocal(t, root)
	if fault != "control" {
		if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TRIGGER refuse_upload_recovery BEFORE INSERT ON job_recovery WHEN NEW.kind='` + fault + `' BEGIN SELECT RAISE(ABORT,'fixture required upload recovery refusal'); END`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	trace := uploadRecoveryProxy(t, h, dst, uploadRecoveryCase{fault: "none"})
	app.WireFilePicker(h.Transfer, listingFilePicker{files: paths})
	picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: dst, Prefix: "live/"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: dst, Prefix: "live/", SelectionToken: picked.Token, Settings: app.TransferSettingsDTO{ChunkSize: "1Mi", LargeObjects: "dlo"}})
	if err != nil {
		t.Fatal(err)
	}
	copyCompletionAwaitPins(t, h, h.Primary)
	saved := uploadRecoveryRead(t, h.database, id)
	assertUploadRecoveryStop(t, h, fault, id, dst, body, paths, saved)
	assertUploadRecoveryStopEffects(t, h, fault, id, dst, body, beforeA, beforeB, trace, saved)
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), nil)
	requests := trace.requestCount()
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	uploadRecoveryReopen(t, h, id, saved)
	if trace.requestCount() != requests {
		t.Fatal("recovery-record refusal reopening contacted Swift")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), nil)
}

func assertUploadRecoveryStop(t *testing.T, h *Harness, fault, id, dst string, body []byte, paths []string, saved uploadRecoveryState) {
	t.Helper()
	want := jobs.ItemTotals{Items: 3, Planned: 3, Admitted: 3}
	done, state, result := int64(3), "complete", "applied"
	if fault == "captured" {
		// The engine had the first folder's two files when the record was
		// refused, and it wrote both. The third file's record carries the
		// reason it was not attempted.
		done, state, result = 2, "partial", "verify_mismatch"
		want.Problems = 1
	}
	counters := jobs.Counters{ObjectsPlanned: 3, BytesPlanned: int64(3 * len(body)), ObjectsCopied: done, ObjectsVerified: done, BytesCopied: done * int64(len(body))}
	if saved.Job.State != state || saved.Job.Counters != counters || saved.Job.FinishedAt == "" ||
		!saved.ItemsCaptured || saved.ItemTotals != want || len(saved.Items) != 3 {
		t.Fatalf("required recovery refusal lost outcomes: job=%+v totals=%+v rows=%d want=%s %+v %+v", saved.Job, saved.ItemTotals, len(saved.Items), state, counters, want)
	}
	byKey := map[string]jobs.ItemRecord{}
	for i, item := range saved.Items {
		key := "live/" + filepath.Base(paths[i])
		if item.Key != key || item.JobID != id || !item.Planned || !item.Admitted || item.ListedBytes != int64(len(body)) ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: paths[i]}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: dst, Object: key}) {
			t.Fatalf("required recovery refusal lost original identity: %+v", item)
		}
		assertUploadRecoveryStopObservation(t, item, fault, i, int64(len(body)))
		byKey[key] = item
	}
	assertUploadItemPages(t, h, id, byKey, want)
	summary := fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, 0 refusals, %d unresolved, 0 unverified, %d with problems", want.Failed, want.Unresolved, want.Problems)
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.local."+state || saved.Audit[0].Resource != "job "+id || saved.Audit[0].Result != result || saved.Audit[0].Summary != summary {
		t.Fatalf("required recovery refusal lost matching terminal audit: %+v", saved.Audit)
	}
	var bookmark string
	if err := h.database.Reader().QueryRow("SELECT bookmark_id FROM audit_events WHERE id=?", saved.Audit[0].ID).Scan(&bookmark); err != nil || bookmark != h.Primary.BookmarkID {
		t.Fatalf("required recovery refusal changed audit authority: %q %v", bookmark, err)
	}
	if h.jobStore.Available() != nil || fault != "control" && !strings.Contains(saved.Job.Error, "fixture required upload recovery refusal") || fault == "control" && saved.Job.Error != "" {
		t.Fatalf("recovery refusal disappeared or manufactured an item-store latch: %q %v", saved.Job.Error, h.jobStore.Available())
	}
}

// Each file has one outcome. A file the engine reported as copied is
// written, whether or not the run stopped afterwards, and the file of the
// folder the engine was never started for was not attempted.
func assertUploadRecoveryStopObservation(t *testing.T, item jobs.ItemRecord, fault string, index int, size int64) {
	t.Helper()
	execution := jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: size}
	verification := &jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}
	if fault == "captured" && index == 2 {
		execution = jobs.ItemObservation{Result: jobs.ItemNotAttempted, Effect: jobs.EffectNotAttempted,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeIncomplete}}
		verification = nil
	}
	if item.Execution == nil || item.Execution.ItemObservation != execution ||
		(item.Verification == nil) != (verification == nil) || verification != nil && item.Verification.ItemObservation != *verification {
		t.Fatalf("recovery refusal invented or erased a phase: item=%+v execution=%+v verification=%+v want=%+v %+v", item, item.Execution, item.Verification, execution, verification)
	}
}

func assertUploadRecoveryStopEffects(t *testing.T, h *Harness, fault, id, dst string, body []byte, beforeA, beforeB *oracle.AccountSnapshot, trace *uploadDeleteTrace, saved uploadRecoveryState) {
	t.Helper()
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeB, afterB)
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	wantWrites := map[string]int{}
	wantRows := map[jobs.RecoveryRecord]int{}
	var allowed []string
	for i, name := range []string{"first", "second", "third"} {
		live, found := afterA.Containers[dst].Objects["live/"+name]
		if expected := fault == "control" || i < 2; found != expected {
			t.Fatalf("required recovery refusal crossed a later effect: %s found=%t want=%t", name, found, expected)
		}
		if !found {
			continue
		}
		assertUploadDLOManifestBody(t, h.Primary, dst, "live/"+name, live.Manifest)
		allowed = append(allowed, dst+"/live/"+name)
		wantWrites["PUT "+storage.Path+"/"+dst+"/live/"+name] = 1
		segments := uploadRecoveryVerifyObject(t, live, body, "dlo", dst+"_segments", "live/"+name)
		allowed = append(allowed, segments...)
		for _, segment := range segments {
			wantWrites["PUT "+storage.Path+"/"+segment] = 1
		}
		if fault == "control" {
			container, prefix, _ := strings.Cut(live.Manifest, "/")
			wantRows[jobs.RecoveryRecord{JobID: id, BookmarkID: h.Primary.BookmarkID, Kind: jobs.RecoveryCaptured, Container: container, Prefix: prefix + "/"}] = 1
		}
	}
	h.AssertNoCollateral(beforeA, afterA, allowed...)
	gotWrites := map[string]int{}
	for _, write := range trace.writeSnapshot() {
		gotWrites[write]++
	}
	gotRows := map[jobs.RecoveryRecord]int{}
	for _, row := range saved.Rows {
		gotRows[row]++
	}
	wantStatus := app.RecoveryStatusDTO{}
	if len(wantRows) > 0 {
		wantStatus = app.RecoveryStatusDTO{Jobs: 1, Records: int64(len(wantRows))}
	}
	if !reflect.DeepEqual(gotWrites, wantWrites) || !reflect.DeepEqual(gotRows, wantRows) || saved.Job.RecoveryRecords != int64(len(wantRows)) || saved.Status != wantStatus {
		t.Fatalf("required recovery refusal changed writes or retained recovery identities: writes=%v want=%v rows=%v want=%v status=%+v", gotWrites, wantWrites, gotRows, wantRows, saved.Status)
	}
}

func assertUploadDLOManifestBody(t *testing.T, account *Account, container, name, manifest string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, account.storageURL+"/"+oracle.Escape(container)+"/"+oracle.Escape(name)+"?multipart-manifest=get", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Auth-Token", account.token)
	response, err := account.rawClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || len(body) != 0 || response.Header.Get("X-Object-Manifest") != manifest {
		t.Fatalf("raw DLO manifest differs: status=%d bytes=%d manifest=%q read=%v close=%v", response.StatusCode, len(body), response.Header.Get("X-Object-Manifest"), readErr, closeErr)
	}
}

func uploadRecoveryStopCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, fault := range []string{"captured", "control"} {
		t.Run(fault, func(t *testing.T) { uploadRecoveryStop(t, makeHarness(t), fault) })
	}
}

func TestUploadRecoveryStopMock(t *testing.T) {
	t.Parallel()
	uploadRecoveryStopCases(t, findMockHarness)
}
func TestUploadRecoveryStopRealSwift(t *testing.T) {
	t.Parallel()
	uploadRecoveryStopCases(t, New)
}
