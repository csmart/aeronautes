package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
)

func TestJobShutdownWaitsForFinalCleanup(t *testing.T) {
	for _, phase := range []string{"running_body", "terminal_body_cleanup", "pin_cleanup"} {
		t.Run(phase, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			runner := h.transfer.runner
			owner, _, err := runner.admit(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer owner.finishStart()
			bookmark := h.transfer.connections.ActiveBookmarkID()
			unpin, err := h.transfer.connections.pinForJob(t.Context(), bookmark)
			if err != nil {
				t.Fatal(err)
			}
			job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeDownload})
			if err != nil {
				unpin()
				t.Fatal(err)
			}
			runner.register(job.ID, owner.cancel)
			gate, unblock := connectionGate(t)
			reached, cleaned := make(chan struct{}), make(chan struct{})
			bodyResult := make(chan error, 1)
			owner.launch(job.ID, bookmark, func() {
				if phase == "pin_cleanup" {
					close(reached)
					<-gate
				}
				unpin()
				close(cleaned)
			}, func(context.Context) {
				if phase == "running_body" {
					close(reached)
					<-gate
				}
				runner.release(job.ID)
				bodyResult <- h.transfer.jobs.Transition(context.Background(), job.ID, jobs.StateCancelled)
				if phase == "terminal_body_cleanup" {
					close(reached)
					<-gate
				}
			}, nil)
			t.Cleanup(func() { unblock(); awaitConnection(t, cleaned) })
			awaitConnection(t, reached)
			assertJobBindingsPinned(t, h, []string{bookmark})
			assertShutdownPending(t, runner)
			if !errors.Is(owner.ctx.Err(), context.Canceled) {
				t.Fatalf("owned context was not cancelled: %v", owner.ctx.Err())
			}
			runner.mu.Lock()
			active, registered := runner.active, len(runner.cancels)
			runner.mu.Unlock()
			if phase == "pin_cleanup" && (active != 0 || registered != 0) {
				t.Fatalf("final cleanup did not outlive slot/registry: active=%d registered=%d", active, registered)
			}
			if err := h.db.Reader().PingContext(t.Context()); err != nil {
				t.Fatalf("database unavailable to held owner: %v", err)
			}
			unblock()
			shutdownTestRunner(t, runner)
			if err := awaitConnection(t, bodyResult); err != nil {
				t.Fatal(err)
			}
			assertJobBindingsReleased(t, h, []string{bookmark})
		})
	}
}

func TestJobShutdownWaiterErrorsKeepOwnership(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name, want := "cancelled", context.Canceled
		if timeout {
			name, want = "deadline", context.DeadlineExceeded
		}
		t.Run(name, func(t *testing.T) {
			runner := newJobRunner(nil, nil)
			owner, startCtx, err := runner.admit(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer owner.finishStart()
			ctx, cancel := context.WithCancel(context.Background())
			if timeout {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			} else {
				cancel()
			}
			defer cancel()
			if err := runner.Shutdown(ctx); !errors.Is(err, want) {
				t.Fatalf("undrained wait: got=%v want=%v", err, want)
			}
			if !errors.Is(startCtx.Err(), context.Canceled) || !errors.Is(owner.ctx.Err(), context.Canceled) {
				t.Fatalf("shutdown did not cancel both owned contexts: start=%v job=%v", startCtx.Err(), owner.ctx.Err())
			}
			if late, _, err := runner.admit(t.Context()); late != nil || !errors.Is(err, errJobRunnerStopped) {
				t.Fatalf("failed waiter reopened admission: owner=%v error=%v", late, err)
			}
			owner.finishStart()
			if err := runner.Shutdown(ctx); err != nil {
				t.Fatalf("already drained runner did not return success: %v", err)
			}
			shutdownTestRunner(t, runner)
		})
	}
}

func TestJobShutdownConcurrentAdmission(t *testing.T) {
	runner := newJobRunner(nil, nil)
	gate := make(chan struct{})
	var started sync.WaitGroup
	owners := make(chan *jobOwner, 32)
	errorsSeen := make(chan error, 32)
	for range cap(owners) {
		started.Go(func() {
			<-gate
			owner, _, err := runner.admit(t.Context())
			if owner != nil {
				owners <- owner
			} else if !errors.Is(err, errJobRunnerStopped) {
				errorsSeen <- err
			}
		})
	}
	close(gate)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runner.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	started.Wait()
	close(owners)
	close(errorsSeen)
	for err := range errorsSeen {
		t.Errorf("unexpected admission result: %v", err)
	}
	for owner := range owners {
		if !errors.Is(owner.ctx.Err(), context.Canceled) {
			t.Error("admission escaped the shutdown cancellation sweep")
		}
		owner.finishStart()
	}
	var waiters sync.WaitGroup
	for range 8 {
		waiters.Go(func() {
			if err := runner.Shutdown(t.Context()); err != nil {
				t.Errorf("concurrent drained waiter: %v", err)
			}
		})
	}
	waiters.Wait()
}

func TestJobShutdownCancelledCallerBeforeHandoff(t *testing.T) {
	for _, beforeAdmission := range []bool{true, false} {
		name := "during_start"
		if beforeAdmission {
			name = "before_admission"
		}
		t.Run(name, func(t *testing.T) {
			runner := newJobRunner(nil, nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if beforeAdmission {
				cancel()
			}
			owner, startCtx, err := runner.admit(ctx)
			if beforeAdmission {
				if owner != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled caller admitted: owner=%v error=%v", owner, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer owner.finishStart()
				cancel()
				if !errors.Is(startCtx.Err(), context.Canceled) {
					t.Fatalf("startup detached before handoff: %v", startCtx.Err())
				}
				owner.finishStart()
			}
			shutdownTestRunner(t, runner)
		})
	}
}

func TestJobShutdownKeepsJobCancellationSeparate(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	runner := h.transfer.runner
	owner, startCtx, err := runner.admit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.finishStart()
	job, err := runner.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeDownload})
	if err != nil {
		t.Fatal(err)
	}
	runner.register(job.ID, owner.cancel)
	defer runner.release(job.ID)
	if err := h.transfer.CancelJob(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := startCtx.Err(); err != nil {
		t.Fatalf("job cancellation changed synchronous start admission: %v", err)
	}
	if !errors.Is(owner.ctx.Err(), context.Canceled) {
		t.Fatal("CancelJob did not cancel the admitted job context")
	}
	assertShutdownPending(t, runner)
	if !errors.Is(startCtx.Err(), context.Canceled) {
		t.Fatal("owner shutdown did not cancel the still-owned startup")
	}
	owner.finishStart()
	shutdownTestRunner(t, runner)
}
