package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestInterruptedItemFailuresStayDistinct(t *testing.T) {
	for _, count := range []int{1, 201, 401} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			store, _ := newStore(t)
			ctx := t.Context()
			saved := Counters{ObjectsPlanned: 503, ObjectsCopied: 37, ObjectsVerified: 29, ObjectsFailed: 400, BytesPlanned: 708, BytesCopied: 91}
			job, err := store.Create(ctx, Job{Type: TypeCopy, ItemRecordsVersion: 1, Counters: saved})
			if err != nil {
				t.Fatal(err)
			}
			for i := range count {
				key := fmt.Sprintf("failed-%04d\nidentity", i)
				intent := ItemIntent{ItemIdentity: ItemIdentity{Key: key,
					Source:      ItemLocation{Kind: ItemSwift, BookmarkID: "source", Container: "src", Object: "scope/" + key},
					Destination: ItemLocation{Kind: ItemSwift, BookmarkID: "destination", Container: "dst", Object: "target/" + key},
				}, Planned: true, Admitted: true}
				if err := store.AdmitItems(ctx, job.ID, []ItemIntent{intent}); err != nil {
					t.Fatal(err)
				}
				failure := ItemObservation{Result: ItemFailed, Effect: EffectNotAttempted, Diagnostic: ItemDiagnostic{Stage: StageSourceRead, Code: CodeForbidden, HTTPStatus: 403}}
				if err := store.RecordExecution(ctx, job.ID, key, failure); err != nil {
					t.Fatal(err)
				}
				failure.Diagnostic = ItemDiagnostic{Stage: StageVerification, Code: CodeMissing, HTTPStatus: 404}
				if err := store.RecordComparison(ctx, job.ID, intent.ItemIdentity, failure); err != nil {
					t.Fatal(err)
				}
			}
			if n, err := store.FailInterrupted(ctx); err != nil || n != 1 {
				t.Fatalf("interrupted = %d, %v", n, err)
			}
			got, err := store.Get(ctx, job.ID)
			saved.ObjectsFailed = int64(count)
			if err != nil || got.State != StateFailed || got.Counters != saved || !strings.Contains(got.Error, "last saved progress") {
				t.Fatalf("interrupted projection lost distinct failed names or progress label: job=%+v, wantCounters=%+v, err=%v", got, saved, err)
			}
			if totals, err := store.ReadItemTotals(ctx, job.ID); err != nil || totals.Failed != int64(count) {
				t.Fatalf("retained totals = %+v, %v", totals, err)
			}
		})
	}
}

func TestInterruptedProjectionAcrossJobPages(t *testing.T) {
	store, bookmark := newStore(t)
	ctx := t.Context()
	saved := Counters{ObjectsPlanned: 23, ObjectsCopied: 7, ObjectsVerified: 5, ObjectsFailed: 999, BytesCopied: 19}
	var recorded []Job
	for i := range 205 {
		kind := []Type{TypeCopy, TypeUpload, TypeDownload}[i%3]
		job, err := store.Create(ctx, Job{Type: kind, ItemRecordsVersion: 1, Counters: saved})
		if err != nil {
			t.Fatal(err)
		}
		for _, state := range []State{StatePlanning, StateRunning, StateVerifying}[:i%4] {
			if err := store.Transition(ctx, job.ID, state); err != nil {
				t.Fatal(err)
			}
		}
		recorded = append(recorded, job)
	}
	legacy, err := store.Create(ctx, Job{Type: TypeCopy, Counters: saved})
	if err != nil {
		t.Fatal(err)
	}
	done, err := store.Create(ctx, Job{Type: TypeCopy, Counters: saved})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Transition(ctx, done.ID, StateCancelled); err != nil {
		t.Fatal(err)
	}
	done, err = store.Get(ctx, done.ID)
	if err != nil {
		t.Fatal(err)
	}
	fact := RecoveryRecord{JobID: recorded[0].ID, BookmarkID: bookmark, Kind: RecoveryCaptured,
		Container: "dest", Prefix: ".staging/retained/"}
	if err := store.RecoveryAdd(ctx, fact); err != nil {
		t.Fatal(err)
	}
	recoveryRows := func() []RecoveryRecord {
		t.Helper()
		rows, err := store.db.Reader().QueryContext(ctx, `SELECT job_id, bookmark_id, kind, container, prefix, object FROM job_recovery ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []RecoveryRecord
		for rows.Next() {
			var r RecoveryRecord
			if err := rows.Scan(&r.JobID, &r.BookmarkID, &r.Kind, &r.Container, &r.Prefix, &r.Object); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if before := recoveryRows(); !reflect.DeepEqual([]RecoveryRecord{fact}, before) {
		t.Fatalf("recovery setup: %+v", before)
	}
	if n, err := store.FailInterrupted(ctx); err != nil || n != 206 {
		t.Fatalf("bounded interruption sweep: %d %v", n, err)
	}
	for _, job := range recorded {
		got, err := store.Get(ctx, job.ID)
		want := saved
		want.ObjectsFailed = 0
		if err != nil || got.State != StateFailed || got.FinishedAt.IsZero() || got.Counters != want || !strings.Contains(got.Error, "other counters show the last saved progress") {
			t.Fatalf("recorded interruption: %+v %v", got, err)
		}
	}
	got, err := store.Get(ctx, legacy.ID)
	if err != nil || got.Counters != saved || !strings.Contains(got.Error, "counters show the last saved progress") {
		t.Fatalf("legacy progress was invented: %+v %v", got, err)
	}
	if _, err := store.ReadItemTotals(ctx, legacy.ID); !errors.Is(err, ErrItemRecordsNotCaptured) {
		t.Fatalf("legacy history became captured: %v", err)
	}
	if got, err := store.Get(ctx, done.ID); err != nil || !reflect.DeepEqual(got, done) {
		t.Fatalf("existing terminal job changed: %+v %v", got, err)
	}
	if after := recoveryRows(); !reflect.DeepEqual([]RecoveryRecord{fact}, after) {
		t.Fatalf("interruption changed recovery facts: %+v", after)
	}
	var audits int
	if err := store.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("startup invented a terminal audit: %d %v", audits, err)
	}
	if n, err := store.FailInterrupted(ctx); err != nil || n != 0 {
		t.Fatalf("repeated sweep: %d %v", n, err)
	}
}

func assertInterruptedUnavailable(t *testing.T, store *Store, id string) {
	t.Helper()
	ctx := t.Context()
	if !errors.Is(store.Available(), ErrInterruptedJobsUnavailable) {
		t.Fatalf("missing interruption latch: %v", store.Available())
	}
	if job, err := store.Get(ctx, id); err == nil || job.ID != "" {
		t.Fatalf("stale job presented as current: %+v %v", job, err)
	}
	if rows, err := store.List(ctx, Filter{}); err == nil || rows != nil {
		t.Fatalf("stale list presented as current: %+v %v", rows, err)
	}
	if page, err := store.History(ctx, "", "", 0); err == nil || page.Jobs != nil {
		t.Fatalf("stale history presented as current: %+v %v", page, err)
	}
	if page, err := store.ReadItems(ctx, id, 0, 0); err == nil || page.Items != nil {
		t.Fatalf("unavailable item facts hidden: %+v %v", page, err)
	}
	if totals, err := store.ReadItemTotals(ctx, id); err == nil || totals != (ItemTotals{}) {
		t.Fatalf("unavailable item counts hidden: %+v %v", totals, err)
	}
	for _, kind := range []Type{TypeCopy, TypeUpload, TypeDownload, TypeMirror, TypeMirrorPreview, TypeDeletePrefix, TypeEmpty, TypeDeleteCont} {
		if job, err := store.Create(ctx, Job{Type: kind}); err == nil || job.ID != "" {
			t.Fatalf("interruption failure admitted %s: %+v %v", kind, job, err)
		}
	}
	if n, err := store.FailInterrupted(ctx); n != 0 || !errors.Is(err, ErrInterruptedJobsUnavailable) {
		t.Fatalf("a retry cleared the process latch: %d %v", n, err)
	}
}

func TestInterruptedProjectionRollsBackAllPages(t *testing.T) {
	for _, fault := range []string{"write_refused", "write_ignored", "counter_decode", "totals_query"} {
		t.Run(fault, func(t *testing.T) {
			store, _ := newStore(t)
			ctx := t.Context()
			var ids []string
			for range 102 {
				version := 0
				if fault == "totals_query" {
					version = 1
				}
				job, err := store.Create(ctx, Job{Type: TypeCopy, ItemRecordsVersion: version})
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, job.ID)
			}
			sort.Strings(ids)
			first, last := ids[0], ids[len(ids)-1]
			if err := store.db.WriteTx(ctx, func(tx *sql.Tx) error {
				switch fault {
				case "write_refused", "write_ignored":
					raise := "RAISE(ABORT,'private projection fixture')"
					if fault == "write_ignored" {
						raise = "RAISE(IGNORE)"
					}
					// This fault exists only after an earlier page has been updated
					// in the same transaction, proving that it must roll back too.
					_, err := tx.ExecContext(ctx, `CREATE TRIGGER refuse_interruption BEFORE UPDATE OF state ON jobs
						WHEN NEW.id='`+last+`' AND EXISTS(SELECT 1 FROM jobs WHERE id='`+first+`' AND state='failed')
						BEGIN SELECT `+raise+`; END`)
					return err
				case "counter_decode":
					_, err := tx.ExecContext(ctx, `UPDATE jobs SET counters_json='private broken JSON' WHERE id=?`, last)
					return err
				default:
					_, err := tx.ExecContext(ctx, `ALTER TABLE job_item_observations RENAME TO unavailable_observations`)
					return err
				}
			}); err != nil {
				t.Fatal(err)
			}
			if n, err := store.FailInterrupted(ctx); n != 0 || !errors.Is(err, ErrInterruptedJobsUnavailable) || strings.Contains(err.Error(), "private") {
				t.Fatalf("failed projection returned partial success or raw diagnostics: %d %v", n, err)
			}
			assertInterruptedUnavailable(t, store, first)
			var unchanged int
			if err := store.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE state='queued' AND finished_at IS NULL AND error IS NULL`).Scan(&unchanged); err != nil || unchanged != 102 {
				t.Fatalf("earlier page escaped rollback: unchanged=%d %v", unchanged, err)
			}
		})
	}
}

func TestInterruptedUnavailableDatabaseLatches(t *testing.T) {
	store, _ := newStore(t)
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := store.FailInterrupted(t.Context()); n != 0 || !errors.Is(err, ErrInterruptedJobsUnavailable) {
		t.Fatalf("closed storage projection: %d %v", n, err)
	}
	assertInterruptedUnavailable(t, store, "missing")
}

func TestInterruptedProjectionFailureLatches(t *testing.T) {
	store, _ := newStore(t)
	ctx := t.Context()
	job, err := store.Create(ctx, Job{Type: TypeCopy, ItemRecordsVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET counters_json='broken JSON' WHERE id=?`, job.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	count, err := store.FailInterrupted(ctx)
	if err == nil || count != 0 || store.Available() == nil {
		t.Fatalf("unreadable interruption was accepted: count=%d err=%v latch=%v", count, err, store.Available())
	}
	var state string
	if err := store.db.Reader().QueryRowContext(ctx, `SELECT state FROM jobs WHERE id=?`, job.ID).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("failed projection changed original state: %s, %v", state, err)
	}
	if _, err := store.Create(ctx, Job{Type: TypeDeleteCont}); err == nil {
		t.Fatal("failed interruption allowed new destructive work")
	}
}
