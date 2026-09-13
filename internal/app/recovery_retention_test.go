package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

func retainedRows(t *testing.T, database *db.DB) []jobs.RecoveryRecord {
	t.Helper()
	rows, err := database.Reader().Query(`SELECT job_id, bookmark_id, kind, container, prefix, object FROM job_recovery ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []jobs.RecoveryRecord
	for rows.Next() {
		var row jobs.RecoveryRecord
		if err := rows.Scan(&row.JobID, &row.BookmarkID, &row.Kind, &row.Container, &row.Prefix, &row.Object); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func retentionOracle(t *testing.T, mock *swifttest.Server) *oracle.Oracle {
	t.Helper()
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	witness, err := oracle.Connect(server.URL+"/auth/v1.0", "test:tester", "testing")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(witness.Close)
	return witness
}

func retainedSnapshot(t *testing.T, witness *oracle.Oracle) *oracle.AccountSnapshot {
	t.Helper()
	snapshot, err := witness.SnapshotContainers("uploads", "unrelated")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestUploadRetainsFinalSweepResidue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("uploads", "standard")
	h.mock.AddContainer("unrelated", "standard")
	block := newEngineBarrier(t)
	h.installEngine(block)
	file := filepath.Join(t.TempDir(), "new.txt")
	if err := os.WriteFile(file, []byte("new upload"), 0600); err != nil {
		t.Fatal(err)
	}
	h.transfer.setFilePicker(fakePicker{files: []string{file}})
	picked, err := h.transfer.PickUploadFiles(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "uploads"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.transfer.StartUpload(ctx, UploadRequestDTO{BookmarkID: bookmark, Container: "uploads", SelectionToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { block.unblock(); awaitTerminal(t, h, id) })
	block.waitUntilEntered(t)
	prefix := ".aeronautes/stage/" + id + "/batch/residue/"
	seedObject(h.mock, "uploads", prefix+"00000000", "outside live bytes", nil)
	h.mock.PutObject("unrelated", "alias", swifttest.Object{Manifest: "uploads/" + prefix})
	seedObject(h.mock, "unrelated", "keep", "unchanged", nil)
	witness := retentionOracle(t, h.mock)
	before := retainedSnapshot(t, witness)
	facts := retainedRows(t, h.db)
	if len(facts) != 1 || facts[0].Kind != jobs.RecoveryStaging {
		t.Fatalf("missing initial recovery intent: %+v", facts)
	}
	block.unblock()
	job := awaitTerminal(t, h, id)
	if job.State != "complete" || job.Counters.ObjectsCopied != 1 {
		t.Fatalf("upload = %+v", job)
	}
	if got := retainedRows(t, h.db); !reflect.DeepEqual(got, facts) {
		t.Errorf("completed upload changed recovery evidence: %+v", got)
	}
	delta := oracle.Diff(before, retainedSnapshot(t, witness))
	want := oracle.Delta{AddedObjects: []oracle.ObjectChange{{Container: "uploads", Object: "new.txt"}}}
	if !reflect.DeepEqual(delta, want) {
		t.Fatalf("upload cleanup changed outside data: %s", delta)
	}
}

func TestRecoveryRecordsAfterProcessReopen(t *testing.T) {
	if path := os.Getenv("AERONAUTES_RECOVERY_REOPEN_TEST_DB"); path != "" {
		database, err := db.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		store := jobs.NewStore(database)
		if count, err := store.FailInterrupted(context.Background()); err != nil || count != 1 {
			t.Fatalf("startup classification = %d, %v", count, err)
		}
		// A retained historical row must work without any connection or
		// credential authority in the newly started process.
		service := NewJobService(store)
		for range 2 {
			if status, err := service.RecoveryStatus(context.Background()); err != nil || status != (RecoveryStatusDTO{Jobs: 1, Records: 3}) {
				t.Fatalf("reopened recovery status = %+v, %v", status, err)
			}
		}
		encoded, err := json.Marshal(retainedRows(t, database))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".reopened.json", encoded, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	h := newHarness(t)
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("uploads", "standard")
	h.mock.AddContainer("unrelated", "standard")
	job, err := h.transfer.jobs.Create(context.Background(), jobs.Job{Type: jobs.TypeCopy, DestBookmarkID: bookmark})
	if err != nil {
		t.Fatal(err)
	}
	prefix := ".aeronautes/stage/" + job.ID + "/"
	seedObject(h.mock, "uploads", prefix+"leaf", "outside live bytes", nil)
	h.mock.PutObject("unrelated", "alias", swifttest.Object{Manifest: "uploads/" + prefix})
	for _, row := range []jobs.RecoveryRecord{
		{JobID: job.ID, BookmarkID: bookmark, Kind: jobs.RecoveryStaging, Container: "uploads", Prefix: prefix},
		{JobID: job.ID, BookmarkID: bookmark, Kind: jobs.RecoveryCaptured, Container: "uploads", Prefix: prefix},
		{JobID: job.ID, BookmarkID: bookmark, Kind: jobs.RecoveryCommitting, Container: "uploads", Prefix: prefix, Object: "malformed"},
	} {
		if err := h.transfer.jobs.RecoveryAdd(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}
	facts := retainedRows(t, h.db)
	witness := retentionOracle(t, h.mock)
	before := retainedSnapshot(t, witness)
	requests := len(h.mock.RequestLog())
	var path string
	if err := h.db.Reader().QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestRecoveryRecordsAfterProcessReopen$", "-test.timeout=15s")
	child.Env = append(os.Environ(), "AERONAUTES_RECOVERY_REOPEN_TEST_DB="+path)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("separate-process reopen: %v\n%s", err, output)
	}
	if got := len(h.mock.RequestLog()); got != requests {
		t.Errorf("startup contacted Swift: %d requests", got-requests)
	}
	encoded, err := os.ReadFile(path + ".reopened.json")
	if err != nil {
		t.Fatal(err)
	}
	var reopened []jobs.RecoveryRecord
	if err := json.Unmarshal(encoded, &reopened); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reopened, facts) || !reflect.DeepEqual(retainedRows(t, h.db), facts) {
		t.Fatalf("reopened process lost recovery evidence: %+v", reopened)
	}
	got, err := h.jobsSvc.Get(context.Background(), job.ID)
	if err != nil || got.State != "failed" || got.Error != "interrupted by shutdown; data and recovery records retained, remote residue unverified; counters show the last saved progress" {
		t.Fatalf("interrupted job = %+v, %v", got, err)
	}
	if delta := oracle.Diff(before, retainedSnapshot(t, witness)); !delta.Empty() {
		t.Fatalf("process reopen changed outside data: %s", delta)
	}
}

func TestRecoveryDoesNotUseChangedBookmark(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	bookmark := h.transfer.connections.ActiveBookmarkID()
	job, err := h.transfer.jobs.Create(ctx, jobs.Job{Type: jobs.TypeCopy, DestBookmarkID: bookmark})
	if err != nil {
		t.Fatal(err)
	}
	prefix := ".aeronautes/stage/" + job.ID + "/"
	if err := h.transfer.jobs.RecoveryAdd(ctx, jobs.RecoveryRecord{JobID: job.ID, BookmarkID: bookmark, Kind: jobs.RecoveryStaging, Container: "uploads", Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	if n, err := h.transfer.jobs.FailInterrupted(ctx); err != nil || n != 1 {
		t.Fatalf("classify interrupted job = %d, %v", n, err)
	}
	other := swifttest.New("test:tester", "testing")
	server := httptest.NewServer(other)
	t.Cleanup(server.Close)
	for _, mock := range []*swifttest.Server{h.mock, other} {
		mock.AddContainer("uploads", "standard")
		mock.AddContainer("unrelated", "standard")
		seedObject(mock, "uploads", prefix+"leaf", "outside live bytes", nil)
		mock.PutObject("unrelated", "alias", swifttest.Object{Manifest: "uploads/" + prefix})
	}
	// The replacement binding has a usable credential. It still cannot
	// authorise recovery for work performed through the historical one.
	if _, err := h.bookmarks.Save(ctx, BookmarkDTO{ID: bookmark, DisplayName: "Changed", AuthURL: server.URL + "/auth/v1.0", Username: "test:tester"}, "testing"); err != nil {
		t.Fatal(err)
	}
	original, replacement := retentionOracle(t, h.mock), retentionOracle(t, other)
	beforeOriginal, beforeReplacement := retainedSnapshot(t, original), retainedSnapshot(t, replacement)
	oldRequests, newRequests := len(h.mock.RequestLog()), len(other.RequestLog())
	facts := retainedRows(t, h.db)
	var auditsBefore int
	if err := h.db.Reader().QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&auditsBefore); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if status, err := h.jobsSvc.RecoveryStatus(ctx); err != nil || status != (RecoveryStatusDTO{Jobs: 1, Records: 1}) {
			t.Fatalf("recovery status = %+v, %v", status, err)
		}
	}
	if len(h.mock.RequestLog()) != oldRequests || len(other.RequestLog()) != newRequests {
		t.Error("recovery contacted a historical or replacement endpoint")
	}
	if got := retainedRows(t, h.db); !reflect.DeepEqual(got, facts) {
		t.Errorf("recovery changed retained facts: %+v", got)
	}
	var auditsAfter int
	if err := h.db.Reader().QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&auditsAfter); err != nil || auditsAfter != auditsBefore {
		t.Errorf("recovery added an audit event: before=%d after=%d err=%v", auditsBefore, auditsAfter, err)
	}
	if delta := oracle.Diff(beforeOriginal, retainedSnapshot(t, original)); !delta.Empty() {
		t.Errorf("historical account changed: %s", delta)
	}
	if delta := oracle.Diff(beforeReplacement, retainedSnapshot(t, replacement)); !delta.Empty() {
		t.Errorf("replacement account changed: %s", delta)
	}
}

func TestRecoveryReadFailureIsNotEmpty(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.transfer.jobs.RecoveryStatus(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled recovery = %v", err)
	}
	if _, err := h.jobsSvc.RecoveryStatus(ctx); err == nil {
		t.Fatalf("cancelled recovery status = %v", err)
	}
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.transfer.jobs.RecoveryStatus(context.Background()); err == nil {
		t.Fatal("unavailable recovery database reported success")
	}
	if _, err := h.jobsSvc.RecoveryStatus(context.Background()); err == nil {
		t.Fatal("unavailable recovery database reported no retained records")
	}
	if _, err := h.transfer.jobs.FailInterrupted(context.Background()); err == nil {
		t.Fatal("unavailable startup classification reported success")
	}
}
