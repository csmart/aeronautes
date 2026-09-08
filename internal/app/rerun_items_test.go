package app

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

func TestRerunRefusesUncertainItemRecords(t *testing.T) {
	for _, mode := range []string{"not captured", "unresolved", "unverified"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			ctx := t.Context()
			source := h.transfer.connections.ActiveBookmarkID()
			destination, target := secondCluster(t, h)
			h.installEngine(nil)
			h.mock.AddContainer("source", "standard")
			target.AddContainer("target", "standard")
			version := 1
			if mode == "not captured" {
				version = 0
			}
			job, err := h.transfer.jobs.Create(ctx, jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: version,
				Source: "source/scope/", Destination: "target/target/", SourceBookmarkID: source, DestBookmarkID: destination})
			if err != nil {
				t.Fatal(err)
			}
			if version == 1 {
				intent := jobs.ItemIntent{ItemIdentity: jobs.ItemIdentity{Key: "unconfirmed",
					Source:      jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: source, Container: "source", Object: "scope/unconfirmed"},
					Destination: jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: destination, Container: "target", Object: "target/unconfirmed"},
				}, Planned: true, Admitted: true}
				if err := h.transfer.jobs.AdmitItems(ctx, job.ID, []jobs.ItemIntent{intent}); err != nil {
					t.Fatal(err)
				}
				if mode == "unverified" {
					if err := h.transfer.jobs.RecordExecution(ctx, job.ID, intent.Key, jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: 10}); err != nil {
						t.Fatal(err)
					}
				}
				result := jobs.Completion{State: jobs.StateCancelled, Audit: audit.Event{Operation: "transfer.fixture", Resource: "constructed prior facts", Result: audit.ResultFailed}}
				if err := h.transfer.jobs.FinishFromItems(ctx, job.ID, result, audit.NewStore(h.db)); err != nil {
					t.Fatal(err)
				}
			} else if err := h.transfer.jobs.Transition(ctx, job.ID, jobs.StateCancelled); err != nil {
				t.Fatal(err)
			}
			beforeSource, beforeTarget := len(h.mock.RequestLog()), len(target.RequestLog())
			id, err := h.transfer.RerunJob(ctx, job.ID)
			if id != "" {
				awaitTerminal(t, h, id)
			}
			if err == nil || id != "" || !strings.Contains(err.Error(), mode) {
				t.Errorf("uncertain item rerun was not refused before admission: id=%q err=%v mode=%s", id, err, mode)
			}
			if len(h.mock.RequestLog()) != beforeSource || len(target.RequestLog()) != beforeTarget {
				t.Error("refused rerun contacted Swift")
			}
			var count int
			if err := h.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`).Scan(&count); err != nil || count != 1 {
				t.Errorf("refused rerun created work: jobs=%d err=%v", count, err)
			}
			// Refusal must also work without any connection/pinning authority.
			// Moving the guard into StartCopy would dereference this nil service.
			h.transfer.connections = nil
			if id, err := h.transfer.RerunJob(ctx, job.ID); id != "" || err == nil || !strings.Contains(err.Error(), mode) {
				t.Fatalf("refusal depended on connections: %q %v", id, err)
			}
		})
	}
}

func TestRerunRefusesUnreadableItemsBeforeConnections(t *testing.T) {
	for _, fault := range []string{"identity_after_200", "observation_after_200", "query", "latched", "closed", "missing"} {
		t.Run(fault, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			ctx := t.Context()
			source := h.transfer.connections.ActiveBookmarkID()
			destination, _ := secondCluster(t, h)
			job, err := h.transfer.jobs.Create(ctx, jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1,
				Source: "source/scope/", Destination: "target/target/", SourceBookmarkID: source, DestBookmarkID: destination})
			if err != nil {
				t.Fatal(err)
			}
			var intents []jobs.ItemIntent
			for i := range 201 {
				key := fmt.Sprintf("same-%03d", i)
				intents = append(intents, jobs.ItemIntent{ItemIdentity: jobs.ItemIdentity{Key: key,
					Source:      jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: "source", Container: "source", Object: "scope/" + key},
					Destination: jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: "destination", Container: "target", Object: "target/" + key}}})
			}
			if err := h.transfer.jobs.AdmitItems(ctx, job.ID, intents); err != nil {
				t.Fatal(err)
			}
			for _, intent := range intents {
				if err := h.transfer.jobs.RecordExecution(ctx, job.ID, intent.Key, jobs.ItemObservation{Result: jobs.ItemSame, Effect: jobs.EffectNotAttempted}); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.transfer.jobs.FinishFromItems(ctx, job.ID, jobs.Completion{State: jobs.StateCancelled,
				Audit: audit.Event{Operation: "transfer.fixture", Resource: "constructed prior facts", Result: audit.ResultFailed}}, audit.NewStore(h.db)); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "latched":
				if err := h.transfer.jobs.AdmitItems(ctx, job.ID, intents[:1]); err == nil {
					t.Fatal("late required write did not latch")
				}
			case "closed":
				if err := h.db.Close(); err != nil {
					t.Fatal(err)
				}
			case "missing":
				job.ID = "nonexistent"
			default:
				query := `ALTER TABLE job_item_observations RENAME TO unavailable_observations`
				if fault == "identity_after_200" {
					query = `UPDATE job_items SET source_json='{"private":"unreadable fixture"}' WHERE item_key='same-200'`
				}
				if fault == "observation_after_200" {
					query = `UPDATE job_item_observations SET created_at='private unreadable fixture' WHERE item_id=(SELECT id FROM job_items WHERE item_key='same-200')`
				}
				if err := h.db.WriteTx(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, query); return err }); err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(fault, "after_200") {
					totals, err := h.transfer.jobs.ReadItemTotals(ctx, job.ID)
					if err != nil || totals.Items != 201 || totals.Unresolved != 0 || totals.Unverified != 0 {
						t.Fatalf("fixture must defeat a totals-only check: %+v %v", totals, err)
					}
				}
			}
			h.transfer.connections = nil
			if id, err := h.transfer.RerunJob(ctx, job.ID); id != "" || err != errRerunRecordsUnavailable {
				t.Fatalf("unreadable records reached connection authority or leaked diagnostics: %q %v", id, err)
			}
		})
	}
}
