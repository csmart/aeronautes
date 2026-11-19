package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/local"
)

func TestLocalCompletionAdmission(t *testing.T) {
	for _, fault := range []string{"planning", "running", "verifying", "counters", "error", "terminal", "audit", "control"} {
		t.Run(fault, func(t *testing.T) {
			h := newHarness(t)
			bm := h.ensureBookmark(t)
			job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeUpload, ItemRecordsVersion: 1, DestBookmarkID: bm})
			if err != nil {
				t.Fatal(err)
			}
			query := ""
			switch fault {
			case "planning", "running", "verifying":
				query = "CREATE TRIGGER refuse_local BEFORE UPDATE OF state ON jobs WHEN NEW.state='" + fault + "' BEGIN SELECT RAISE(ABORT,'fixture admission refusal'); END"
			case "counters":
				query = "CREATE TRIGGER refuse_local BEFORE UPDATE OF counters_json ON jobs WHEN json_extract(NEW.counters_json,'$.objectsVerified')=1 BEGIN SELECT RAISE(ABORT,'fixture final counters refusal'); END"
			case "error":
				query = "CREATE TRIGGER refuse_local BEFORE UPDATE OF error ON jobs BEGIN SELECT RAISE(ABORT,'fixture error refusal'); END"
			case "terminal":
				query = "CREATE TRIGGER refuse_local BEFORE UPDATE OF state ON jobs WHEN NEW.state IN ('complete','partial') BEGIN SELECT RAISE(ABORT,'fixture terminal refusal'); END"
			case "audit":
				query = "CREATE TRIGGER refuse_local BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'fixture audit refusal'); END"
			}
			if query != "" {
				if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			h.transfer.runner.register(job.ID, cancel)
			called := false
			h.transfer.runLocal(ctx, job.ID, "", job.Counters, func(context.Context, func(int64, int64, int64, int64)) (*local.Report, error) {
				called = true
				recorder := h.transfer.uploadItemRecorder(ctx, job.ID, UploadRequestDTO{BookmarkID: bm, Container: "media"})
				item := local.UploadItem{Path: filepath.Join(t.TempDir(), "first"), Name: "first", Size: 10}
				if err := recorder.Admit(local.UploadAdmission{Item: item}); err != nil {
					t.Fatal(err)
				}
				if err := recorder.Execute(item, transfercopy.Observation{Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: 10}); err != nil {
					t.Fatal(err)
				}
				if err := recorder.Verify(item, transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}); err != nil {
					t.Fatal(err)
				}
				report := &local.Report{Done: 1, Verified: 1, Bytes: 10}
				if fault == "error" {
					report.Failures = []local.Failure{{Name: "other", Stage: "verify", Err: "size mismatch"}}
				}
				return report, nil
			})
			wantCalled := fault != "planning" && fault != "running"
			if called != wantCalled {
				t.Fatalf("body invocation=%v want=%v", called, wantCalled)
			}
			if _, active := h.transfer.runner.cancel(job.ID); active {
				t.Fatal("finished body remains cancellation-addressable")
			}
			visible, err := h.jobsSvc.Get(t.Context(), h.bookmarkID(), job.ID)
			unavailable := fault == "counters" || fault == "error" || fault == "terminal" || fault == "audit"
			if unavailable {
				if err == nil || visible != nil || !strings.Contains(err.Error(), "final result could not be saved") {
					t.Fatalf("persistence failure hidden: %+v %v", visible, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "complete"
			if !wantCalled {
				want = "failed"
			}
			if fault == "verifying" {
				want = "partial"
			}
			if visible.State != want {
				t.Fatalf("state=%s want=%s", visible.State, want)
			}
			if want != "complete" && !strings.Contains(visible.Error, "refusal") {
				t.Fatalf("lost persistence reason: %+v", visible)
			}
		})
	}
}

func TestQueuedJobHonoursPersistenceLatch(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "cancelled"}[cancelled], func(t *testing.T) {
			h := newHarness(t)
			r := h.transfer.runner
			r.setMaxActiveJobs(1)
			if !r.acquireSlot(t.Context()) {
				t.Fatal("test slot unavailable")
			}
			bm := h.ensureBookmark(t)
			job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeUpload, DestBookmarkID: bm})
			if err != nil {
				t.Fatal(err)
			}
			broken, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeUpload})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r.register(job.ID, cancel)
			released, called := make(chan struct{}), make(chan struct{}, 1)
			r.launchWithQueuedOutcome(ctx, job.ID, "", func() { close(released) }, func(context.Context) { called <- struct{}{} }, nil)
			if err := h.transfer.jobs.Finish(t.Context(), broken.ID, jobs.Completion{State: jobs.StateFailed, Audit: audit.Event{}}, r.audit); err == nil {
				t.Fatal("fixture must latch a failed completion")
			}
			if cancelled {
				cancel()
			}
			r.releaseSlot()
			select {
			case <-released:
			case <-time.After(5 * time.Second):
				t.Fatal("queued job did not release pins")
			}
			select {
			case <-called:
				t.Fatal("queued job entered body after persistence failure")
			default:
			}
			if _, active := r.cancel(job.ID); active {
				t.Fatal("queued job retained cancellation entry")
			}
			row, err := jobs.NewStore(h.db).Get(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if row.State != jobs.StateQueued || !row.StartedAt.IsZero() {
				t.Fatalf("blocked job changed: %+v", row)
			}
			events, err := r.audit.List(t.Context(), audit.Query{Limit: 10})
			if err != nil || len(events) != 0 {
				t.Fatalf("invented queued terminal audit: %+v %v", events, err)
			}
		})
	}
}

func TestLocalQueuedCompletion(t *testing.T) {
	for _, kind := range []string{"upload", "download"} {
		for _, fault := range []string{"control", "state", "audit"} {
			t.Run(kind+"_"+fault, func(t *testing.T) {
				h := newHarness(t)
				h.connect(t)
				r := h.transfer.runner
				r.setMaxActiveJobs(1)
				if !r.acquireSlot(t.Context()) {
					t.Fatal("test slot unavailable")
				}
				defer r.releaseSlot()
				start, ids := generationJobStart(t, h, kind, h.transfer.connections.ActiveBookmarkID())
				id, err := start()
				if err != nil {
					t.Fatal(err)
				}
				before, err := h.jobsSvc.Get(t.Context(), h.bookmarkID(), id)
				if err != nil {
					t.Fatal(err)
				}
				query := ""
				if fault == "state" {
					query = "CREATE TRIGGER refuse_queued BEFORE UPDATE OF state ON jobs WHEN NEW.state='cancelled' BEGIN SELECT RAISE(ABORT,'fixture queued state refusal'); END"
				}
				if fault == "audit" {
					query = "CREATE TRIGGER refuse_queued BEFORE INSERT ON audit_events WHEN NEW.operation='transfer.local.cancelled' BEGIN SELECT RAISE(ABORT,'fixture queued audit refusal'); END"
				}
				if query != "" {
					if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
						t.Fatal(err)
					}
				}
				if err := h.transfer.CancelJob(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				// A successful binding edit acknowledges release of the whole
				// queued launch, rather than relying on a terminal-row timing gap.
				awaitJobBindingsReleased(t, h, ids)
				visible, visibleErr := h.jobsSvc.Get(t.Context(), h.bookmarkID(), id)
				row, err := jobs.NewStore(h.db).Get(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				events, err := h.auditSvc.Search(t.Context(), AuditQueryDTO{OperationPrefix: "transfer.local.", Limit: 10})
				if err != nil {
					t.Fatal(err)
				}
				if !row.StartedAt.IsZero() || row.Counters != before.Counters {
					t.Fatalf("queued cancellation changed execution evidence: %+v", row)
				}
				if fault == "control" {
					if visibleErr != nil || visible.State != "cancelled" || len(events) != 1 || events[0].Operation != "transfer.local.cancelled" || events[0].Result != "failed" {
						t.Fatalf("queued outcome differs: %+v %v %+v", visible, visibleErr, events)
					}
				} else if visibleErr == nil || row.State != jobs.StateQueued || len(events) != 0 {
					t.Fatalf("partial queued completion: %+v %v %+v", row, visibleErr, events)
				}
				if _, active := r.cancel(id); active {
					t.Fatal("cancelled queued job retained registration")
				}
			})
		}
	}
}
