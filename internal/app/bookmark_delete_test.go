package app

import (
	"context"
	"database/sql"
	"strings"
	"testing"

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
	finished := func(j jobs.Job, state jobs.State) jobs.Job {
		t.Helper()
		created, err := h.jobsSvc.jobs.Create(ctx, j)
		if err != nil {
			t.Fatal(err)
		}
		// The shortest legal path to each end: planning may complete, and
		// the other ends are reached from running.
		path := []jobs.State{jobs.StatePlanning, jobs.StateRunning, state}
		if state == jobs.StateComplete {
			path = []jobs.State{jobs.StatePlanning, state}
		}
		for _, to := range path {
			if err := h.jobsSvc.jobs.Transition(ctx, created.ID, to); err != nil {
				t.Fatal(err)
			}
		}
		return created
	}
	upload := finished(jobs.Job{Type: jobs.TypeUpload, Source: "3 local files from /home/you", Destination: "media/", DestBookmarkID: gone.ID}, jobs.StateComplete)
	copied := finished(jobs.Job{Type: jobs.TypeCopy, Source: "media/", Destination: "backup/", SourceBookmarkID: gone.ID, DestBookmarkID: gone.ID}, jobs.StateFailed)
	partial := finished(jobs.Job{Type: jobs.TypeDownload, Source: "media/", Destination: "local directory /home/you", SourceBookmarkID: gone.ID}, jobs.StatePartial)
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
	for _, want := range []jobs.Job{upload, copied, partial} {
		kept, err := h.jobsSvc.jobs.Get(ctx, want.ID)
		if err != nil {
			t.Fatalf("job %s went with its bookmark: %v", want.ID, err)
		}
		if kept.SourceBookmarkID != "" || kept.DestBookmarkID != "" || kept.Source != want.Source || kept.Destination != want.Destination {
			t.Fatalf("kept job = %+v, want its route and no bookmark", kept)
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
