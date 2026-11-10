package app

import (
	"context"
	"fmt"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

// pauseGate is one registered job's pause state (ADR 0104): requested is what
// the operator last asked for, parked is the run waiting at a unit boundary
// without a slot. Both are read and written under the runner's mutex.
type pauseGate struct {
	requested bool
	parked    bool
}

// requestPause asks a registered job to pause at its next unit boundary. It
// reports false for a job this process is not running.
func (r *jobRunner) requestPause(jobID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	gate := r.pauses[jobID]
	if gate == nil {
		return false
	}
	gate.requested = true
	return true
}

// requestResume withdraws a pause. A run that has not reached its boundary
// carries on; a parked one wakes and waits for a slot. It reports false when
// there was nothing to withdraw.
func (r *jobRunner) requestResume(jobID string) bool {
	r.mu.Lock()
	gate := r.pauses[jobID]
	asked := gate != nil && (gate.requested || gate.parked)
	if asked {
		gate.requested = false
	}
	r.mu.Unlock()
	r.slotFreed.Broadcast()
	return asked
}

// pauseStatus is what the drawer says beside the job's state: "pausing" while
// a request waits for its boundary, "resuming" while a resumed run waits for a
// slot, and "" otherwise, a job that is simply paused included.
func (r *jobRunner) pauseStatus(jobID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch gate := r.pauses[jobID]; {
	case gate == nil:
		return ""
	case gate.requested && !gate.parked:
		return "pausing"
	case gate.parked && !gate.requested:
		return "resuming"
	}
	return ""
}

// checkpoint is called by a transfer route at the head of a unit, and nowhere
// else: where it holds no engine child, no request in flight and no unit half
// done (ADR 0104 decision 1). When a pause has been asked for, the run parks
// here. It gives up its slot and takes a queue place in one step, waits to be
// resumed or cancelled, and returns with a slot again, so the caller carries
// on exactly as if it had not stopped.
//
// A cancellation returns the context's error without waiting for a slot: the
// route unwinds as it does for a cancel seen at this boundary, which starts no
// transfer and sends nothing (decision 4). The slot count is taken for that
// unwinding all the same, so the release that follows every body balances.
func (r *jobRunner) checkpoint(ctx context.Context, jobID, bookmarkID string) error {
	r.mu.Lock()
	gate := r.pauses[jobID]
	asked := gate != nil && gate.requested
	r.mu.Unlock()
	if !asked || ctx.Err() != nil {
		return nil
	}
	background := context.WithoutCancel(ctx)
	if err := r.jobs.Transition(background, jobID, jobs.StatePaused); err != nil {
		return fmt.Errorf("pause job %s: %w", jobID, err)
	}
	r.record(background, bookmarkID, "transfer.paused", "job "+jobID, "paused between two units", audit.ResultApplied)

	// A cancellation changes the wait's predicate outside the mutex; waking
	// under it keeps the wake from passing a waiter that has not yet waited.
	stop := context.AfterFunc(ctx, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.slotFreed.Broadcast()
	})
	defer stop()

	r.mu.Lock()
	gate.parked = true
	r.active--
	r.paused++
	r.slotFreed.Broadcast()
	for ctx.Err() == nil {
		if gate.requested {
			r.slotFreed.Wait()
			continue
		}
		if r.active < r.capacity {
			break
		}
		// Resumed with no slot free: the queue place it already holds is
		// counted with the waiters for as long as it waits, never twice.
		r.paused--
		r.queued++
		r.slotFreed.Wait()
		r.queued--
		r.paused++
	}
	r.paused--
	r.active++
	gate.parked = false
	cancelled := ctx.Err()
	r.mu.Unlock()
	// Another job's lost record can have stopped the store while this run
	// was parked. Its remaining units are already admitted, so nothing else
	// would stop it publishing them; the launch asks the same before a body
	// starts. It is asked before the cancel, so that a run stopped for this
	// is ended as a lost record ends it, with its stage kept, and never
	// cleaned up as a cancel is (ADR 0048 decision 7).
	if err := r.jobs.Available(); err != nil {
		return fmt.Errorf("resume job %s: %w", jobID, err)
	}
	if cancelled != nil {
		return cancelled
	}
	if err := r.jobs.Transition(background, jobID, jobs.StateRunning); err != nil {
		return fmt.Errorf("resume job %s: %w", jobID, err)
	}
	r.record(background, bookmarkID, "transfer.resumed", "job "+jobID, "resumed after a pause", audit.ResultApplied)
	return nil
}
