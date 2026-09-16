package systemtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
)

type completionReopenResult struct {
	Before, After uploadRecoveryState
	Interrupted   int64
}

func completionReopenChild(t *testing.T) bool {
	input := os.Getenv("AERONAUTES_COMPLETION_REOPEN_INPUT")
	if input == "" {
		return false
	}
	var request struct{ Database, JobID, Output string }
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(t.Context(), request.Database)
	if err != nil {
		t.Fatal(err)
	}
	result := completionReopenResult{Before: uploadRecoveryRead(t, database, request.JobID)}
	result.Interrupted, err = jobs.NewStore(database).FailInterrupted(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result.After = uploadRecoveryRead(t, database, request.JobID)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(request.Output, data, 0600); err != nil {
		t.Fatal(err)
	}
	return true
}

func completionReopen(t *testing.T, h *Harness, id string, want uploadRecoveryState) {
	t.Helper()
	// Removing the injected refusal models a usable database at restart;
	// this is an orderly reopen, not evidence of crash or disk recovery.
	if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec("DROP TRIGGER IF EXISTS refuse_local_completion")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.CloseFind()
	if err := h.database.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	request := struct{ Database, JobID, Output string }{h.statePath, id, filepath.Join(dir, "output.json")}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestLocalCompletionRealSwift$", "-test.timeout=15s")
	child.Env = append(os.Environ(), "AERONAUTES_COMPLETION_REOPEN_INPUT="+input)
	started := time.Now().UTC().Truncate(time.Second)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("reopen: %v\n%s", err, output)
	}
	data, err = os.ReadFile(request.Output)
	if err != nil {
		t.Fatal(err)
	}
	var got completionReopenResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Before, want) {
		t.Fatalf("durable result changed on reopen: got=%+v want=%+v", got.Before, want)
	}
	wantInterrupted := int64(0)
	wantAfter := want
	if !jobs.Terminal(jobs.State(want.Job.State)) {
		wantInterrupted = 1
		job := *want.Job
		job.State = "failed"
		job.Error = "interrupted by shutdown; data and recovery records retained, remote residue unverified; counters show the last saved progress"
		if want.ItemsCaptured {
			job.Error = "interrupted by shutdown; data and recovery records retained, remote residue unverified; other counters show the last saved progress"
			job.Counters.ObjectsFailed = want.ItemTotals.Failed
		}
		finished, err := time.Parse(time.RFC3339, got.After.Job.FinishedAt)
		if err != nil || finished.Before(started) || finished.After(time.Now().UTC()) {
			t.Fatalf("invalid restart finish time: %q", got.After.Job.FinishedAt)
		}
		job.FinishedAt = got.After.Job.FinishedAt
		wantAfter.Job = &job
		if len(want.Rows) > 0 {
			wantAfter.Status = app.RecoveryStatusDTO{Jobs: 1, Records: int64(len(want.Rows))}
		}
	}
	if got.Interrupted != wantInterrupted || !reflect.DeepEqual(got.After, wantAfter) {
		t.Fatalf("restart invented/lost outcomes: got=%+v want=%+v changed=%d", got, wantAfter, wantInterrupted)
	}
}

func localCompletionRun(t *testing.T, h *Harness, direction, fault, class string) {
	t.Helper()
	container := h.Container(h.Primary, "local-completion")
	other := h.Container(h.Secondary, "untouched")
	h.Seed(h.Primary, container, "keep", []byte("primary sentinel"), nil)
	h.Seed(h.Primary, container, ".aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("secondary sentinel"), nil)
	name := "000.bin"
	if fault == "error" {
		name = "001.bin"
	}
	body := []byte("local completion bytes")
	settings := app.TransferSettingsDTO{}
	if class == "dlo" {
		body = []byte(strings.Repeat("abcdefg", 160000))
		settings.ChunkSize = "1Mi"
		h.rawPut(h.Primary, "/"+container+"_segments", nil, nil)
		t.Cleanup(func() { h.purge(h.Primary, container+"_segments") })
		h.Seed(h.Primary, container+"_segments", "outside/keep", []byte("shared store sentinel"), nil)
	}
	if direction == "download" {
		h.Seed(h.Primary, container, "live/"+name, body, nil)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root, destination := t.TempDir(), t.TempDir()
	path := seedListingLocal(t, root, name, body)
	blockedPath := seedListingLocal(t, root, "blocked.bin", []byte("unselected bytes"))
	seedListingLocal(t, destination, "keep", []byte("local destination sentinel"))
	inputBefore, destBefore := snapshotListingLocal(t, root), snapshotListingLocal(t, destination)
	proxyFault := "none"
	if fault == "error" {
		proxyFault = "mixed"
	}
	trace := uploadRecoveryProxy(t, h, container, uploadRecoveryCase{fault: proxyFault, class: class, count: 1})
	var query string
	switch fault {
	case "counters":
		query = `CREATE TRIGGER refuse_local_completion BEFORE UPDATE OF counters_json ON jobs WHEN json_extract(NEW.counters_json,'$.objectsVerified')>0 BEGIN SELECT RAISE(ABORT,'fixture final counters refusal'); END`
	case "error":
		query = `CREATE TRIGGER refuse_local_completion BEFORE UPDATE OF error ON jobs BEGIN SELECT RAISE(ABORT,'fixture error refusal'); END`
	case "terminal":
		query = `CREATE TRIGGER refuse_local_completion BEFORE UPDATE OF state ON jobs WHEN NEW.state IN ('complete','partial','failed','cancelled') BEGIN SELECT RAISE(ABORT,'fixture terminal refusal'); END`
	case "audit":
		query = `CREATE TRIGGER refuse_local_completion BEFORE INSERT ON audit_events WHEN NEW.operation LIKE 'transfer.local.%' BEGIN SELECT RAISE(ABORT,'fixture terminal audit refusal'); END`
	case "start_audit":
		query = fmt.Sprintf(`CREATE TRIGGER refuse_local_completion BEFORE INSERT ON audit_events WHEN NEW.operation='transfer.%s.start' BEGIN SELECT RAISE(ABORT,'fixture start audit refusal'); END`, direction)
	case "planning", "running", "verifying":
		query = fmt.Sprintf(`CREATE TRIGGER refuse_local_completion BEFORE UPDATE OF state ON jobs WHEN NEW.state='%s' BEGIN SELECT RAISE(ABORT,'fixture admission refusal'); END`, fault)
	case "recovery":
		query = `CREATE TRIGGER refuse_local_completion BEFORE INSERT ON job_recovery BEGIN SELECT RAISE(ABORT,'fixture recovery refusal'); END`
	}
	if query != "" {
		if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
			t.Fatal(err)
		}
	}
	app.WireFilePicker(h.Transfer, listingFilePicker{files: []string{path}, dir: destination})
	var id string
	var startErr error
	if direction == "upload" {
		picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/"})
		if err != nil {
			t.Fatal(err)
		}
		id, startErr = h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/", SelectionToken: picked.Token, Settings: settings})
	} else {
		picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/"})
		if err != nil {
			t.Fatal(err)
		}
		id, startErr = h.Transfer.StartDownload(t.Context(), app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/", DestDirToken: picked.Token})
	}
	startRefused := fault == "start_audit" || fault == "recovery"
	if startRefused {
		if startErr == nil || id != "" {
			t.Fatalf("start accounting refusal ignored: %q %v", id, startErr)
		}
		rows, err := jobs.NewStore(h.database).List(t.Context(), jobs.Filter{})
		if err != nil || len(rows) != 1 {
			t.Fatalf("admission job record: %+v %v", rows, err)
		}
		id = rows[0].ID
	} else if startErr != nil {
		t.Fatal(startErr)
	}
	unavailable := fault == "counters" || fault == "error" || fault == "terminal" || fault == "audit"
	deadline := time.Now().Add(30 * time.Second)
	for {
		visible, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
		if err != nil {
			if !unavailable || !strings.Contains(err.Error(), "final result could not be saved") {
				t.Fatalf("unexpected job error: %v", err)
			}
			if visible != nil {
				t.Fatal("unavailable read returned a current job")
			}
			break
		}
		if visible.FinishedAt != "" {
			if unavailable {
				t.Fatalf("persistence refusal produced terminal result: %+v", visible)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish or report persistence failure")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err == nil {
		t.Fatal("finished body remains cancellable")
	}
	if unavailable {
		if _, err := h.Jobs.List(t.Context(), "", false, 50); err == nil {
			t.Fatal("job list hid persistence failure")
		}
		app.WireFilePicker(h.Transfer, listingFilePicker{files: []string{blockedPath}})
		picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "blocked/"})
		if err != nil {
			t.Fatal(err)
		}
		newID, err := h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "blocked/", SelectionToken: picked.Token})
		if err == nil || newID != "" || !strings.Contains(err.Error(), "new jobs are blocked") {
			t.Fatalf("new job admitted after persistence failure: %q %v", newID, err)
		}
	}
	state := uploadRecoveryRead(t, h.database, id)
	if !state.ItemsCaptured {
		t.Fatalf("local caller used the wrong recording version: direction=%s captured=%t", direction, state.ItemsCaptured)
	}
	admitted := !startRefused && fault != "planning" && fault != "running"
	wantState, wantAudit := "complete", "applied"
	if unavailable {
		wantState, wantAudit = "verifying", ""
	} else if !admitted {
		wantState, wantAudit = "failed", "failed"
	} else if fault == "verifying" {
		wantState, wantAudit = "partial", "verify_mismatch"
	}
	wantCounters := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: int64(len(body))}
	if admitted {
		wantCounters.ObjectsCopied = 1
		if fault != "error" {
			wantCounters.BytesCopied = int64(len(body))
		}
		if !unavailable {
			wantCounters.ObjectsVerified = 1
		}
	}
	if state.Job.State != wantState || state.Job.Counters != wantCounters {
		t.Fatalf("durable snapshot differs: %+v want state=%s counters=%+v", state.Job, wantState, wantCounters)
	}
	if unavailable {
		if len(state.Audit) != 0 || state.Job.Error != "" || state.Job.FinishedAt != "" {
			t.Fatalf("partial terminal transaction: %+v", state)
		}
	} else {
		if len(state.Audit) != 1 || state.Audit[0].Operation != "transfer.local."+wantState || state.Audit[0].Resource != "job "+id || state.Audit[0].Result != wantAudit {
			t.Fatalf("job/audit disagree: %+v", state)
		}
		var auditBookmark string
		if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT bookmark_id FROM audit_events WHERE id=?", state.Audit[0].ID).Scan(&auditBookmark); err != nil || auditBookmark != h.Primary.BookmarkID {
			t.Fatalf("terminal audit authority differs: %q %v", auditBookmark, err)
		}
		if wantState != "complete" && !strings.Contains(state.Job.Error, "refusal") {
			t.Fatalf("admission/persistence reason lost: %+v", state.Job)
		}
		if wantState == "complete" && state.Job.Error != "" {
			t.Fatal("successful control acquired an error")
		}
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeB, afterB)
	assertListingLocalDelta(t, inputBefore, snapshotListingLocal(t, root), nil)
	var allowed []string
	manifest := ""
	if direction == "upload" && admitted {
		liveName := "live/" + name
		if fault == "error" {
			liveName = ".aeronautes/stage/" + id + "/batch/" + name
		}
		object, ok := afterA.Containers[container].Objects[liveName]
		if !ok {
			t.Fatalf("expected exact uploaded identity missing: %s", liveName)
		}
		manifest = object.Manifest
		allowed = append(allowed, container+"/"+liveName)
		allowed = append(allowed, uploadRecoveryVerifyObject(t, object, body, class, container+"_segments", ".aeronautes/stage/"+id+"/batch/"+name)...)
	}
	h.AssertNoCollateral(beforeA, afterA, allowed...)
	if direction == "download" && admitted {
		assertListingLocalDelta(t, destBefore, snapshotListingLocal(t, destination), map[string][]byte{name: body})
	} else {
		assertListingLocalDelta(t, destBefore, snapshotListingLocal(t, destination), nil)
	}
	if (!admitted || direction == "download") && len(trace.writeSnapshot()) != 0 {
		t.Fatalf("unadmitted remote writes: %v", trace.writeSnapshot())
	}
	var wantRows []jobs.RecoveryRecord
	if direction == "upload" && fault != "recovery" {
		wantRows = append(wantRows, jobs.RecoveryRecord{JobID: id, BookmarkID: h.Primary.BookmarkID, Kind: jobs.RecoveryStaging, Container: container, Prefix: ".aeronautes/stage/" + id + "/"})
		if class == "dlo" {
			segmentContainer, prefix, ok := strings.Cut(manifest, "/")
			if !ok {
				t.Fatal("DLO manifest missing")
			}
			for _, kind := range []jobs.RecoveryKind{jobs.RecoveryCaptured, jobs.RecoveryCommitting, jobs.RecoveryCommitted} {
				row := jobs.RecoveryRecord{JobID: id, BookmarkID: h.Primary.BookmarkID, Kind: kind, Container: segmentContainer, Prefix: prefix + "/"}
				if kind == jobs.RecoveryCommitting {
					row.Object = container + "/live/" + name
				}
				wantRows = append(wantRows, row)
			}
		}
	}
	if !reflect.DeepEqual(state.Rows, wantRows) || state.Job.RecoveryRecords != int64(len(wantRows)) {
		t.Fatalf("recovery identities differ: got=%+v want=%+v", state, wantRows)
	}
	count := trace.requestCount()
	completionReopen(t, h, id, state)
	if trace.requestCount() != count {
		t.Fatal("reopen issued remote requests")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
}

func localCompletionCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, direction := range []string{"upload", "download"} {
		faults := []string{"control", "counters", "terminal", "audit", "planning", "running", "start_audit", "verifying"}
		if direction == "upload" {
			faults = append(faults, "error", "recovery")
		}
		for _, fault := range faults {
			t.Run(direction+"_"+fault, func(t *testing.T) { localCompletionRun(t, makeHarness(t), direction, fault, "plain") })
		}
	}
	for _, fault := range []string{"control", "audit"} {
		t.Run("upload_"+fault+"_dlo", func(t *testing.T) { localCompletionRun(t, makeHarness(t), "upload", fault, "dlo") })
	}
}

func TestLocalCompletionMock(t *testing.T) {
	t.Parallel()
	localCompletionCases(t, findMockHarness)
}
func TestLocalCompletionRealSwift(t *testing.T) {
	t.Parallel()

	if completionReopenChild(t) {
		return
	}
	localCompletionCases(t, New)
}
