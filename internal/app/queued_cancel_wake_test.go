package app

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/transfer/local"
)

// queueCheckContext pauses a real Err observation, without changing its value,
// so cancellation can occur after the predicate read and before Cond.Wait.
type queueCheckContext struct {
	context.Context
	checked chan struct{}
	resume  <-chan struct{}
	once    sync.Once
}

func (c *queueCheckContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() {
		close(c.checked)
		<-c.resume
	})
	return err
}

// queueWakeLocker keeps the real condition mutex. The channel only makes a
// pending wake-lock attempt durably blocked for synctest; it neither broadcasts
// nor changes admission. Wait registers its waiter before calling Unlock.
type queueWakeLocker struct {
	mu         *sync.Mutex
	permitLock <-chan struct{}
	waiting    chan struct{}
	once       sync.Once
}

func (l *queueWakeLocker) Lock() {
	<-l.permitLock
	l.mu.Lock()
}

func (l *queueWakeLocker) Unlock() {
	l.once.Do(func() { close(l.waiting) })
	l.mu.Unlock()
}

func TestQueuedCancelWake(t *testing.T) {
	for _, phase := range []string{"before_wait", "already_waiting", "not_cancelled"} {
		t.Run(phase, func(t *testing.T) {
			// Network and SQLite workers are outside the bubble. The scheduled
			// code below uses the real stores, pin and public cancellation API.
			h := newHarness(t)
			h.connect(t)
			bookmark := h.bookmarkID()
			synctest.Test(t, func(t *testing.T) {
				runner := h.transfer.runner
				// One slot, so the job launched below waits behind it.
				runner.setMaxActiveJobs(1)
				if !runner.acquireSlot(t.Context()) {
					t.Fatal("could not occupy the active slot")
				}
				held := true
				defer func() {
					if held {
						runner.releaseSlot()
					}
				}()
				owner, _, err := runner.admit(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer owner.finishStart()
				unpin, err := h.transfer.connections.pinForJob(t.Context(), bookmark)
				if err != nil {
					t.Fatal(err)
				}
				job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{
					Type: jobs.TypeDownload, ItemRecordsVersion: 1, SourceBookmarkID: bookmark,
				})
				if err != nil {
					unpin()
					t.Fatal(err)
				}
				resumeCheck, permitLock := make(chan struct{}), make(chan struct{})
				check := &queueCheckContext{Context: owner.ctx, checked: make(chan struct{}), resume: resumeCheck}
				owner.ctx = check
				locker := &queueWakeLocker{mu: &runner.mu, permitLock: permitLock, waiting: make(chan struct{})}
				runner.slotFreed.L = locker
				cancelNow, cancelLookedUp := make(chan struct{}), make(chan struct{})
				cancelResult := make(chan error, 1)
				runner.register(job.ID, func() {
					// Public CancelJob first releases the registry mutex. Retain
					// exactly that ordering while its invocation is scheduled.
					close(cancelLookedUp)
					<-cancelNow
					owner.cancel()
				})
				if phase != "not_cancelled" {
					go func() { cancelResult <- h.transfer.CancelJob(t.Context(), job.ID) }()
					<-cancelLookedUp
				}
				bodyRan, cleaned := false, false
				owner.hold(func() { unpin(); cleaned = true })
				owner.launch(job.ID, bookmark, func(ctx context.Context) {
					bodyRan = true
					h.transfer.runLocal(ctx, job.ID, bookmark, 1, job.Counters,
						func(context.Context, func(int64, int64, int64, int64)) (*local.Report, error) {
							return &local.Report{}, nil
						})
				}, func(ctx context.Context) {
					h.transfer.finishLocal(ctx, job.ID, bookmark, 1, job.Counters, jobs.StateCancelled, "", "")
				})
				<-check.checked
				if err := check.Context.Err(); err != nil {
					t.Fatalf("predicate was already cancelled: %v", err)
				}
				if phase == "before_wait" {
					close(cancelNow)
					// The old unlocked callback finishes here. A mutex-owning
					// callback instead waits at permitLock, before the same mutex.
					synctest.Wait()
				}
				close(resumeCheck)
				<-locker.waiting
				if phase == "already_waiting" {
					close(cancelNow)
				}
				close(permitLock)
				synctest.Wait()
				if phase == "not_cancelled" {
					if bodyRan || cleaned {
						t.Error("uncancelled queued job ran before the active slot was released")
					}
					assertJobBindingsPinned(t, h, []string{bookmark})
					runner.releaseSlot()
					held = false
					synctest.Wait()
				} else if err := <-cancelResult; err != nil {
					t.Fatal(err)
				}
				got, err := h.jobsSvc.Get(t.Context(), bookmark, job.ID)
				if err != nil {
					t.Fatal(err)
				}
				want := "cancelled"
				if phase == "not_cancelled" {
					want = "complete"
				}
				if got.State != want || (phase != "not_cancelled" && (got.StartedAt != "" || bodyRan)) {
					t.Errorf("queued outcome = state %s, started %q, body ran %t; want %s", got.State, got.StartedAt, bodyRan, want)
				}
				if !cleaned {
					t.Error("queued cancellation retained the credential cleanup")
				}
				runner.mu.Lock()
				active, owners, registered := runner.active, len(runner.owners), len(runner.cancels)
				runner.mu.Unlock()
				wantActive := 0
				if held {
					wantActive = 1
				}
				if active != wantActive || owners != 0 || registered != 0 {
					t.Errorf("queue ownership: active=%d owners=%d registered=%d; want %d/0/0", active, owners, registered, wantActive)
				}
				saved, err := h.bookmarks.store.Get(t.Context(), bookmark)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := h.bookmarks.store.Save(t.Context(), saved, []byte("replacement")); err != nil {
					t.Errorf("queued cancellation still prevents credential replacement: %v", err)
				}
				// Drain a broken implementation only after recording its failed
				// outcomes, without using slot release to satisfy the assertions.
				runner.mu.Lock()
				runner.slotFreed.Broadcast()
				runner.mu.Unlock()
				synctest.Wait()
			})
		})
	}
}
