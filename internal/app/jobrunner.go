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

	mu      sync.Mutex
	slots   chan struct{}
	cancels map[string]context.CancelFunc
}

// NewJobRunner is the exported constructor for the shell (main.go).
func NewJobRunner(jobStore *jobs.Store, auditStore *audit.Store) *jobRunner {
	return newJobRunner(jobStore, auditStore)
}

func newJobRunner(jobStore *jobs.Store, auditStore *audit.Store) *jobRunner {
	return &jobRunner{
		jobs:    jobStore,
		audit:   auditStore,
		slots:   make(chan struct{}, defaultMaxActiveJobs),
		cancels: map[string]context.CancelFunc{},
	}
}

func (r *jobRunner) setMaxActiveJobs(n int) {
	if n <= 0 {
		n = defaultMaxActiveJobs
	}
	r.mu.Lock()
	r.slots = make(chan struct{}, n)
	r.mu.Unlock()
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
	r.mu.Lock()
	slots := r.slots
	r.mu.Unlock()
	go func() {
		background := context.WithoutCancel(ctx)
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			_ = r.jobs.Transition(background, jobID, jobs.StateCancelled)
			r.record(background, bookmarkID, "transfer.cancelled-queued", "job "+jobID, "cancelled before a job slot freed", audit.ResultApplied)
			r.release(jobID)
			return
		}
		defer func() { <-slots }()
		body(ctx)
	}()
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
