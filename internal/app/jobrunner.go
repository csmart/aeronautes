package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

// jobRunner is the shared §38 execution core: one bounded active-job
// budget and one cancellation registry across every job-running service
// (transfers and deletions compete for the same slots).
type jobRunner struct {
	jobs  *jobs.Store
	audit *audit.Store
	mu    sync.Mutex
	// active/capacity are the §38 admission budget; slotFreed wakes
	// queued launches on release, resize, or queued-cancel.
	active   int
	capacity int
	// ceiling is the hard limit capacity may never exceed, seeded from
	// maxActiveJobsCeiling (ADR 0047). It is a runner value rather than
	// the constant itself so TestJobCeilingSurvivesResize can still raise
	// a runner and exercise the resize machinery; see setMaxActiveJobs.
	ceiling int
	// queueCapacity bounds the jobs that may wait behind the active
	// slots (ADR 0074 decision 2): admit refuses a start when as many
	// owners are already waiting for a slot. queued counts them: owners
	// inside acquireSlot's wait, not owners still in their start phase,
	// so a burst of concurrent starts is admitted as before.
	queueCapacity int
	queued        int
	// paused counts the runs parked at a unit boundary (ADR 0104). A parked
	// run gives up its slot and holds a queue place instead, so admit counts
	// it with the waiters and what parked runs retain stays bounded by the
	// queue depth and the active slots (ADR 0104 decision 2).
	paused    int
	pauses    map[string]*pauseGate
	slotFreed *sync.Cond
	cancels   map[string]context.CancelFunc
	stopped   bool
	owners    map[*jobOwner]struct{}
	drained   chan struct{}
}

var errJobRunnerStopped = errors.New("job shutdown has begun; new jobs are unavailable")

// errJobQueueFull wraps the refusal of a start when the queue is full; the
// message names the count so the operator knows what to wait for.
var errJobQueueFull = errors.New("the job queue is full")

type jobOwner struct {
	runner      *jobRunner
	ctx         context.Context
	cancel      context.CancelFunc
	cancelStart context.CancelFunc
	handedOff   bool
	// pins releases the bookmark generations the start pinned for its job
	// (hold). A start that stops before launch releases them itself; a
	// launched job releases them after its whole body has returned.
	pins func()
	// claims are what the job stages into or sweeps (claim), and jobID names
	// it once its row exists; both are read and written under the runner's
	// mutex, and both leave with the owner.
	claims []scopeClaim
	jobID  string
}

// NewJobRunner is the exported constructor for the shell (main.go).
func NewJobRunner(jobStore *jobs.Store, auditStore *audit.Store) *jobRunner {
	return newJobRunner(jobStore, auditStore)
}

func newJobRunner(jobStore *jobs.Store, auditStore *audit.Store) *jobRunner {
	r := &jobRunner{
		jobs:          jobStore,
		audit:         auditStore,
		capacity:      defaultMaxActiveJobs,
		ceiling:       maxActiveJobsCeiling,
		queueCapacity: defaultMaxQueuedJobs,
		cancels:       map[string]context.CancelFunc{},
		pauses:        map[string]*pauseGate{},
		owners:        map[*jobOwner]struct{}{},
		drained:       make(chan struct{}),
	}
	r.slotFreed = sync.NewCond(&r.mu)
	// A runner built without a store runs no job; some tests build one.
	if jobStore != nil {
		jobStore.OnStop(r.storeStopped)
	}
	return r
}

// storeStopped cancels every other job the runner holds once the job store
// can no longer record (ADR 0105 decision 9). With several running, a sibling
// whose items are admitted would otherwise go on publishing them, because
// recording an outcome stays possible after the stop. Each route ends as it
// does for a cancel, at the check that stands before each of its
// publications, and a parked or queued job is woken to see it. New starts are
// refused by the store itself. The store has already cancelled the jobs whose
// bodies were running, before its failing write returned; this notice arrives
// later and is what reaches the rest.
//
// The job whose record was lost is left alone. Its own write has told it, and
// it ends on that account as it did when it was the only job; a cancel
// arriving beside that error could change what it reports.
func (r *jobRunner) storeStopped(failed string) {
	r.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(r.owners))
	for owner := range r.owners {
		if failed != "" && owner.jobID == failed {
			continue
		}
		cancels = append(cancels, owner.cancelOwned)
	}
	r.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	r.mu.Lock()
	r.slotFreed.Broadcast()
	r.mu.Unlock()
}

// Startup must be owned before it acquires pins or performs I/O, since
// shutdown can otherwise miss work that has not registered a job yet.
func (r *jobRunner) admit(ctx context.Context) (*jobOwner, context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return nil, nil, errJobRunnerStopped
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("start job: %w", err)
	}
	// A store that can no longer record refuses every new job. Refused here,
	// before the start registers or does any I/O, because the store's notice
	// to the runner (storeStopped) arrives on a goroutine of its own and
	// cancels every start it finds: a start that came after the stop but
	// before that notice would end as "context canceled" instead of being
	// told why.
	if r.jobs != nil {
		if err := r.jobs.Available(); err != nil {
			return nil, nil, err
		}
	}
	// Once as many jobs wait for a slot as the queue depth allows, a new
	// start is refused rather than queued without bound (ADR 0074
	// decision 2).
	// The refusal names the running jobs too: the queue depth counts only
	// the waiting ones, so "1 waiting" alone did not say why a third start
	// was taken and a fourth refused (ADR 0126 decision 2).
	if held := r.queued + r.paused; held >= r.queueCapacity {
		if r.paused > 0 {
			return nil, nil, fmt.Errorf("%w: %d running and %d waiting or paused; wait for one to finish, resume one or cancel one", errJobQueueFull, r.active, held)
		}
		return nil, nil, fmt.Errorf("%w: %d running and %d waiting; wait for one to finish or cancel one", errJobQueueFull, r.active, held)
	}
	startCtx, cancelStart := context.WithCancel(ctx)
	jobCtx, cancelJob := context.WithCancel(context.WithoutCancel(ctx))
	owner := &jobOwner{
		runner: r, ctx: jobCtx, cancelStart: cancelStart,
		cancel: cancelJob,
	}
	r.owners[owner] = struct{}{}
	return owner, startCtx, nil
}

// hold gives the owner what the start pinned for its job, so every path
// out of the start releases it exactly once: finishStart when the start
// stops short of launch, the job's own cleanup after launch. A later hold
// replaces the earlier release, for a start that widens what it holds.
func (o *jobOwner) hold(releasePins func()) {
	o.pins = releasePins
}

func (o *jobOwner) releasePins() {
	if o.pins != nil {
		o.pins()
	}
}

func (o *jobOwner) finishStart() {
	o.cancelStart()
	if !o.handedOff {
		o.releasePins()
		o.release()
	}
}

func (o *jobOwner) launch(jobID, bookmarkID string, body, queuedCancel func(context.Context)) {
	o.handedOff = true
	o.runner.mu.Lock()
	o.jobID = jobID
	o.runner.mu.Unlock()
	o.runner.launchWithQueuedOutcome(o.ctx, jobID, bookmarkID, func() {
		// Slots and terminal records can disappear before handles and
		// pins; only the end of this cleanup permits the owner to leave.
		defer o.release()
		o.releasePins()
	}, body, queuedCancel)
}

func (o *jobOwner) release() {
	o.cancelOwned()
	r := o.runner
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, present := r.owners[o]; !present {
		return
	}
	delete(r.owners, o)
	if r.stopped && len(r.owners) == 0 {
		close(r.drained)
	}
}

func (o *jobOwner) cancelOwned() {
	o.cancelStart()
	o.cancel()
}

// Shutdown keeps admission closed even if this caller stops waiting.
// A successful return joins every admitted start and its final cleanup.
func (r *jobRunner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	var cancels []context.CancelFunc
	if !r.stopped {
		r.stopped = true
		cancels = make([]context.CancelFunc, 0, len(r.owners))
		for owner := range r.owners {
			cancels = append(cancels, owner.cancelOwned)
		}
		if len(r.owners) == 0 {
			close(r.drained)
		}
	}
	r.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	// Cancellation changes the queue predicate outside mu. Synchronise
	// its final wake with Wait so a queued owner cannot miss shutdown.
	r.mu.Lock()
	r.slotFreed.Broadcast()
	r.mu.Unlock()
	select {
	case <-r.drained:
		return nil
	default:
	}
	select {
	case <-r.drained:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for job shutdown: %w", ctx.Err())
	}
}

// setMaxActiveJobs resizes the §38 ceiling. The budget is a COUNT, not
// a swapped channel (review G24/H-16): running jobs stay accounted, a
// lowered ceiling admits nothing until enough of them finish, and a
// raised one wakes queued jobs immediately.
// The ceiling is clamped here rather than at the settings surface because
// this is where admission is decided: any future direct caller of this
// path would otherwise raise a runner past the §38 limit (ADR 0047).
func (r *jobRunner) setMaxActiveJobs(n int) {
	if n <= 0 {
		n = defaultMaxActiveJobs
	}
	r.mu.Lock()
	if n > r.ceiling {
		n = r.ceiling
	}
	r.capacity = n
	r.mu.Unlock()
	r.slotFreed.Broadcast()
}

// setMaxQueuedJobs resizes the queue depth (ADR 0074 decision 2). A
// lowered depth refuses new starts until enough waiters have left; jobs
// already waiting keep their place.
func (r *jobRunner) setMaxQueuedJobs(n int) {
	if n <= 0 {
		n = defaultMaxQueuedJobs
	}
	r.mu.Lock()
	r.queueCapacity = n
	r.mu.Unlock()
}

// queueDepth reports the depth in force.
func (r *jobRunner) queueDepth() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queueCapacity
}

func (r *jobRunner) register(jobID string, cancel context.CancelFunc) {
	r.mu.Lock()
	r.cancels[jobID] = cancel
	r.pauses[jobID] = &pauseGate{}
	r.mu.Unlock()
	if r.jobs != nil {
		r.jobs.CancelOnStop(jobID, cancel)
	}
}

func (r *jobRunner) release(jobID string) {
	r.mu.Lock()
	delete(r.cancels, jobID)
	delete(r.pauses, jobID)
	r.mu.Unlock()
	if r.jobs != nil {
		r.jobs.ForgetOnStop(jobID)
	}
}

func (r *jobRunner) cancel(jobID string) (context.CancelFunc, bool) {
	r.mu.Lock()
	cancel, ok := r.cancels[jobID]
	r.mu.Unlock()
	return cancel, ok
}

// A caller's terminal contract also covers cancellation that prevents
// its execution body from ever starting.
func (r *jobRunner) launchWithQueuedOutcome(ctx context.Context, jobID, bookmarkID string, releasePins func(), body, queuedCancel func(context.Context)) {
	go func() {
		// Terminal rows and cancellation deregistration can precede
		// cleanup. The binding survives until the whole body has returned,
		// including when queued cancellation never enters that body.
		defer releasePins()
		background := context.WithoutCancel(ctx)
		if !r.acquireSlot(ctx) {
			if r.jobs.Available() != nil {
				r.release(jobID)
				return
			}
			if queuedCancel != nil {
				queuedCancel(background)
				return
			}
			_ = r.jobs.Transition(background, jobID, jobs.StateCancelled)
			r.record(background, bookmarkID, "transfer.cancelled-queued", "job "+jobID, "cancelled before a job slot freed", audit.ResultApplied)
			r.release(jobID)
			return
		}
		defer r.releaseSlot()
		if r.jobs.Available() != nil {
			r.release(jobID)
			return
		}
		body(ctx)
	}()
}

// acquireSlot blocks until the active count is under the ceiling or the
// context dies; false means cancelled-while-queued.
func (r *jobRunner) acquireSlot(ctx context.Context) bool {
	// Cancellation can follow the predicate check but precede Wait. Sharing
	// the condition mutex keeps that wake from passing an unregistered waiter.
	stop := context.AfterFunc(ctx, func() {
		r.slotFreed.L.Lock()
		defer r.slotFreed.L.Unlock()
		r.slotFreed.Broadcast()
	})
	defer stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active >= r.capacity {
		// Waiting for a slot is what the queue depth counts; the count
		// drops when this owner leaves the wait, with or without a slot.
		r.queued++
		defer func() { r.queued-- }()
	}
	for r.active >= r.capacity {
		if ctx.Err() != nil {
			return false
		}
		r.slotFreed.Wait()
	}
	if ctx.Err() != nil {
		return false
	}
	r.active++
	return true
}

func (r *jobRunner) releaseSlot() {
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	r.slotFreed.Broadcast()
}

func (r *jobRunner) record(ctx context.Context, bookmarkID, operation, resource, summary string, result audit.Result) {
	_ = appendAudit(ctx, r.audit, audit.Event{
		BookmarkID: bookmarkID, Operation: operation, Resource: resource, Summary: summary, Result: result,
	})
}

// appendAudit writes one trail event and names a refused write in the log,
// because the operation it describes has already happened; a caller that
// must act on the refusal keeps the error.
func appendAudit(ctx context.Context, store *audit.Store, event audit.Event) error {
	err := store.Append(ctx, event)
	if err != nil {
		applog.Errorf("AUDIT WRITE FAILED for %s %s: %v", event.Operation, event.Resource, err)
	}
	return err
}
