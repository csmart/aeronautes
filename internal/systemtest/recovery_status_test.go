package systemtest

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
)

func recoveryStatusHarness(t *testing.T) (*Harness, *jobs.Store, *db.DB) {
	t.Helper()
	database, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	store := jobs.NewStore(database)
	insertFixtureBookmark(t, database, "fixture")
	// No connection service is supplied: counting local evidence must
	// remain available independently of Swift and credential authority.
	return &Harness{T: t, Jobs: app.NewJobService(store), jobStore: store, Primary: &Account{BookmarkID: "fixture"}}, store, database
}

func TestRecoveryStatusBeyondRecentJobs(t *testing.T) {
	h, store, database := recoveryStatusHarness(t)
	ctx := t.Context()
	if status, read := h.RecoveryStatus(ctx); read.Err() != nil || status != (app.RecoveryStatusDTO{}) {
		t.Fatalf("initial status = %+v, %v", status, read.Err())
	}
	cases := []struct {
		state jobs.State
		path  []jobs.State
	}{
		{jobs.StateQueued, nil},
		{jobs.StatePlanning, []jobs.State{jobs.StatePlanning}},
		{jobs.StateRunning, []jobs.State{jobs.StatePlanning, jobs.StateRunning}},
		{jobs.StateVerifying, []jobs.State{jobs.StatePlanning, jobs.StateRunning, jobs.StateVerifying}},
		{jobs.StateComplete, []jobs.State{jobs.StatePlanning, jobs.StateComplete}},
		{jobs.StatePartial, []jobs.State{jobs.StatePlanning, jobs.StateRunning, jobs.StatePartial}},
		{jobs.StateCancelled, []jobs.State{jobs.StateCancelled}},
		{jobs.StateFailed, []jobs.State{jobs.StateFailed}},
	}
	for i, test := range cases {
		job, err := store.Create(ctx, jobs.Job{Type: jobs.TypeCopy, SourceBookmarkID: h.Primary.BookmarkID})
		if err != nil {
			t.Fatal(err)
		}
		for _, to := range test.path {
			if err := store.Transition(ctx, job.ID, to); err != nil {
				t.Fatal(err)
			}
		}
		for record := range i + 1 {
			if err := store.RecoveryAdd(ctx, jobs.RecoveryRecord{
				JobID: job.ID, BookmarkID: "historical-binding", Kind: jobs.RecoveryCaptured,
				Container: "data", Prefix: fmt.Sprintf("malformed-legacy-%d", record),
			}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := h.Jobs.Get(ctx, h.Primary.BookmarkID, job.ID)
		if err != nil || got.State != string(test.state) || got.RecoveryRecords != int64(i+1) {
			t.Fatalf("per-job status = %+v, %v", got, err)
		}
	}
	all, err := h.Jobs.List(ctx, "", false, 50)
	if err != nil || len(all) != 8 {
		t.Fatalf("initial jobs = %d, %v", len(all), err)
	}
	for _, job := range all {
		if job.RecoveryRecords == 0 {
			t.Fatalf("list lost per-job count: %+v", job)
		}
	}
	if err := database.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET created_at='2000-01-01T00:00:00Z'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for range 65 {
		job, err := store.Create(ctx, jobs.Job{Type: jobs.TypeDownload, SourceBookmarkID: h.Primary.BookmarkID})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Transition(ctx, job.ID, jobs.StateCancelled); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := h.Jobs.List(ctx, "", false, 50)
	if err != nil || len(recent) != 50 {
		t.Fatalf("recent jobs = %d, %v", len(recent), err)
	}
	for _, job := range recent {
		if job.RecoveryRecords != 0 {
			t.Fatal("fixture failed to place retained jobs outside the recent window")
		}
	}
	status, read := h.RecoveryStatus(ctx)
	if read.Err() != nil || status != (app.RecoveryStatusDTO{Jobs: 4, Records: 26}) {
		t.Fatalf("global retained status = %+v, %v", status, read.Err())
	}
	encoded, err := json.Marshal(status)
	if err != nil || string(encoded) != `{"jobs":4,"records":26}` {
		t.Fatalf("bridge must expose only record/job counts: %s, %v", encoded, err)
	}
	if pruned, err := store.PruneTerminal(ctx, time.Now().Add(time.Hour)); err != nil || pruned != 65 {
		t.Fatalf("pruning retained jobs = %d, %v", pruned, err)
	}
	if after, result := h.RecoveryStatus(ctx); result.Err() != nil || after != status {
		t.Fatalf("pruning changed retained status = %+v, %v", after, result.Err())
	}
	var records int
	if err := database.Reader().QueryRow(`SELECT COUNT(*) FROM job_recovery`).Scan(&records); err != nil || records != 36 {
		t.Fatalf("status/pruning lost live or terminal facts: %d, %v", records, err)
	}
	read.CreditRead()
}

func TestRecoveryStatusRefusesUnavailableDatabase(t *testing.T) {
	h, _, database := recoveryStatusHarness(t)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	status, read := h.RecoveryStatus(t.Context())
	if read.Err() == nil || status != (app.RecoveryStatusDTO{}) {
		t.Fatalf("unavailable state claimed empty recovery: %+v, %v", status, read.Err())
	}
	if read.Err().Error() != "recovery status is unavailable" {
		t.Fatalf("unexpected bridge error: %v", read.Err())
	}
	if rows, err := h.Jobs.List(t.Context(), "", false, 50); err == nil || rows != nil {
		t.Fatalf("unavailable state claimed per-job counts: %+v, %v", rows, err)
	}
	if job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, "missing"); err == nil || job != nil {
		t.Fatalf("unavailable state returned a job count: %+v, %v", job, err)
	}
	read.CreditRefusal()
}
