package app

import (
	"sync"
	"testing"
	"time"
)

type manualFindClock struct {
	mu      sync.Mutex
	at      time.Time
	timers  map[*manualFindTimer]struct{}
	created chan time.Duration
}

type manualFindTimer struct {
	clock    *manualFindClock
	at       time.Time
	callback func()
}

func (t *manualFindTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	_, active := t.clock.timers[t]
	delete(t.clock.timers, t)
	return active
}

func newManualFindClock() *manualFindClock {
	return &manualFindClock{at: time.Unix(1000, 0), timers: map[*manualFindTimer]struct{}{}, created: make(chan time.Duration, 128)}
}

func (c *manualFindClock) clock() findClock {
	return findClock{
		now: func() time.Time {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.at
		},
		afterFunc: func(delay time.Duration, callback func()) findTimer {
			c.mu.Lock()
			defer c.mu.Unlock()
			timer := &manualFindTimer{clock: c, at: c.at.Add(delay), callback: callback}
			c.timers[timer] = struct{}{}
			select {
			case c.created <- delay:
			default:
			}
			return timer
		},
	}
}

// Queuing separately from execution reproduces an already-fired callback
// waiting for the service lock when a poll renews or removes its record.
func (c *manualFindClock) queue(delay time.Duration) []func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(delay)
	var callbacks []func()
	for timer := range c.timers {
		if !c.at.Before(timer.at) {
			delete(c.timers, timer)
			callbacks = append(callbacks, timer.callback)
		}
	}
	return callbacks
}

func (c *manualFindClock) advance(delay time.Duration) {
	for _, callback := range c.queue(delay) {
		callback()
	}
}

func (c *manualFindClock) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

func (c *manualFindClock) waitForShutdownTimer(t *testing.T, result <-chan error) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case delay := <-c.created:
			if delay == findShutdownWait {
				return
			}
		case err := <-result:
			t.Fatalf("shutdown returned before the held worker exited: %v", err)
		case <-deadline.C:
			t.Fatal("shutdown did not enter its bounded wait")
		}
	}
}

func clockFindFixture(t *testing.T, blocked bool) (*harness, *FindService, *manualFindClock, *findRequestGate) {
	t.Helper()
	h := newHarness(t)
	h.connect(t)
	h.mock.AddContainer("find-clock", "standard")
	seedObject(h.mock, "find-clock", "match.txt", "data", nil)
	var gate *findRequestGate
	if blocked {
		gate = gateFindRequests(t, h, true)
	}
	clock := newManualFindClock()
	service := newFindService(h.transfer.connections, clock.clock())
	t.Cleanup(func() {
		if gate != nil {
			gate.unblock()
		}
		result := make(chan error, 1)
		go func() { result <- service.ServiceShutdown() }()
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("Find cleanup: %v", err)
			}
		case <-time.After(5 * time.Second):
			// A broken drain must fail finitely even though test time does
			// not advance by itself. It is not accepted as clean teardown.
			clock.advance(findShutdownWait)
			t.Errorf("Find cleanup did not drain: %v", awaitConnection(t, result))
		}
	})
	return h, service, clock, gate
}

func startClockFind(t *testing.T, service *FindService) string {
	t.Helper()
	id, err := service.StartFind(t.Context(), "find-clock", "", "match")
	if err != nil {
		t.Fatal(err)
	}
	return id
}
