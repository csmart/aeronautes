package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Maintained regressions for the active-job budget (ADR 0105, which raised
// ADR 0047's one): two jobs run at once by default and four at most. The
// ceiling is enforced rather than defaulted: no stored setting and no caller
// of the resize path can raise a runner above it, and a start beyond the
// budget queues rather than being refused.

// holdSlots takes n slots and reports whether one more is admitted at once.
func holdSlots(t *testing.T, r *jobRunner, n int) (overflow chan bool) {
	t.Helper()
	ctx := context.Background()
	for i := range n {
		if !r.acquireSlot(ctx) {
			t.Fatalf("slot %d of %d must admit", i+1, n)
		}
	}
	overflow = make(chan bool, 1)
	go func() { overflow <- r.acquireSlot(ctx) }()
	select {
	case <-overflow:
		t.Fatalf("a job was admitted beside the %d the budget allows", n)
	case <-time.After(200 * time.Millisecond):
	}
	return overflow
}

func TestTheActiveJobBudgetIsEnforced(t *testing.T) {
	t.Run("a production runner admits two and queues the third", func(t *testing.T) {
		r := newJobRunner(nil, nil)
		third := holdSlots(t, r, defaultMaxActiveJobs)
		// The queue is unchanged: the waiter runs when a slot frees,
		// rather than having been refused.
		r.releaseSlot()
		select {
		case ok := <-third:
			if !ok {
				t.Fatal("the queued job reported refusal instead of admission")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the queued job never ran after a slot freed")
		}
	})

	t.Run("the resize path cannot raise a runner above four", func(t *testing.T) {
		r := newJobRunner(nil, nil)
		r.setMaxActiveJobs(16)
		r.mu.Lock()
		capacity := r.capacity
		r.mu.Unlock()
		if maxActiveJobsCeiling != 4 || capacity != maxActiveJobsCeiling {
			t.Fatalf("resize to 16 left capacity %d, want the ceiling of four", capacity)
		}
		holdSlots(t, r, 4)
	})

	t.Run("a zero or negative resize falls back to the default", func(t *testing.T) {
		r := newJobRunner(nil, nil)
		r.setMaxActiveJobs(0)
		r.mu.Lock()
		capacity := r.capacity
		r.mu.Unlock()
		if defaultMaxActiveJobs != 2 || capacity != defaultMaxActiveJobs {
			t.Fatalf("fallback left capacity %d, want the default of two", capacity)
		}
	})
}

// ADR 0105 decisions 6 and 7: one range with one refusal; no settings file
// reads the default; a hand-written value outside the range makes the file
// unreadable, so the default is in force and the file is left as written
// (ADR 0109 decision 1).
func TestSettingsStateTheEffectiveBudget(t *testing.T) {
	ctx := context.Background()
	runner := newJobRunner(nil, nil)
	path := settingsFile(t)
	service := NewSettingsService(path, runner)

	if settings, err := service.Get(ctx); err != nil || settings.MaxActiveJobs != 2 {
		t.Fatalf("no settings file reads a budget of %d, %v; want the default of two", settings.MaxActiveJobs, err)
	}
	// A write outside the range is refused and names the range, rather than
	// being clamped silently or stored as a value that would not apply.
	var err error
	for _, outside := range []int{0, 5, 16} {
		_, err = service.Set(ctx, AppSettingsDTO{MaxActiveJobs: outside, DefaultProfile: "auto"})
		if err == nil || !strings.Contains(err.Error(), "choose 1 to 4") {
			t.Fatalf("a budget of %d: %v; want a refusal naming the range", outside, err)
		}
	}
	// A refused save stores and applies nothing, whichever field it was
	// refused for, and says so in the fields the bridge hands to the window
	// as the rejection's cause. The window relies on both: it goes on
	// treating what it read before the save as current only for such a
	// refusal.
	_, err = service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 3, DefaultProfile: "no such profile"})
	if err == nil {
		t.Fatal("a document with an unknown profile was saved")
	}
	// The bridge's own encoding of a returned error (Wails' default
	// error marshaller).
	if cause, marshalErr := json.Marshal(&err); marshalErr != nil || !strings.Contains(string(cause), `"refused":true`) {
		t.Fatalf("the refusal reaches the window as %s, %v; want it marked refused", cause, marshalErr)
	}
	runner.mu.Lock()
	capacity := runner.capacity
	runner.mu.Unlock()
	if settings, err := service.Get(ctx); err != nil || settings.MaxActiveJobs != 2 || capacity != 2 {
		t.Fatalf("after refused saves the budget reads %d and the runner admits %d, %v; want both unchanged at two", settings.MaxActiveJobs, capacity, err)
	}
	for _, inside := range []int{1, 3, 4} {
		if saved, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: inside, DefaultProfile: "auto"}); err != nil || saved.MaxActiveJobs != inside {
			t.Fatalf("a budget of %d was not kept: %+v %v", inside, saved, err)
		}
	}

	// A budget written by hand outside the range is not clamped: the whole
	// file is unreadable, the default is in force at once and at
	// construction, the report names the reason, and the file is left as
	// written until the operator saves.
	for _, written := range []string{"16", "0"} {
		hand := []byte(`{"maxActiveJobs": ` + written + `, "defaultProfile": "auto"}`)
		if err := os.WriteFile(path, hand, 0o600); err != nil {
			t.Fatal(err)
		}
		settings, err := service.Get(ctx)
		if err != nil || settings.MaxActiveJobs != defaultMaxActiveJobs || !strings.Contains(settings.FileProblem, "max active jobs "+written+" is out of range") {
			t.Fatalf("a hand-written %s reads as %d (%v, %q); want the default and the reason", written, settings.MaxActiveJobs, err, settings.FileProblem)
		}
		runner2 := newJobRunner(nil, nil)
		NewSettingsService(path, runner2)
		runner2.mu.Lock()
		capacity = runner2.capacity
		runner2.mu.Unlock()
		if capacity != defaultMaxActiveJobs {
			t.Fatalf("a hand-written %s seeded capacity %d at construction, want the default %d", written, capacity, defaultMaxActiveJobs)
		}
		if kept, err := os.ReadFile(path); err != nil || string(kept) != string(hand) {
			t.Fatalf("the hand-written file was rewritten to %q (%v)", kept, err)
		}
	}
}
