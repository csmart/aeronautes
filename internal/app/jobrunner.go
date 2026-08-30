package app

import (
	"context"
	"log"
	"sync"

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
	active    int
	capacity  int
	slotFreed *sync.Cond
	cancels   map[string]context.CancelFunc
}

// NewJobRunner is the exported constructor for the shell (main.go).
func NewJobRunner(jobStore *jobs.Store, auditStore *audit.Store) *jobRunner {
	return newJobRunner(jobStore, auditStore)
}

func newJobRunner(jobStore *jobs.Store, auditStore *audit.Store) *jobRunner {
	r := &jobRunner{
		jobs:     jobStore,
		audit:    auditStore,
		capacity: defaultMaxActiveJobs,
		cancels:  map[string]context.CancelFunc{},
	}
	r.slotFreed = sync.NewCond(&r.mu)
	return r
}

// setMaxActiveJobs resizes the §38 ceiling. The budget is a COUNT, not
// a swapped channel (review G24/H-16): running jobs stay accounted, a
// lowered ceiling admits nothing until enough of them finish, and a
// raised one wakes queued jobs immediately.
func (r *jobRunner) setMaxActiveJobs(n int) {
	if n <= 0 {
		n = defaultMaxActiveJobs
	}
	r.mu.Lock()
	r.capacity = n
	r.mu.Unlock()
	r.slotFreed.Broadcast()
}

func (r *jobRunner) register(jobID string, cancel context.CancelFunc) {
	r.mu.Lock()
	r.cancels[jobID] = cancel
	r.mu.Unlock()
}

func (r *jobRunner) release(jobID string) {
	r.mu.Lock()
	delete(r.cancels, jobID)
	r.mu.Unlock()
}

func (r *jobRunner) cancel(jobID string) (context.CancelFunc, bool) {
	r.mu.Lock()
	cancel, ok := r.cancels[jobID]
	r.mu.Unlock()
	return cancel, ok
}

// launch runs a job body under the slot budget: the job stays Queued
// until a slot frees; cancellation while queued is honoured without
// ever starting.
func (r *jobRunner) launch(ctx context.Context, jobID, bookmarkID string, body func(ctx context.Context)) {
	go func() {
		background := context.WithoutCancel(ctx)
		if !r.acquireSlot(ctx) {
			_ = r.jobs.Transition(background, jobID, jobs.StateCancelled)
			r.record(background, bookmarkID, "transfer.cancelled-queued", "job "+jobID, "cancelled before a job slot freed", audit.ResultApplied)
			r.release(jobID)
			return
		}
		defer r.releaseSlot()
		body(ctx)
	}()
}

// acquireSlot blocks until the active count is under the ceiling or the
// context dies; false means cancelled-while-queued.
func (r *jobRunner) acquireSlot(ctx context.Context) bool {
	// A context watcher pokes the cond so a queued cancellation wakes.
	stop := context.AfterFunc(ctx, func() { r.slotFreed.Broadcast() })
	defer stop()
	r.mu.Lock()
	defer r.mu.Unlock()
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
	if err := r.audit.Append(ctx, audit.Event{
		BookmarkID: bookmarkID,
		Operation:  operation,
		Resource:   resource,
		Summary:    summary,
		Result:     result,
	}); err != nil {
		log.Printf("AUDIT WRITE FAILED for %s %s: %v", operation, resource, err)
	}
}
