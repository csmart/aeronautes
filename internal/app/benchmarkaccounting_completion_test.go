package app

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/jobs"
)

func TestBenchmarkAccountingCompletionIsAtomic(t *testing.T) {
	for _, fault := range []string{"control", "counters", "reason", "state", "audit"} {
		t.Run(fault, func(t *testing.T) {
			fixture := newBenchmarkAccountingFixture(t, "main_bulk", "index_write")
			fixture.start(t)
			for _, barrier := range fixture.barriers {
				if _, err := barrier.Wait(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			before, err := fixture.h.jobsSvc.Get(t.Context(), fixture.h.bookmarkID(), fixture.jobID)
			if err != nil {
				t.Fatal(err)
			}
			var query string
			switch fault {
			case "counters":
				query = `CREATE TRIGGER refuse_benchmark_completion BEFORE UPDATE OF counters_json ON jobs
					WHEN json_extract(NEW.counters_json,'$.deletionAccountingIncomplete')=1
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
			refuseDeletionAccounting(t, fixture.cache)
			for _, barrier := range fixture.barriers {
				if err := barrier.Release(); err != nil {
					t.Fatal(err)
				}
			}
			fixture.join(t)
			for _, barrier := range fixture.barriers {
				if err := barrier.Err(); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "control" {
				fixture.assertOutcome(t, fixture.h.mock.RequestLog())
				return
			}
			visible, getErr := fixture.h.jobsSvc.Get(t.Context(), fixture.h.bookmarkID(), fixture.jobID)
			list, listErr := fixture.h.jobsSvc.List(t.Context(), fixture.bookmark, false, 10)
			history, historyErr := fixture.h.jobsSvc.History(t.Context(), fixture.bookmark, "", 10)
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
