package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

// ADR 0128: a bookmark whose finished jobs name it, as source and as
// destination, is deleted; the jobs stay with what they recorded and no
// bookmark, and its benchmark result goes with it. A job that is still
// running refuses the deletion in ADR 0015's words.
func TestDeletingABookmarkKeepsItsFinishedJobs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	gone, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "Gone", AuthURL: h.authURL, Username: "test:tester"}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	other, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "Other", AuthURL: h.authURL, Username: "test:tester"}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	finished := func(j jobs.Job, state jobs.State) jobs.Job {
		t.Helper()
		created, err := h.jobsSvc.jobs.Create(ctx, j)
		if err != nil {
			t.Fatal(err)
		}
		if j.ItemRecordsVersion == 1 {
			intent := jobs.ItemIntent{ItemIdentity: jobs.ItemIdentity{Key: "retained",
				Source:      jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: gone.ID, Container: "media", Object: "retained"},
				Destination: jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: gone.ID, Container: "backup", Object: "retained"},
			}, Planned: true, Admitted: true}
			if err := h.jobsSvc.jobs.AdmitItems(ctx, created.ID, []jobs.ItemIntent{intent}); err != nil {
				t.Fatal(err)
			}
			if err := h.jobsSvc.jobs.RecordExecution(ctx, created.ID, intent.Key, jobs.ItemObservation{
				Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
				Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageSourceRead, Code: jobs.CodeForbidden, HTTPStatus: 403},
			}); err != nil {
				t.Fatal(err)
			}
		}
		// The shortest legal path to each end: planning may complete, and
		// the other ends are reached from running.
		path := []jobs.State{jobs.StatePlanning, jobs.StateRunning, state}
		if state == jobs.StateComplete {
			path = []jobs.State{jobs.StatePlanning, state}
		}
		for _, to := range path {
			var err error
			if jobs.Terminal(to) && j.ItemRecordsVersion == 1 {
				err = h.jobsSvc.jobs.FinishFromItems(ctx, created.ID, jobs.Completion{State: to,
					Audit: audit.Event{Operation: "transfer.fixture", Resource: created.ID, Result: audit.ResultFailed}}, audit.NewStore(h.db))
			} else {
				err = h.jobsSvc.jobs.Transition(ctx, created.ID, to)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		return created
	}
	upload := finished(jobs.Job{Type: jobs.TypeUpload, Source: "3 local files from /home/you", Destination: "media/", DestBookmarkID: gone.ID}, jobs.StateComplete)
	copied := finished(jobs.Job{Type: jobs.TypeCopy, Source: "media/", Destination: "backup/", SourceBookmarkID: gone.ID, DestBookmarkID: gone.ID}, jobs.StateFailed)
	partial := finished(jobs.Job{Type: jobs.TypeDownload, Source: "media/", Destination: "local directory /home/you", SourceBookmarkID: gone.ID}, jobs.StatePartial)
	recorded := finished(jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1, Source: "media/", Destination: "backup/", SourceBookmarkID: gone.ID, DestBookmarkID: gone.ID}, jobs.StateCancelled)
	attached := finished(jobs.Job{Type: jobs.TypeCopy, Source: "media/", Destination: "backup/", SourceBookmarkID: gone.ID, DestBookmarkID: other.ID}, jobs.StateComplete)
	if err := h.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO benchmark_results (bookmark_id, ran_at, results_json, recommendation_json) VALUES (?, '2026-11-19T00:00:00Z', '{}', '{}')`, gone.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	running, err := h.jobsSvc.jobs.Create(ctx, jobs.Job{Type: jobs.TypeDownload, Source: "media/", Destination: "local directory /home/you", SourceBookmarkID: gone.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.bookmarks.Delete(ctx, gone.ID); err == nil || !strings.Contains(err.Error(), "1 running or queued job(s) still use this bookmark") {
		t.Fatalf("deleting a bookmark a queued job uses = %v, want the refusal in words", err)
	}
	if err := h.jobsSvc.jobs.Transition(ctx, running.ID, jobs.StateCancelled); err != nil {
		t.Fatal(err)
	}

	if err := h.bookmarks.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("deleting a bookmark whose jobs have finished: %v", err)
	}
	for _, want := range []jobs.Job{upload, copied, partial, recorded, running, attached} {
		bookmark := ""
		if want.ID == attached.ID {
			bookmark = other.ID
		}
		kept, err := h.jobsSvc.Get(ctx, bookmark, want.ID)
		if err != nil {
			t.Fatalf("retained job %s is unreadable: %v", want.ID, err)
		}
		if kept.SourceBookmarkID != "" || kept.DestBookmarkID != bookmark || kept.Source != want.Source || kept.Destination != want.Destination {
			t.Fatalf("kept job = %+v, want its route and only the surviving bookmark", kept)
		}
		page, err := h.jobsSvc.Items(ctx, bookmark, want.ID, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		totals, err := h.jobsSvc.ItemTotals(ctx, bookmark, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if want.ID == recorded.ID {
			if page.Status != "available" || len(page.Items) != 1 || page.Items[0].Key != "retained" ||
				page.Items[0].Source.BookmarkID != gone.ID || page.Items[0].Execution == nil || page.Items[0].Execution.HTTPStatus != 403 ||
				totals.Status != "available" || totals.Items != 1 || totals.Failed != 1 {
				t.Fatalf("retained facts changed: page=%+v totals=%+v", page, totals)
			}
		} else if page.Status != "not_captured" || totals.Status != "not_captured" {
			t.Fatalf("historical availability changed: page=%+v totals=%+v", page, totals)
		}
		wrong := other.ID
		if bookmark != "" {
			wrong = ""
		}
		if _, err := h.jobsSvc.Get(ctx, wrong, want.ID); !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("job read with wrong bookmark %q: %v", wrong, err)
		}
	}
	if _, err := h.transfer.RerunJob(ctx, copied.ID); err == nil || !strings.Contains(err.Error(), "a bookmark it used was deleted") {
		t.Fatalf("re-running a copy whose bookmark was deleted = %v, want it refused in words", err)
	}
	var benchmarks int
	if err := h.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM benchmark_results WHERE bookmark_id = ?`, gone.ID).Scan(&benchmarks); err != nil || benchmarks != 0 {
		t.Fatalf("benchmark results left for the deleted bookmark = %d, %v", benchmarks, err)
	}
}
