package app

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func TestBenchmarkAccountingCompletionIsAtomic(t *testing.T) {
	for _, fault := range []string{"control", "counters", "reason", "state", "audit"} {
		t.Run(fault, func(t *testing.T) {
			barrier, err := swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
				Operation: t.Name(), Method: http.MethodPost, EscapedPath: "/v1/AUTH_test", RawQuery: "bulk-delete", Occurrence: 1,
			}, false, 15*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			fixture := newBenchmarkCleanupFixture(t, "bulk_no_effect", barrier)
			fixture.start(t)
			if _, err := barrier.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, err := fixture.h.jobsSvc.Get(t.Context(), fixture.h.bookmarkID(), fixture.jobID)
			if err != nil {
				t.Fatal(err)
			}
			var query string
			switch fault {
			case "counters":
				query = `CREATE TRIGGER refuse_benchmark_completion BEFORE UPDATE OF counters_json ON jobs
					BEGIN SELECT RAISE(ABORT,'fixture benchmark counters refusal'); END`
			case "reason":
				query = `CREATE TRIGGER refuse_benchmark_completion BEFORE UPDATE OF error ON jobs
					BEGIN SELECT RAISE(ABORT,'fixture benchmark reason refusal'); END`
			case "state":
				query = `CREATE TRIGGER refuse_benchmark_completion BEFORE UPDATE OF state ON jobs WHEN NEW.state='partial'
					BEGIN SELECT RAISE(ABORT,'fixture benchmark terminal refusal'); END`
			case "audit":
				query = `CREATE TRIGGER refuse_benchmark_completion BEFORE INSERT ON audit_events WHEN NEW.operation='benchmark.partial'
					BEGIN SELECT RAISE(ABORT,'fixture benchmark audit refusal'); END`
			}
			if query != "" {
				if err := fixture.h.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
					_, err := tx.ExecContext(t.Context(), query)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := barrier.Release(); err != nil {
				t.Fatal(err)
			}
			fixture.join(t)
			if err := barrier.Err(); err != nil {
				t.Fatal(err)
			}
			if fault == "control" {
				job, err := fixture.h.jobsSvc.Get(t.Context(), fixture.h.bookmarkID(), fixture.jobID)
				if err != nil || job.State != "partial" || !strings.Contains(job.Error, "unresolved deletion outcomes: 2") || job.Counters != before.Counters {
					t.Fatalf("partial completion control: %+v %v", job, err)
				}
				events, err := fixture.h.auditSvc.Search(t.Context(), AuditQueryDTO{OperationPrefix: "benchmark.partial", Limit: 10})
				if err != nil || len(events) != 1 || events[0].Resource != cleanupBenchmarkContainer || events[0].Result != "verify_mismatch" {
					t.Fatalf("terminal audit control: %+v %v", events, err)
				}
				return
			}
			visible, getErr := fixture.h.jobsSvc.Get(t.Context(), fixture.h.bookmarkID(), fixture.jobID)
			list, listErr := fixture.h.jobsSvc.List(t.Context(), fixture.h.bookmarkID(), false, 10)
			history, historyErr := fixture.h.jobsSvc.History(t.Context(), fixture.h.bookmarkID(), "", 10)
			if visible != nil || getErr == nil || list != nil || listErr == nil || historyErr == nil || len(history.Jobs) != 0 {
				t.Errorf("failed benchmark completion remained publicly available: %+v %v %+v %v %+v %v", visible, getErr, list, listErr, history, historyErr)
			}
			if getErr != nil && !strings.Contains(getErr.Error(), "final result could not be saved") {
				t.Errorf("benchmark unavailable reason: %v", getErr)
			}
			row := reopenedAccountingJob(t, fixture.h, fixture.jobID)
			if row.State != jobs.StateRunning || row.Counters != before.Counters || row.Error != before.Error || !row.FinishedAt.IsZero() {
				t.Errorf("benchmark completion transaction leaked: before=%+v reopened=%+v", before, row)
			}
			events, err := fixture.h.auditSvc.Search(t.Context(), AuditQueryDTO{OperationPrefix: "benchmark.partial", Limit: 10})
			if err != nil || len(events) != 0 {
				t.Errorf("failed benchmark completion published an audit: %+v %v", events, err)
			}
			if _, registered := fixture.h.transfer.runner.cancel(fixture.jobID); registered {
				t.Error("failed benchmark completion remains cancel-addressable")
			}
		})
	}
}

func reopenedAccountingJob(t *testing.T, h *harness, id string) jobs.Job {
	t.Helper()
	var sequence int
	var name, path string
	if err := h.db.Reader().QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	row, err := jobs.NewStore(reopened).Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return row
}
