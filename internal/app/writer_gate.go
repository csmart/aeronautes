package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// errWritersStopped refuses a synchronous write once shutdown has begun.
// The caller has not reached the store, so refusing here changes nothing
// the operator can observe (ADR 0053).
var errWritersStopped = errors.New("shutdown has begun; new writes are unavailable")

// writerGate owns synchronous writers across shutdown. A writer enters
// before it can change the operator's data and leaves only after its
// final record, so the shell cannot close the state database or release
// the instance lock underneath one (ADR 0053).
//
// Unlike the job runner this never cancels an entered writer. A job can
// be cancelled because its work is resumable and its outcome is a record
// of not finishing; a synchronous write that has already reached the
// store must be allowed to finish saying what it did, and cancelling it
// is exactly how the record is lost.
type writerGate struct {
	mu      sync.Mutex
	stopped bool
	closed  bool
	active  int
	drained chan struct{}
}

func newWriterGate() *writerGate {
	return &writerGate{drained: make(chan struct{})}
}

// enter admits a writer, or refuses once shutdown has begun.
func (g *writerGate) enter() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return errWritersStopped
	}
	g.active++
	return nil
}

// leave releases a writer. It must be called after the write's final
// record, not after the store request.
func (g *writerGate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active > 0 {
		g.active--
	}
	g.markDrainedLocked()
}

// markDrainedLocked closes the drain exactly once, and only when
// admission is shut and nothing is left inside.
func (g *writerGate) markDrainedLocked() {
	if g.stopped && g.active == 0 && !g.closed {
		g.closed = true
		close(g.drained)
	}
}

// Shutdown closes admission and waits for entered writers to leave.
func (g *writerGate) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	if !g.stopped {
		g.stopped = true
		g.markDrainedLocked()
	}
	g.mu.Unlock()
	// Prefer a completed drain to a cancelled context: an already drained
	// gate is drained whatever the deadline says.
	select {
	case <-g.drained:
		return nil
	default:
	}
	select {
	case <-g.drained:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for synchronous writers: %w", ctx.Err())
	}
}
