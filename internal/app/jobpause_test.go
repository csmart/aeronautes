package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

// unitJob is a job launched the way the services launch one, whose body is a
// run of units: each unit begins at the runner's checkpoint, reports itself
// and waits to be told to finish, so a test holds every interleaving.
type unitJob struct {
	id      string
	reached chan int
	proceed chan struct{}
}

func launchUnits(t *testing.T, h *harness, bookmark string, units int) *unitJob {
	t.Helper()
	runner := h.transfer.runner
	owner, ctx, err := runner.admit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.finishStart()
	unpin, err := h.transfer.connections.pinForJob(ctx, bookmark)
	if err != nil {
		t.Fatal(err)
	}
	job, err := h.transfer.jobs.Create(ctx, jobs.Job{Type: jobs.TypeUpload, DestBookmarkID: bookmark})
	if err != nil {
		unpin()
		t.Fatal(err)
	}
	runner.register(job.ID, owner.cancel)
	u := &unitJob{id: job.ID, reached: make(chan int, units), proceed: make(chan struct{})}
	finish := func(ctx context.Context, state jobs.State) {
		h.transfer.finishLocal(ctx, job.ID, bookmark, 0, job.Counters, state, "", "")
	}
	owner.hold(unpin)
	owner.launch(job.ID, bookmark, func(ctx context.Context) {
		background := context.WithoutCancel(ctx)
		for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning} {
			if err := h.transfer.jobs.Transition(background, job.ID, state); err != nil {
				t.Error(err)
				return
			}
		}
		for unit := range units {
			if err := errors.Join(runner.checkpoint(ctx, job.ID, bookmark), ctx.Err()); err != nil {
				finish(background, jobs.StateCancelled)
				return
			}
			u.reached <- unit
			select {
			case <-u.proceed:
			case <-ctx.Done():
				finish(background, jobs.StateCancelled)
				return
			}
		}
		if err := h.transfer.jobs.Transition(background, job.ID, jobs.StateVerifying); err != nil {
			t.Error(err)
		}
		finish(background, jobs.StateComplete)
	}, func(ctx context.Context) { finish(ctx, jobs.StateCancelled) })
	return u
}

func (u *unitJob) expectUnit(t *testing.T, unit int) {
	t.Helper()
	select {
	case got := <-u.reached:
		if got != unit {
			t.Fatalf("job %s reached unit %d, want %d", u.id, got, unit)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("job %s did not reach unit %d", u.id, unit)
	}
}

func (u *unitJob) finishUnit(t *testing.T) {
	t.Helper()
	select {
	case u.proceed <- struct{}{}:
	case <-time.After(10 * time.Second):
		t.Fatalf("job %s was not in a unit to finish", u.id)
	}
}

type runnerCounts struct{ active, queued, paused int }

func pauseHarness(t *testing.T) (*harness, *jobRunner, string, func(want runnerCounts)) {
	t.Helper()
	h := newHarness(t)
	h.connect(t)
	runner := h.transfer.runner
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runner.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	waitCounts := func(want runnerCounts) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			runner.mu.Lock()
			got := runnerCounts{runner.active, runner.queued, runner.paused}
			runner.mu.Unlock()
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("runner counts = %+v, want %+v", got, want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	return h, runner, h.transfer.connections.ActiveBookmarkID(), waitCounts
}

func jobState(t *testing.T, h *harness, id string) jobs.State {
	t.Helper()
	job, err := h.transfer.jobs.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return job.State
}

// ADR 0104 decisions 1 to 3: a pause takes effect at the next unit head and
// not before; the parked run gives up its slot, so another job runs; a resume
// waits for the slot in the queue place the job holds, staying paused until it
// has it; and the same run then carries on with its next unit.
func TestPausedRunGivesUpItsSlotAndTakesItBack(t *testing.T) {
	h, runner, bookmark, waitCounts := pauseHarness(t)

	first := launchUnits(t, h, bookmark, 2)
	first.expectUnit(t, 0)
	if !runner.requestPause(first.id) || runner.pauseStatus(first.id) != "pausing" {
		t.Fatalf("pause was not taken as pending: status %q", runner.pauseStatus(first.id))
	}
	if state := jobState(t, h, first.id); state != jobs.StateRunning {
		t.Fatalf("a pause took effect inside a unit: state %s", state)
	}
	first.finishUnit(t)
	waitCounts(runnerCounts{active: 0, queued: 0, paused: 1})
	if state, status := jobState(t, h, first.id), runner.pauseStatus(first.id); state != jobs.StatePaused || status != "" {
		t.Fatalf("parked run: state %s status %q, want paused and nothing pending", state, status)
	}

	second := launchUnits(t, h, bookmark, 1)
	second.expectUnit(t, 0)

	if !runner.requestResume(first.id) {
		t.Fatal("the resume was refused")
	}
	waitCounts(runnerCounts{active: 1, queued: 1, paused: 0})
	if state, status := jobState(t, h, first.id), runner.pauseStatus(first.id); state != jobs.StatePaused || status != "resuming" {
		t.Fatalf("a resumed run waiting for the slot: state %s status %q, want paused and resuming", state, status)
	}

	second.finishUnit(t)
	if job := awaitTerminal(t, h, second.id); job.State != "complete" {
		t.Fatalf("the job that ran during the pause = %+v", job)
	}
	first.expectUnit(t, 1)
	if state := jobState(t, h, first.id); state != jobs.StateRunning {
		t.Fatalf("the resumed run is %s, want running", state)
	}
	first.finishUnit(t)
	if job := awaitTerminal(t, h, first.id); job.State != "complete" {
		t.Fatalf("the resumed job = %+v", job)
	}
	waitCounts(runnerCounts{})

	events, err := h.auditSvc.audit.List(t.Context(), audit.Query{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, event := range events {
		if event.Resource == "job "+first.id {
			seen[event.Operation]++
		}
	}
	if seen["transfer.paused"] != 1 || seen["transfer.resumed"] != 1 {
		t.Errorf("trail for the paused job = %v, want one pause and one resume", seen)
	}
}

// ADR 0104 decision 3: a resume asked before the boundary withdraws the
// request, and the run carries on without ever being paused.
func TestResumeBeforeTheBoundaryWithdrawsThePause(t *testing.T) {
	h, runner, bookmark, waitCounts := pauseHarness(t)
	job := launchUnits(t, h, bookmark, 2)
	job.expectUnit(t, 0)
	if !runner.requestPause(job.id) || !runner.requestResume(job.id) {
		t.Fatal("the pause or its withdrawal was refused")
	}
	job.finishUnit(t)
	job.expectUnit(t, 1)
	if state := jobState(t, h, job.id); state != jobs.StateRunning {
		t.Fatalf("state %s, want running throughout", state)
	}
	job.finishUnit(t)
	awaitTerminal(t, h, job.id)
	waitCounts(runnerCounts{})
	if runner.requestResume(job.id) || runner.requestPause(job.id) {
		t.Error("a finished job still took a pause or a resume")
	}
}

// ADR 0104 decision 4: cancelling a parked run ends it at once, while another
// job holds the slot, and leaves the slot accounting balanced.
func TestCancellingAPausedRunDoesNotWaitForTheSlot(t *testing.T) {
	h, runner, bookmark, waitCounts := pauseHarness(t)
	first := launchUnits(t, h, bookmark, 2)
	first.expectUnit(t, 0)
	runner.requestPause(first.id)
	first.finishUnit(t)
	waitCounts(runnerCounts{paused: 1})

	second := launchUnits(t, h, bookmark, 1)
	second.expectUnit(t, 0)

	cancel, ok := runner.cancel(first.id)
	if !ok {
		t.Fatal("a paused job cannot be cancelled")
	}
	cancel()
	if job := awaitTerminal(t, h, first.id); job.State != "cancelled" {
		t.Fatalf("the cancelled paused job = %+v", job)
	}
	waitCounts(runnerCounts{active: 1})

	second.finishUnit(t)
	awaitTerminal(t, h, second.id)
	waitCounts(runnerCounts{})
}

// ADR 0104 decision 2: a parked run holds a queue place. Waiting and paused
// jobs together fill the queue, a start is then refused in words that name
// both, and a resume is never refused: it waits in the place the job holds.
func TestPausedRunsCountAgainstTheQueueDepth(t *testing.T) {
	h, runner, bookmark, waitCounts := pauseHarness(t)
	runner.setMaxQueuedJobs(2)

	parked := launchUnits(t, h, bookmark, 2)
	parked.expectUnit(t, 0)
	// The queue is full before the pause: an admitted, active job parks with
	// one job waiting behind it and no place to spare.
	runner.setMaxQueuedJobs(1)
	waiting := launchUnits(t, h, bookmark, 1)
	waitCounts(runnerCounts{active: 1, queued: 1})
	runner.requestPause(parked.id)
	parked.finishUnit(t)
	waiting.expectUnit(t, 0)
	waitCounts(runnerCounts{active: 1, queued: 0, paused: 1})

	refused := func() {
		t.Helper()
		owner, _, err := runner.admit(t.Context())
		if owner != nil {
			owner.finishStart()
		}
		if !errors.Is(err, errJobQueueFull) || !strings.Contains(err.Error(), "waiting or paused") {
			t.Fatalf("a start with the queue full of paused and waiting jobs = %v", err)
		}
	}
	refused()

	if !runner.requestResume(parked.id) {
		t.Fatal("a resume was refused although the job already holds its place")
	}
	waitCounts(runnerCounts{active: 1, queued: 1, paused: 0})

	waiting.finishUnit(t)
	awaitTerminal(t, h, waiting.id)
	parked.expectUnit(t, 1)
	parked.finishUnit(t)
	awaitTerminal(t, h, parked.id)
	waitCounts(runnerCounts{})

	owner, _, err := runner.admit(t.Context())
	if err != nil {
		t.Fatalf("a start after the paused job ended = %v", err)
	}
	owner.finishStart()
}

// ADR 0104 decision 2: a running job that parks was counted as neither waiting
// nor paused when it was admitted, so the jobs waiting and paused together can
// exceed the queue depth by the number of active slots, and by no more. With a
// depth of one and one slot, one job runs and one waits; the first parks, the
// second takes the slot and parks too. Nothing new is admitted while both are
// parked, and both still resume and finish.
func TestParkingMayExceedTheQueueDepthByTheActiveBudget(t *testing.T) {
	h, runner, bookmark, waitCounts := pauseHarness(t)
	runner.setMaxQueuedJobs(1)

	first := launchUnits(t, h, bookmark, 2)
	first.expectUnit(t, 0)
	second := launchUnits(t, h, bookmark, 2)
	waitCounts(runnerCounts{active: 1, queued: 1})
	runner.requestPause(first.id)
	first.finishUnit(t)
	second.expectUnit(t, 0)
	waitCounts(runnerCounts{active: 1, paused: 1})
	runner.requestPause(second.id)
	second.finishUnit(t)
	waitCounts(runnerCounts{paused: 2})
	if jobState(t, h, first.id) != jobs.StatePaused || jobState(t, h, second.id) != jobs.StatePaused {
		t.Fatal("both jobs should be recorded as paused")
	}
	owner, _, err := runner.admit(t.Context())
	if owner != nil {
		owner.finishStart()
	}
	if !errors.Is(err, errJobQueueFull) {
		t.Fatalf("a start with two jobs parked over a depth of one = %v", err)
	}

	for _, job := range []*unitJob{first, second} {
		if !runner.requestResume(job.id) {
			t.Fatalf("job %s refused a resume", job.id)
		}
		job.expectUnit(t, 1)
		job.finishUnit(t)
		awaitTerminal(t, h, job.id)
	}
	waitCounts(runnerCounts{})
}

// ADR 0104 decision 4: quitting cancels a paused job as it cancels a running
// one, and the shutdown returns.
func TestShutdownEndsAPausedRun(t *testing.T) {
	h, runner, bookmark, waitCounts := pauseHarness(t)
	job := launchUnits(t, h, bookmark, 2)
	job.expectUnit(t, 0)
	runner.requestPause(job.id)
	job.finishUnit(t)
	waitCounts(runnerCounts{paused: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runner.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown with a paused job: %v", err)
	}
	if state := jobState(t, h, job.id); state != jobs.StateCancelled {
		t.Fatalf("the paused job is %s after shutdown, want cancelled", state)
	}
	waitCounts(runnerCounts{})
}
