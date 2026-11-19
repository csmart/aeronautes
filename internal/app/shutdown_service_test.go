package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
)

func TestJobShutdownRefusesNewStarts(t *testing.T) {
	for _, kind := range []string{"copy", "upload", "download", "delete", "benchmark", "segments"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			start, ids := generationJobStart(t, h, kind, h.transfer.connections.ActiveBookmarkID())
			requests := h.mock.Requests()
			if err := h.transfer.runner.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
			id, err := start()
			cleanupShutdownJob(t, h, id)
			if id != "" || !errors.Is(err, errJobRunnerStopped) {
				t.Fatalf("start after shutdown: id=%q error=%v", id, err)
			}
			if h.mock.Requests() != requests {
				t.Fatal("refused start dispatched a storage request")
			}
			if rows, err := h.jobsSvc.List(t.Context(), "", false, 10); err != nil || len(rows) != 0 {
				t.Fatalf("refused start created a job: rows=%v error=%v", rows, err)
			}
			assertJobBindingsReleased(t, h, ids)
		})
	}
}

func TestJobShutdownOwnsStartingCalls(t *testing.T) {
	for _, kind := range []string{"copy", "upload", "download", "delete", "benchmark", "segments"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			start, ids := generationJobStart(t, h, kind, h.transfer.connections.ActiveBookmarkID())
			gate := gateFindRequests(t, h, true)
			var id string
			var startErr error
			done := make(chan struct{})
			go func() {
				defer close(done)
				id, startErr = start()
			}()
			t.Cleanup(func() {
				gate.unblock()
				awaitConnection(t, done)
				cleanupShutdownJob(t, h, id)
			})
			requestCtx := awaitConnection(t, gate.entered)
			if rows, err := h.jobsSvc.List(t.Context(), "", false, 10); err != nil || len(rows) != 0 {
				t.Fatalf("startup barrier followed job creation: rows=%v error=%v", rows, err)
			}
			assertShutdownPending(t, h.transfer.runner)
			if !errors.Is(requestCtx.Err(), context.Canceled) {
				t.Fatalf("shutdown did not cancel admitted startup I/O: %v", requestCtx.Err())
			}
			assertJobBindingsPinned(t, h, ids)
			gate.unblock()
			awaitConnection(t, done)
			if id != "" || !errors.Is(startErr, context.Canceled) {
				t.Fatalf("cancelled startup escaped refusal: id=%q error=%v", id, startErr)
			}
			shutdownTestRunner(t, h.transfer.runner)
			assertJobBindingsReleased(t, h, ids)
		})
	}
}

func TestJobShutdownCancelsQueuedFamilies(t *testing.T) {
	for _, kind := range []string{"copy", "upload", "download", "delete", "benchmark", "segments"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			start, ids := generationJobStart(t, h, kind, h.transfer.connections.ActiveBookmarkID())
			runner := h.transfer.runner
			runner.setMaxActiveJobs(1)
			if !runner.acquireSlot(t.Context()) {
				t.Fatal("cannot hold the active slot")
			}
			t.Cleanup(runner.releaseSlot)
			id, err := start()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cleanupShutdownJob(t, h, id) })
			requests := h.mock.Requests()
			shutdownTestRunner(t, runner)
			job, err := h.jobsSvc.Get(t.Context(), h.bookmarkID(), id)
			if err != nil || job.State != "cancelled" || job.StartedAt != "" {
				t.Fatalf("queued shutdown: job=%+v error=%v", job, err)
			}
			if h.mock.Requests() != requests {
				t.Fatal("queued cancellation started remote work")
			}
			assertJobBindingsReleased(t, h, ids)
		})
	}
}

func TestJobShutdownDrainsFailedStarts(t *testing.T) {
	for _, kind := range []string{"copy", "upload", "download", "delete", "benchmark", "segments"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			start, ids := generationJobStart(t, h, kind, h.transfer.connections.ActiveBookmarkID())
			if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.Exec(`CREATE TRIGGER refuse_shutdown_job BEFORE INSERT ON jobs
					BEGIN SELECT RAISE(ABORT, 'shutdown fixture create refusal'); END`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			id, err := start()
			cleanupShutdownJob(t, h, id)
			if id != "" || err == nil || !strings.Contains(err.Error(), "shutdown fixture create refusal") {
				t.Fatalf("creation fault not reached: id=%q error=%v", id, err)
			}
			shutdownTestRunner(t, h.transfer.runner)
			assertJobBindingsReleased(t, h, ids)
		})
	}
}

func TestJobShutdownPreservesDetachedCaller(t *testing.T) {
	for _, stopOwner := range []bool{false, true} {
		name := "caller_only"
		if stopOwner {
			name = "owner_shutdown"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			bookmark := h.transfer.connections.ActiveBookmarkID()
			h.mock.AddContainer("shutdown", "standard")
			seedObject(h.mock, "shutdown", "scope/file", "retained contents", nil)
			barrier := newEngineBarrier(t)
			h.installEngine(barrier)
			directory := t.TempDir()
			h.transfer.setFilePicker(fakePicker{dir: directory})
			picked, err := h.transfer.PickDownloadDir(t.Context(), SelectionScopeDTO{BookmarkID: bookmark, Container: "shutdown", Prefix: "scope/"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			id, err := h.transfer.StartDownload(ctx, DownloadRequestDTO{BookmarkID: bookmark,
				Container: "shutdown", Prefix: "scope/", DestDirToken: picked.Token})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { barrier.unblock(); cleanupShutdownJob(t, h, id) })
			barrier.waitUntilEntered(t)
			cancel()
			if stopOwner {
				// The mock engine observes cancellation immediately; its
				// barrier is still released during cleanup on a broken guard.
				shutdownTestRunner(t, h.transfer.runner)
			} else {
				barrier.unblock()
			}
			job := awaitTerminal(t, h, id)
			want := "complete"
			if stopOwner {
				want = "cancelled"
			}
			if job.State != want {
				t.Fatalf("detached caller outcome: got=%s want=%s", job.State, want)
			}
			data, readErr := os.ReadFile(filepath.Join(directory, "file"))
			if stopOwner {
				if !os.IsNotExist(readErr) || job.Counters.ObjectsCopied != 0 || job.Counters.ObjectsVerified != 0 {
					t.Fatalf("cancelled owner placed data: data=%q read=%v counters=%+v", data, readErr, job.Counters)
				}
			} else if readErr != nil || string(data) != "retained contents" || job.Counters.ObjectsCopied != 1 || job.Counters.ObjectsVerified != 1 {
				t.Fatalf("detached completion: data=%q read=%v counters=%+v", data, readErr, job.Counters)
			}
			shutdownTestRunner(t, h.transfer.runner)
			assertJobBindingsReleased(t, h, []string{bookmark})
		})
	}
}

func TestJobShutdownPreservesPersistenceFailure(t *testing.T) {
	for _, phase := range []string{"queued", "running"} {
		for _, fault := range []string{"control", "state", "audit"} {
			t.Run(phase+"_"+fault, func(t *testing.T) {
				h := newHarness(t)
				h.connect(t)
				start, ids := generationJobStart(t, h, "download", h.transfer.connections.ActiveBookmarkID())
				runner := h.transfer.runner
				barrier := newEngineBarrier(t)
				h.installEngine(barrier)
				if phase == "queued" {
					runner.setMaxActiveJobs(1)
					if !runner.acquireSlot(t.Context()) {
						t.Fatal("cannot hold the active slot")
					}
					t.Cleanup(runner.releaseSlot)
				}
				id, err := start()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { barrier.unblock(); cleanupShutdownJob(t, h, id) })
				if phase == "running" {
					barrier.waitUntilEntered(t)
				}
				query := ""
				switch fault {
				case "state":
					query = `CREATE TRIGGER refuse_shutdown_finish BEFORE UPDATE OF state ON jobs
						WHEN NEW.state='cancelled' BEGIN SELECT RAISE(ABORT, 'shutdown state refusal'); END`
				case "audit":
					query = `CREATE TRIGGER refuse_shutdown_audit BEFORE INSERT ON audit_events
						WHEN NEW.operation='transfer.local.cancelled' BEGIN SELECT RAISE(ABORT, 'shutdown audit refusal'); END`
				}
				if query != "" {
					if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
						_, err := tx.Exec(query)
						return err
					}); err != nil {
						t.Fatal(err)
					}
				}
				shutdownTestRunner(t, runner)
				row, err := jobs.NewStore(h.db).Get(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				events, err := h.auditSvc.Search(t.Context(), AuditQueryDTO{OperationPrefix: "transfer.local.cancelled", Limit: 10})
				if err != nil {
					t.Fatal(err)
				}
				if fault == "control" {
					if runner.jobs.Available() != nil || row.State != jobs.StateCancelled || len(events) != 1 {
						t.Fatalf("healthy shutdown lost its result: row=%+v audit=%v available=%v", row, events, runner.jobs.Available())
					}
				} else {
					if runner.jobs.Available() == nil || string(row.State) != phase || len(events) != 0 {
						t.Fatalf("failed completion appeared saved: row=%+v audit=%v available=%v", row, events, runner.jobs.Available())
					}
					if visible, err := h.jobsSvc.Get(t.Context(), h.bookmarkID(), id); visible != nil || err == nil {
						t.Fatalf("stale result remained available: job=%+v error=%v", visible, err)
					}
				}
				if _, registered := runner.cancel(id); registered {
					t.Fatal("drained job retained its cancellation registration")
				}
				assertJobBindingsReleased(t, h, ids)
			})
		}
	}
}

func cleanupShutdownJob(t *testing.T, h *harness, id string) {
	t.Helper()
	if id == "" {
		return
	}
	if cancel, running := h.transfer.runner.cancel(id); running {
		cancel()
	}
	if h.transfer.jobs.Available() == nil {
		awaitTerminal(t, h, id)
	}
}

func shutdownTestRunner(t *testing.T, runner *jobRunner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.Shutdown(ctx); err != nil {
		t.Fatalf("job owners did not drain: %v", err)
	}
}

func assertShutdownPending(t *testing.T, runner *jobRunner) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runner.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("held owner was reported drained: %v", err)
	}
}
