package jobs

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
)

func TestCompletionAtomicity(t *testing.T) {
	for _, fault := range []string{"control", "counters", "error", "state", "finished_at", "audit", "retention", "invalid_audit", "secret_audit", "illegal", "missing", "closed"} {
		t.Run(fault, func(t *testing.T) {
			s, bookmark := newStore(t)
			trail := audit.NewStoreWithCap(s.db, 1)
			job, err := s.Create(t.Context(), Job{Type: TypeUpload, DestBookmarkID: bookmark, Counters: Counters{ObjectsPlanned: 2, ObjectsCopied: 2, BytesCopied: 20}})
			if err != nil {
				t.Fatal(err)
			}
			for _, state := range []State{StatePlanning, StateRunning, StateVerifying} {
				if err := s.Transition(t.Context(), job.ID, state); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.SetError(t.Context(), job.ID, "previous status"); err != nil {
				t.Fatal(err)
			}
			if err := s.RecoveryAdd(t.Context(), RecoveryRecord{JobID: job.ID, BookmarkID: bookmark, Kind: RecoveryCaptured, Container: "dest", Prefix: "stage/"}); err != nil {
				t.Fatal(err)
			}
			before, err := s.Get(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := trail.Append(t.Context(), audit.Event{Operation: "prior", Resource: "sentinel", Result: audit.ResultApplied}); err != nil {
				t.Fatal(err)
			}
			prior, err := trail.List(t.Context(), audit.Query{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			var query string
			switch fault {
			case "counters", "error", "state", "finished_at":
				column := fault
				if column == "counters" {
					column = "counters_json"
				}
				query = "CREATE TRIGGER refuse_completion BEFORE UPDATE OF " + column + " ON jobs BEGIN SELECT RAISE(ABORT,'private fixture database detail'); END"
			case "audit":
				query = "CREATE TRIGGER refuse_completion BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'private fixture database detail'); END"
			case "retention":
				query = "CREATE TRIGGER refuse_completion BEFORE DELETE ON audit_events BEGIN SELECT RAISE(ABORT,'private fixture database detail'); END"
			}
			if query != "" {
				if err := s.db.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
					t.Fatal(err)
				}
			}
			result := Completion{State: StatePartial, Counters: Counters{ObjectsPlanned: 2, ObjectsCopied: 1, ObjectsVerified: 1, ObjectsFailed: 1, BytesCopied: 10}, Error: "1 failed: second object", Audit: audit.Event{BookmarkID: bookmark, Operation: "transfer.local.partial", Resource: "job " + job.ID, Result: audit.ResultVerifyMismatch}}
			id := job.ID
			switch fault {
			case "invalid_audit":
				result.Audit.Resource = ""
			case "secret_audit":
				result.Audit.Summary = "password=synthetic-fixture"
			case "illegal":
				result.State = StateRunning
			case "missing":
				id = "missing"
			case "closed":
				if err := s.db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = s.Finish(t.Context(), id, result, trail)
			// A summary the audit filter refuses costs the trail its sentence,
			// never the job its final state, so that case completes like the control.
			if fault == "control" || fault == "secret_audit" {
				if err != nil {
					t.Fatal(err)
				}
				got, err := s.Get(t.Context(), job.ID)
				if err != nil {
					t.Fatal(err)
				}
				if got.State != result.State || got.Counters != result.Counters || got.Error != result.Error || got.FinishedAt.IsZero() || got.StartedAt != before.StartedAt || got.RecoveryRecords != 1 {
					t.Fatalf("final snapshot differs: %+v", got)
				}
				events, err := trail.List(t.Context(), audit.Query{Limit: 10})
				if err != nil || len(events) != 1 || events[0].Resource != result.Audit.Resource || events[0].Result != result.Audit.Result {
					t.Fatalf("audit/retention differs: %+v %v", events, err)
				}
				if strings.Contains(events[0].Summary, "synthetic") {
					t.Fatalf("a refused summary was stored: %q", events[0].Summary)
				}
				return
			}
			if err == nil {
				t.Fatal("completion fault was ignored")
			}
			visible := s.Available()
			if visible == nil || !strings.Contains(visible.Error(), "changes may already have occurred") || strings.Contains(visible.Error(), "private") {
				t.Fatalf("unsafe diagnostic: %v", visible)
			}
			if _, err := s.Get(t.Context(), job.ID); err != visible {
				t.Fatalf("Get hid persistence error: %v", err)
			}
			if _, err := s.List(t.Context(), Filter{}); err != visible {
				t.Fatalf("List hid persistence error: %v", err)
			}
			if _, err := s.Create(t.Context(), Job{Type: TypeUpload}); err != visible {
				t.Fatalf("new job admission ignored persistence error: %v", err)
			}
			if fault == "closed" {
				return
			}
			// A fresh read-only observer examines durable state independently
			// of the failed process's diagnostic latch.
			got, err := NewStore(s.db).Get(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, before) {
				t.Fatalf("partial job commit: got=%+v before=%+v", got, before)
			}
			events, err := trail.List(t.Context(), audit.Query{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(events, prior) {
				t.Fatalf("partial audit commit: got=%+v before=%+v", events, prior)
			}
		})
	}
}

func TestCompletionTerminalCannotBeReplaced(t *testing.T) {
	s, _ := newStore(t)
	trail := audit.NewStore(s.db)
	job, err := s.Create(t.Context(), Job{Type: TypeUpload})
	if err != nil {
		t.Fatal(err)
	}
	result := Completion{State: StateCancelled, Audit: audit.Event{Operation: "transfer.local.cancelled", Resource: "job " + job.ID, Result: audit.ResultFailed}}
	if err := s.Finish(t.Context(), job.ID, result, trail); err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(t.Context(), job.ID, result, trail); err == nil {
		t.Fatal("terminal result overwritten")
	}
	got, err := NewStore(s.db).Get(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, before) {
		t.Fatal("terminal row changed")
	}
	events, err := trail.List(t.Context(), audit.Query{Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("duplicate terminal audit: %+v %v", events, err)
	}
}
