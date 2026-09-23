package app

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func findBM(s *FindService) string { return s.connections.ActiveBookmarkID() }

type findRequestGate struct {
	entered chan context.Context
	release chan struct{}
	once    sync.Once
	armed   atomic.Bool
}

func (g *findRequestGate) unblock() {
	g.armed.Store(false)
	g.once.Do(func() { close(g.release) })
}

// The admission hook holds a real scan worker, even when an underlying
// operation takes time to return after observing cancellation.
func gateFindRequests(t *testing.T, h *harness, waitAfterCancel bool) *findRequestGate {
	t.Helper()
	g := &findRequestGate{entered: make(chan context.Context, 8), release: make(chan struct{})}
	t.Cleanup(g.unblock)
	c := h.transfer.connections
	id := c.ActiveBookmarkID()
	cfg, generation, err := c.store.ConnectionConfig(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AdmitRequest = func(ctx context.Context) error {
		if err := c.store.WithCurrentGeneration(ctx, id, generation, nil); err != nil {
			return err
		}
		if g.armed.Load() {
			g.entered <- ctx
			if waitAfterCancel {
				<-g.release
			} else {
				select {
				case <-g.release:
				case <-ctx.Done():
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return c.store.WithCurrentGeneration(ctx, id, generation, nil)
	}
	session, err := swiftcontrol.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Authenticate(t.Context()); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.active = cachedSession{session: session, generation: generation}
	c.sessions[id] = c.active
	c.mu.Unlock()
	g.armed.Store(true)
	return g
}

func findDone(t *testing.T, service *FindService, id string) *FindStatusDTO {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		status, err := service.FindStatus(t.Context(), findBM(service), id)
		if err != nil {
			t.Fatal(err)
		}
		if status.Done {
			return status
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("Find did not complete")
		}
	}
}

func cleanupFind(t *testing.T, service *FindService, ids *[]string, gate *findRequestGate) {
	t.Helper()
	for _, id := range *ids {
		if err := service.CancelFind(context.Background(), findBM(service), id); err != nil {
			t.Errorf("Find cleanup cancellation: %v", err)
		}
	}
	if gate != nil {
		gate.unblock()
	}
	// These two public-API reproductions also run against the old service,
	// which has no shutdown hook; they still cancel every admitted id.
	if shutdown, ok := any(service).(interface{ ServiceShutdown() error }); ok {
		if err := shutdown.ServiceShutdown(); err != nil {
			t.Errorf("Find cleanup drain: %v", err)
		}
	}
}

func TestFindWorkerAdmissionBound(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	g := gateFindRequests(t, h, true)
	service := NewFindService(h.transfer.connections)
	var ids []string
	defer cleanupFind(t, service, &ids, g)
	for range 4 {
		id, err := service.StartFind(t.Context(), findBM(service), "media", "", "report", false)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		awaitConnection(t, g.entered)
	}
	id, err := service.StartFind(t.Context(), findBM(service), "media", "", "report", false)
	if id != "" {
		ids = append(ids, id)
	}
	if err == nil || id != "" {
		t.Errorf("fifth Find worker admitted: id=%q err=%v", id, err)
	}
}

func TestFindRetainedRecordBound(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.mock.AddContainer("find-retained", "standard")
	seedObject(h.mock, "find-retained", "match.txt", "data", nil)
	service := NewFindService(h.transfer.connections)
	var ids []string
	defer cleanupFind(t, service, &ids, nil)
	for range 17 {
		id, err := service.StartFind(t.Context(), findBM(service), "find-retained", "", "match", false)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		status := findDone(t, service, id)
		if status.Error != "" || status.Scanned != 1 || len(status.Matches) != 1 {
			t.Fatalf("completed scan = %+v", status)
		}
	}
	if _, err := service.FindStatus(t.Context(), findBM(service), ids[0]); err == nil {
		t.Error("oldest completed Find record survived the sixteen-record bound")
	}
	for _, id := range ids[1:] {
		if _, err := service.FindStatus(t.Context(), findBM(service), id); err != nil {
			t.Errorf("newer completed scan was evicted: %v", err)
		}
	}
}

func TestFindIdleExpiryWithoutAnotherCall(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "running"}[running], func(t *testing.T) {
			_, service, clock, gate := clockFindFixture(t, running)
			if clock.count() != 0 {
				t.Fatal("constructor started a timer")
			}
			id := startClockFind(t, service)
			var worker context.Context
			if running {
				worker = awaitConnection(t, gate.entered)
			} else {
				findDone(t, service, id)
			}
			service.mu.Lock()
			scan := service.scans[id]
			service.mu.Unlock()
			clock.advance(findIdleLease)
			service.mu.Lock()
			retained, matches := len(service.scans), len(scan.matches)
			service.mu.Unlock()
			if retained != 0 || matches != 0 || clock.count() != 0 {
				t.Errorf("idle expiry retained records=%d matches=%d timers=%d", retained, matches, clock.count())
			}
			if running && worker.Err() != context.Canceled {
				t.Error("idle expiry did not cancel the worker")
			}
			if _, err := service.FindStatus(t.Context(), findBM(service), id); err == nil {
				t.Error("expired id still answered")
			}
		})
	}
}

func TestFindLeaseRenewalAndStaleCallbacks(t *testing.T) {
	_, service, clock, gate := clockFindFixture(t, true)
	id := startClockFind(t, service)
	worker := awaitConnection(t, gate.entered)
	service.mu.Lock()
	oldCallback := service.scans[id].timer.(*manualFindTimer).callback
	service.mu.Unlock()
	for range 3 {
		if _, err := service.FindStatus(t.Context(), findBM(service), id); err != nil {
			t.Fatal(err)
		}
		if clock.count() != 1 {
			t.Errorf("poll accumulated %d timers for one record", clock.count())
		}
	}
	clock.advance(4 * time.Minute)
	if _, err := service.FindStatus(t.Context(), findBM(service), id); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Minute)
	oldCallback()
	if worker.Err() != nil {
		t.Error("old callback cancelled the renewed scan")
	}
	if clock.count() != 1 {
		t.Errorf("renewal has %d timers, want one", clock.count())
	}
	for range 4 {
		clock.advance(3 * time.Minute)
		if _, err := service.FindStatus(t.Context(), findBM(service), id); err != nil {
			t.Fatalf("actively observed scan expired: %v", err)
		}
	}
	clock.advance(findIdleLease)
	if worker.Err() != context.Canceled {
		t.Error("scan did not expire after polls stopped")
	}
}

func TestFindPollCannotReviveExpiredLease(t *testing.T) {
	_, service, clock, gate := clockFindFixture(t, true)
	id := startClockFind(t, service)
	worker := awaitConnection(t, gate.entered)
	callbacks := clock.queue(findIdleLease)
	if len(callbacks) != 1 {
		t.Fatal("expiry callback was not queued")
	}
	if _, err := service.FindStatus(t.Context(), findBM(service), id); err == nil {
		t.Error("poll revived an expired lease before its queued callback ran")
	}
	if worker.Err() != context.Canceled || clock.count() != 0 {
		t.Error("expired poll did not retire the worker and timer")
	}
	for _, callback := range callbacks {
		callback()
	}
}

func TestFindCancelledPollDoesNotRenew(t *testing.T) {
	_, service, clock, gate := clockFindFixture(t, true)
	id := startClockFind(t, service)
	worker := awaitConnection(t, gate.entered)
	clock.advance(4 * time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.FindStatus(ctx, findBM(service), id); err != context.Canceled {
		t.Errorf("cancelled poll = %v", err)
	}
	clock.advance(time.Minute)
	if worker.Err() != context.Canceled {
		t.Error("unsuccessful status call renewed the lease")
	}
}

func TestFindCancellationRetainsWorkerSlotsUntilExit(t *testing.T) {
	for _, ending := range []string{"cancel", "expire", "shutdown"} {
		t.Run(ending, func(t *testing.T) {
			_, service, clock, gate := clockFindFixture(t, true)
			var ids []string
			var workers []context.Context
			for range 4 {
				ids = append(ids, startClockFind(t, service))
				workers = append(workers, awaitConnection(t, gate.entered))
			}
			var shutdown chan error
			switch ending {
			case "cancel":
				for _, id := range ids {
					if err := service.CancelFind(t.Context(), findBM(service), id); err != nil {
						t.Fatal(err)
					}
				}
			case "expire":
				clock.advance(findIdleLease)
			case "shutdown":
				shutdown = make(chan error, 1)
				go func() { shutdown <- service.ServiceShutdown() }()
				clock.waitForShutdownTimer(t, shutdown)
			}
			for _, worker := range workers {
				if worker.Err() != context.Canceled {
					t.Error("retirement did not request cancellation")
				}
			}
			if id, err := service.StartFind(t.Context(), findBM(service), "find-clock", "", "match", false); err == nil || id != "" {
				t.Errorf("replacement admitted before old workers exited: %q %v", id, err)
			}
			service.mu.Lock()
			retained, active := len(service.scans), len(service.workers)
			service.mu.Unlock()
			if retained != 0 || active != 4 {
				t.Errorf("retirement has records=%d workers=%d, want 0/4", retained, active)
			}
			gate.unblock()
			if shutdown == nil {
				shutdown = make(chan error, 1)
				go func() { shutdown <- service.ServiceShutdown() }()
			}
			if err := awaitConnection(t, shutdown); err != nil {
				t.Fatal(err)
			}
			if clock.count() != 0 {
				t.Errorf("drained service kept %d timers", clock.count())
			}
			if err := service.ServiceShutdown(); err != nil {
				t.Errorf("repeated shutdown: %v", err)
			}
		})
	}
}

func TestFindShutdownReportsAnUndrainedWorker(t *testing.T) {
	_, service, clock, gate := clockFindFixture(t, true)
	id := startClockFind(t, service)
	worker := awaitConnection(t, gate.entered)
	if err := service.CancelFind(t.Context(), findBM(service), id); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- service.ServiceShutdown() }()
	clock.waitForShutdownTimer(t, result)
	clock.advance(findShutdownWait)
	if err := awaitConnection(t, result); err == nil {
		t.Error("shutdown reported a drain while the cancelled worker was still held")
	}
	if worker.Err() != context.Canceled {
		t.Error("shutdown left the forgotten worker uncancelled")
	}
	service.mu.Lock()
	active := len(service.workers)
	service.mu.Unlock()
	if active != 1 {
		t.Errorf("shutdown timeout released the still-running worker: %d", active)
	}
	if id, err := service.StartFind(t.Context(), findBM(service), "find-clock", "", "match", false); err == nil || id != "" {
		t.Error("shutdown timeout reopened admission")
	}
}

func TestFindQueuedCallbackCannotUndoRenewal(t *testing.T) {
	_, service, clock, gate := clockFindFixture(t, true)
	id := startClockFind(t, service)
	worker := awaitConnection(t, gate.entered)
	clock.advance(4 * time.Minute)
	baseNow := service.clock.now
	var queued []func()
	var crossed atomic.Bool
	service.mu.Lock()
	service.clock.now = func() time.Time {
		now := baseNow()
		if crossed.CompareAndSwap(false, true) {
			queued = clock.queue(time.Minute)
		}
		return now
	}
	service.mu.Unlock()
	if _, err := service.FindStatus(t.Context(), findBM(service), id); err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Fatal("old deadline did not fire while the poll held service ownership")
	}
	for _, callback := range queued {
		callback()
	}
	if worker.Err() != nil || clock.count() != 1 {
		t.Error("queued callback retired the renewed lease")
	}
}

func TestFindQueuedCallbackCannotRetireAnotherRecord(t *testing.T) {
	_, service, clock, _ := clockFindFixture(t, false)
	id := startClockFind(t, service)
	findDone(t, service, id)
	callbacks := clock.queue(findIdleLease)
	if len(callbacks) != 1 {
		t.Fatal("expiry callback was not queued")
	}
	if err := service.CancelFind(t.Context(), findBM(service), id); err != nil {
		t.Fatal(err)
	}
	replacement := startClockFind(t, service)
	findDone(t, service, replacement)
	// Force the possible id reuse after retirement without replacing the
	// service's random-id source or any of the real scan work.
	service.mu.Lock()
	scan := service.scans[replacement]
	delete(service.scans, replacement)
	scan.id = id
	service.scans[id] = scan
	service.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
	status, err := service.FindStatus(t.Context(), findBM(service), id)
	if err != nil || !status.Done || status.Scanned != 1 || len(status.Matches) != 1 {
		t.Errorf("old callback retired the replacement record: %+v %v", status, err)
	}
}

func TestFindRecordLimitIncludesRunningScans(t *testing.T) {
	h, service, _, _ := clockFindFixture(t, false)
	var complete []string
	for range 13 {
		id := startClockFind(t, service)
		findDone(t, service, id)
		complete = append(complete, id)
	}
	gate := gateFindRequests(t, h, true)
	var running []string
	for range 4 {
		running = append(running, startClockFind(t, service))
		awaitConnection(t, gate.entered)
	}
	if _, err := service.FindStatus(t.Context(), findBM(service), complete[0]); err == nil {
		t.Error("running records were excluded from the retention bound")
	}
	for _, id := range append(complete[1:], running...) {
		if _, err := service.FindStatus(t.Context(), findBM(service), id); err != nil {
			t.Errorf("newer or running record evicted: %v", err)
		}
	}
	if id, err := service.StartFind(t.Context(), findBM(service), "find-clock", "", "match", false); id != "" || err == nil {
		t.Error("full worker admission succeeded by evicting a completed record")
	}
	if _, err := service.FindStatus(t.Context(), findBM(service), complete[1]); err != nil {
		t.Error("worker overload discarded retained results")
	}
}

func TestFindEvictsByCompletionThenAdmissionOrder(t *testing.T) {
	_, service, clock, gate := clockFindFixture(t, true)
	slow := startClockFind(t, service)
	awaitConnection(t, gate.entered)
	gate.armed.Store(false)
	var earlier []string
	for range 15 {
		id := startClockFind(t, service)
		findDone(t, service, id)
		earlier = append(earlier, id)
	}
	clock.advance(time.Second)
	gate.unblock()
	findDone(t, service, slow)
	findDone(t, service, startClockFind(t, service))
	if _, err := service.FindStatus(t.Context(), findBM(service), earlier[0]); err == nil {
		t.Error("oldest completion was not evicted")
	}
	for _, id := range append(earlier[1:], slow) {
		if _, err := service.FindStatus(t.Context(), findBM(service), id); err != nil {
			t.Errorf("newer completion or tie-break successor was evicted: %v", err)
		}
	}
}

func TestFindCurrentResultsRemainBoundedAndCopied(t *testing.T) {
	h, service, _, _ := clockFindFixture(t, false)
	for i := range 1005 {
		seedObject(h.mock, "find-clock", fmt.Sprintf(" scope/Match-%04d", i), "data", nil)
	}
	seedObject(h.mock, "find-clock", " scope/unrelated", "data", nil)
	id, err := service.StartFind(t.Context(), findBM(service), "find-clock", " scope/", "mAtCh", false)
	if err != nil {
		t.Fatal(err)
	}
	status := findDone(t, service, id)
	if status.Scanned != 1006 || len(status.Matches) != 1000 || !status.Truncated || status.Error != "" {
		t.Fatalf("bounded scan status = %+v", status)
	}
	if status.Matches[0].Name != " scope/Match-0000" || status.Matches[999].Name != " scope/Match-0999" {
		t.Error("matching changed literal prefix, case-insensitive matching or list order")
	}
	status.Matches[0].Name = "caller changed snapshot"
	fresh, err := service.FindStatus(t.Context(), findBM(service), id)
	if err != nil || fresh.Matches[0].Name != " scope/Match-0000" {
		t.Errorf("caller modified retained scan results: %+v %v", fresh, err)
	}
	service.mu.Lock()
	scan := service.scans[id]
	service.mu.Unlock()
	if err := service.CancelFind(t.Context(), findBM(service), id); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	retained := len(scan.matches)
	service.mu.Unlock()
	if retained != 0 {
		t.Error("cancellation retained the match buffer")
	}
}

func TestFindCurrentErrorAndStoppedThroughput(t *testing.T) {
	_, service, clock, gate := clockFindFixture(t, true)
	id := startClockFind(t, service)
	awaitConnection(t, gate.entered)
	clock.advance(2 * time.Second)
	gate.unblock()
	status := findDone(t, service, id)
	if status.Scanned != 1 || status.Error != "" || status.ObjectsPerSecond != 0.5 {
		t.Fatalf("completed scan throughput = %+v", status)
	}
	clock.advance(time.Minute)
	status, err := service.FindStatus(t.Context(), findBM(service), id)
	if err != nil || status.ObjectsPerSecond != 0.5 {
		t.Errorf("completed throughput changed with later polls: %+v %v", status, err)
	}
	missing, err := service.StartFind(t.Context(), findBM(service), "not-a-container", "", "match", false)
	if err != nil {
		t.Fatal(err)
	}
	failed := findDone(t, service, missing)
	if failed.Error == "" || failed.Scanned != 0 || len(failed.Matches) != 0 {
		t.Errorf("current listing error was not reported: %+v", failed)
	}
	if err := service.CancelFind(t.Context(), findBM(service), "absent-id"); err != nil {
		t.Errorf("absent cancellation was not idempotent: %v", err)
	}
}
