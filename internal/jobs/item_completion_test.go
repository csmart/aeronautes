package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
)

func TestItemCompletionRefusesIncompleteFacts(t *testing.T) {
	for _, fault := range []string{"missing_execution", "failed", "conflict", "refused", "unverified_copy", "verification_failed", "uncertain_copy", "post_commit", "legacy_finish", "direct_transition"} {
		t.Run(fault, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeCopy)
			intent := transferIntent(bookmark, "retained-name")
			intent.Admitted = fault != "refused"
			if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
				t.Fatal(err)
			}
			var execution *ItemObservation
			verified := ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}
			var verification *ItemObservation
			want := ItemTotals{Items: 1, Planned: 1, Admitted: 1}
			switch fault {
			case "failed":
				problem := itemProblem(ItemFailed, StageEngine, CodeUnknown)
				problem.Effect = EffectUnresolved
				execution, verification = &problem, &verified
				want.Failed, want.Problems, want.Unresolved = 1, 1, 1
			case "conflict":
				problem := itemProblem(ItemConflict, StageCommit, CodeConflict)
				execution = &problem
				want.Conflicts, want.Problems = 1, 1
			case "refused":
				problem := itemProblem(ItemRefused, StagePlanning, CodeUnsupported)
				execution = &problem
				want.Admitted, want.Refused, want.Problems = 0, 1, 1
			case "unverified_copy", "verification_failed", "uncertain_copy", "post_commit":
				copied := ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied, CopiedBytes: 10}
				execution = &copied
				switch fault {
				case "unverified_copy":
					want.Unverified = 1
				case "verification_failed":
					problem := itemProblem(ItemFailed, StageVerification, CodeIncomplete)
					verification = &problem
					want.Unverified, want.Failed, want.Problems = 1, 1, 1
				case "uncertain_copy":
					copied.Effect = EffectUnresolved
					verification = &verified
					want.Unresolved = 1
				case "post_commit":
					copied.Diagnostic = ItemDiagnostic{Stage: StageCleanup, Code: CodeIO}
					verification = &verified
					want.Problems = 1
				}
			default:
				want.Unresolved = 1
			}
			if execution != nil {
				if err := s.RecordExecution(t.Context(), job.ID, intent.Key, *execution); err != nil {
					t.Fatal(err)
				}
			}
			if verification != nil {
				if err := s.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, *verification); err != nil {
					t.Fatal(err)
				}
			}
			if totals, err := s.ReadItemTotals(t.Context(), job.ID); err != nil || totals != want {
				t.Fatalf("wrong independent facets: %+v want=%+v %v", totals, want, err)
			}
			before, err := s.Get(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			items := readAllItems(t, s, job.ID, 0)
			trail := audit.NewStore(s.db)
			result := itemCompletion(job, StateComplete)
			switch fault {
			case "legacy_finish":
				err = s.Finish(t.Context(), job.ID, result, trail)
			case "direct_transition":
				err = s.Transition(t.Context(), job.ID, StateComplete)
			default:
				err = s.FinishFromItems(t.Context(), job.ID, result, trail)
			}
			if fault != "legacy_finish" && fault != "direct_transition" && !errors.Is(err, ErrIncompleteItemRecords) {
				t.Fatalf("contradictory complete must have a typed reason: %v", err)
			}
			assertItemUnavailable(t, s, job.ID, err)
			observer := NewStore(s.db)
			got, err := observer.Get(t.Context(), job.ID)
			if err != nil || !reflect.DeepEqual(got, before) || !reflect.DeepEqual(readAllItems(t, observer, job.ID, 0), items) {
				t.Fatalf("contradictory completion changed retained facts: %+v %v", got, err)
			}
			if events, err := trail.List(t.Context(), audit.Query{Limit: 10}); err != nil || len(events) != 0 {
				t.Fatalf("contradictory completion left a success audit: %+v %v", events, err)
			}
			// The caller can still save an honest result for already admitted work.
			result = itemCompletion(job, StatePartial)
			if err := s.FinishFromItems(t.Context(), job.ID, result, trail); err != nil {
				t.Fatalf("honest terminal salvage refused: %v", err)
			}
			got, err = observer.Get(t.Context(), job.ID)
			if err != nil || got.State != StatePartial || got.Counters.ObjectsFailed != want.Failed || got.Counters.ObjectsCopied != 1 || got.Counters.BytesCopied != 10 {
				t.Fatalf("salvage replaced established counter inputs: %+v %v", got, err)
			}
			if s.Available() == nil {
				t.Fatal("later save incorrectly cleared the process diagnostic")
			}
			events, err := trail.List(t.Context(), audit.Query{Limit: 10})
			wantSummary := fmt.Sprintf(" · item outcomes: %d failed names, %d conflicts, %d refusals, %d unresolved, %d unverified, %d with problems",
				want.Failed, want.Conflicts, want.Refused, want.Unresolved, want.Unverified, want.Problems)
			if err != nil || len(events) != 1 || events[0].Summary != wantSummary {
				t.Fatalf("terminal audit disagrees with retained facets: %+v want=%q %v", events, wantSummary, err)
			}
		})
	}
}

func TestItemCompletionTransactionRollback(t *testing.T) {
	for _, fault := range []string{"counters", "state", "audit", "retention"} {
		t.Run(fault, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeCopy)
			intent := transferIntent(bookmark, "failed")
			if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
				t.Fatal(err)
			}
			if err := s.RecordExecution(t.Context(), job.ID, intent.Key, itemProblem(ItemFailed, StageSourceRead, CodeForbidden)); err != nil {
				t.Fatal(err)
			}
			if err := s.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, itemProblem(ItemFailed, StageVerification, CodeUnverified)); err != nil {
				t.Fatal(err)
			}
			before, err := s.Get(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			items := readAllItems(t, s, job.ID, 0)
			trail := audit.NewStoreWithCap(s.db, 1)
			if err := trail.Append(t.Context(), audit.Event{Operation: "prior", Resource: "prior", Result: audit.ResultApplied}); err != nil {
				t.Fatal(err)
			}
			prior, err := trail.List(t.Context(), audit.Query{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			queries := map[string]string{
				"counters":  `CREATE TRIGGER refuse_finish BEFORE UPDATE OF counters_json ON jobs BEGIN SELECT RAISE(ABORT,'private token=fixture'); END`,
				"state":     `CREATE TRIGGER refuse_finish BEFORE UPDATE OF state ON jobs BEGIN SELECT RAISE(ABORT,'private token=fixture'); END`,
				"audit":     `CREATE TRIGGER refuse_finish BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'private token=fixture'); END`,
				"retention": `CREATE TRIGGER refuse_finish BEFORE DELETE ON audit_events BEGIN SELECT RAISE(ABORT,'private token=fixture'); END`,
			}
			itemSQL(t, s, queries[fault])
			err = s.FinishFromItems(t.Context(), job.ID, itemCompletion(job, StatePartial), trail)
			assertItemUnavailable(t, s, job.ID, err)
			if !errors.Is(err, ErrItemStorage) {
				t.Fatalf("storage failure must differ from a contradictory completion: %v", err)
			}
			observer := NewStore(s.db)
			got, err := observer.Get(t.Context(), job.ID)
			if err != nil || !reflect.DeepEqual(got, before) || !reflect.DeepEqual(items, readAllItems(t, observer, job.ID, 0)) {
				t.Fatalf("partial terminal transaction: %+v %v", got, err)
			}
			events, err := trail.List(t.Context(), audit.Query{Limit: 10})
			if err != nil || !reflect.DeepEqual(events, prior) {
				t.Fatalf("partial audit transaction: %+v %v", events, err)
			}
			itemSQL(t, s, `DROP TRIGGER refuse_finish`)
			if err := s.FinishFromItems(t.Context(), job.ID, itemCompletion(job, StatePartial), trail); err != nil {
				t.Fatalf("admitted work could not save after storage recovery: %v", err)
			}
			got, err = observer.Get(t.Context(), job.ID)
			if err != nil || got.Counters.ObjectsFailed != 1 || got.Counters.ObjectsCopied != 1 {
				t.Fatalf("terminal snapshot used caller or phase counts: %+v %v", got, err)
			}
			events, err = trail.List(t.Context(), audit.Query{Limit: 10})
			if err != nil || len(events) != 1 || events[0].Operation != "transfer.copy.partial" ||
				events[0].Summary != " · item outcomes: 1 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 1 with problems" {
				t.Fatalf("audit facets did not commit with the distinct failed counter: %+v %v", events, err)
			}
		})
	}
}

func TestItemObservationFailureRetainsIntent(t *testing.T) {
	s, bookmark := newStore(t)
	job := recordedJob(t, s, TypeCopy)
	intent := transferIntent(bookmark, "effect-may-have-landed")
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoveryAdd(t.Context(), RecoveryRecord{JobID: job.ID, BookmarkID: bookmark, Kind: RecoveryCaptured, Container: "stage", Prefix: job.ID}); err != nil {
		t.Fatal(err)
	}
	before := readAllItems(t, s, job.ID, 0)
	itemSQL(t, s, `CREATE TRIGGER refuse_observation BEFORE INSERT ON job_item_observations BEGIN SELECT RAISE(ABORT,'private token=fixture'); END`)
	copied := ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied, CopiedBytes: 10}
	err := s.RecordExecution(t.Context(), job.ID, intent.Key, copied)
	assertItemUnavailable(t, s, job.ID, err)
	observer := NewStore(s.db)
	if got := readAllItems(t, observer, job.ID, 0); !reflect.DeepEqual(got, before) {
		t.Fatal("failed observation lost intent")
	}
	if totals, err := observer.ReadItemTotals(t.Context(), job.ID); err != nil || totals.Unresolved != 1 || totals.Failed != 0 {
		t.Fatalf("missing outcome invented failure or certainty: %+v %v", totals, err)
	}
	if job, err := observer.Get(t.Context(), job.ID); err != nil || job.RecoveryRecords != 1 {
		t.Fatalf("failed observation lost recovery: %+v %v", job, err)
	}
	itemSQL(t, s, `DROP TRIGGER refuse_observation`)
	if err := s.RecordExecution(t.Context(), job.ID, intent.Key, copied); err != nil {
		t.Fatalf("admitted work cannot save remaining observation: %v", err)
	}
	if totals, err := observer.ReadItemTotals(t.Context(), job.ID); err != nil || totals.Unresolved != 0 || totals.Unverified != 1 {
		t.Fatalf("saved copy invented verification: %+v %v", totals, err)
	}
	if s.Available() == nil {
		t.Fatal("successful observation cleared failure latch")
	}
}

func TestItemTerminalAndUnrecordedWritesRefused(t *testing.T) {
	for _, operation := range []string{"admit", "execute", "compare", "finish", "legacy"} {
		t.Run(operation, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeCopy)
			intent := transferIntent(bookmark, "first")
			if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
				t.Fatal(err)
			}
			trail := audit.NewStore(s.db)
			result := itemCompletion(job, StatePartial)
			if err := s.FinishFromItems(t.Context(), job.ID, result, trail); err != nil {
				t.Fatal(err)
			}
			before := readAllItems(t, s, job.ID, 0)
			var err error
			switch operation {
			case "admit":
				err = s.AdmitItems(t.Context(), job.ID, []ItemIntent{transferIntent(bookmark, "late")})
			case "execute":
				err = s.RecordExecution(t.Context(), job.ID, intent.Key, ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied})
			case "compare":
				err = s.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted})
			case "finish":
				err = s.FinishFromItems(t.Context(), job.ID, result, trail)
			case "legacy":
				legacy, createErr := s.Create(t.Context(), Job{Type: TypeCopy})
				if createErr != nil {
					t.Fatal(createErr)
				}
				err = s.FinishFromItems(t.Context(), legacy.ID, itemCompletion(legacy, StateCancelled), trail)
				if !errors.Is(err, ErrItemRecordsNotCaptured) {
					t.Fatalf("unrecorded completion accepted: %v", err)
				}
			}
			assertItemUnavailable(t, s, job.ID, err)
			if got := readAllItems(t, NewStore(s.db), job.ID, 0); !reflect.DeepEqual(got, before) {
				t.Fatal("late write changed terminal item history")
			}
			var count int
			if err := s.db.Reader().QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("late write duplicated terminal audit: %d %v", count, err)
			}
		})
	}
}

func TestItemTerminalSummaryRejectsLateProgress(t *testing.T) {
	s, _ := newStore(t)
	job := recordedJob(t, s, TypeCopy)
	if err := s.FinishFromItems(t.Context(), job.ID, itemCompletion(job, StatePartial), audit.NewStore(s.db)); err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCounters(t.Context(), job.ID, Counters{ObjectsFailed: 999}); err == nil {
		t.Fatal("late progress replaced item-derived terminal count")
	}
	if err := s.SetError(t.Context(), job.ID, "late error"); err == nil {
		t.Fatal("late progress replaced terminal reason")
	}
	got, err := s.Get(t.Context(), job.ID)
	if err != nil || !reflect.DeepEqual(got, before) {
		t.Fatalf("terminal snapshot changed: %+v %v", got, err)
	}
}

func TestItemReadCorruptionIsUnavailable(t *testing.T) {
	for _, query := range []string{
		`UPDATE job_items SET source_json='{"kind":"swift","bookmarkId":"a","container":"c","object":"n","token":"fixture"}'`,
		`UPDATE job_items SET destination_json='not-json'`,
		`UPDATE job_items SET created_at='not-time'`,
		`UPDATE job_item_observations SET effect='invalid'`,
		`DROP TABLE job_item_observations`,
	} {
		t.Run(query, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeCopy)
			intent := transferIntent(bookmark, "one")
			if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
				t.Fatal(err)
			}
			if err := s.RecordExecution(t.Context(), job.ID, intent.Key, ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied}); err != nil {
				t.Fatal(err)
			}
			itemSQL(t, s, query)
			page, err := s.ReadItems(t.Context(), job.ID, 0, 100)
			if !errors.Is(err, ErrItemRecordsUnavailable) || page.Items != nil || page.NextCursor != 0 {
				t.Fatalf("bad stored records returned partial page: %+v %v", page, err)
			}
		})
	}
}

func TestItemFinishDoesNotReplaceTerminalDiagnostic(t *testing.T) {
	s, bookmark := newStore(t)
	job := recordedJob(t, s, TypeCopy)
	intent := transferIntent(bookmark, "name")
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(t.Context(), "missing", itemCompletion(job, StatePartial), audit.NewStore(s.db)); err == nil {
		t.Fatal("missing-job completion accepted")
	}
	prior := s.Available()
	if err := s.RecordExecution(t.Context(), job.ID, "missing", ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied}); err == nil {
		t.Fatal("missing item execution accepted")
	}
	if s.Available() != prior {
		t.Fatal("item failure replaced terminal-save diagnostic")
	}
	var count int
	if err := s.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM job_items`).Scan(&count)
	}); err != nil || count != 1 {
		t.Fatalf("failed diagnostic path changed intents: %d %v", count, err)
	}
}

// ADR 0129 decision 1: a name a cancellation stopped is incomplete, not
// failed. Its item still counts in Failed, so the job is never complete,
// and the job's counts report it apart from a real failure.
func TestItemCompletionCountsAStoppedNameIncomplete(t *testing.T) {
	s, bookmark := newStore(t)
	job := recordedJob(t, s, TypeUpload)
	stopped, broken := transferIntent(bookmark, "stopped-name"), transferIntent(bookmark, "broken-name")
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{stopped, broken}); err != nil {
		t.Fatal(err)
	}
	for key, code := range map[string]ItemCode{stopped.Key: CodeCancelled, broken.Key: CodeUnknown} {
		problem := itemProblem(ItemFailed, StageEngine, code)
		problem.Effect = EffectUnresolved
		if err := s.RecordExecution(t.Context(), job.ID, key, problem); err != nil {
			t.Fatal(err)
		}
	}
	totals, err := s.ReadItemTotals(t.Context(), job.ID)
	if err != nil || totals.Failed != 2 || totals.Incomplete != 1 {
		t.Fatalf("totals = %+v %v, want two failed executions of which one incomplete", totals, err)
	}
	if err := s.FinishFromItems(t.Context(), job.ID, itemCompletion(job, StatePartial), audit.NewStore(s.db)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(t.Context(), job.ID)
	if err != nil || got.Counters.ObjectsFailed != 1 || got.Counters.ObjectsIncomplete != 1 {
		t.Fatalf("counters = %+v %v, want one failed and one incomplete", got.Counters, err)
	}
}
