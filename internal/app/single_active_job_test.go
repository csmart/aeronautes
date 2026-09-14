package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/db"
)

// Maintained regressions for ADR 0047. One transfer job runs at a time,
// enforced rather than defaulted: no stored setting and no caller of the
// resize path can raise a runner above the ceiling, and a second start
// queues rather than being refused. These replace the probe archived with
// the plan, whose three observations are the three refusals asserted here.

func TestOneActiveJobIsEnforced(t *testing.T) {
	ctx := context.Background()

	t.Run("a production runner admits one", func(t *testing.T) {
		r := newJobRunner(nil, nil)
		if !r.acquireSlot(ctx) {
			t.Fatal("the first slot must admit")
		}
		second := make(chan bool, 1)
		go func() { second <- r.acquireSlot(ctx) }()
		select {
		case <-second:
			t.Fatal("a second job was admitted beside the first")
		case <-time.After(200 * time.Millisecond):
		}
		// The queue is unchanged: the waiter runs when the slot frees,
		// rather than having been refused.
		r.releaseSlot()
		select {
		case ok := <-second:
			if !ok {
				t.Fatal("the queued job reported refusal instead of admission")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the queued job never ran after the slot freed")
		}
	})

	t.Run("the resize path cannot raise a runner", func(t *testing.T) {
		r := newJobRunner(nil, nil)
		r.setMaxActiveJobs(16)
		r.mu.Lock()
		capacity := r.capacity
		r.mu.Unlock()
		if capacity != maxActiveJobsCeiling {
			t.Fatalf("resize to 16 left capacity %d, want the ceiling %d", capacity, maxActiveJobsCeiling)
		}
		if !r.acquireSlot(ctx) {
			t.Fatal("the first slot must admit")
		}
		admitted := make(chan struct{})
		go func() {
			if r.acquireSlot(ctx) {
				close(admitted)
			}
		}()
		select {
		case <-admitted:
			t.Fatal("a raised setting admitted a second job")
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("a zero or negative resize falls back within the ceiling", func(t *testing.T) {
		r := newJobRunner(nil, nil)
		r.setMaxActiveJobs(0)
		r.mu.Lock()
		capacity := r.capacity
		r.mu.Unlock()
		if capacity != maxActiveJobsCeiling {
			t.Fatalf("fallback left capacity %d, want the ceiling %d", capacity, maxActiveJobsCeiling)
		}
	})
}

func TestSettingsStateTheEffectiveCeiling(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	runner := newJobRunner(nil, nil)
	service := NewSettingsService(d, runner)

	// A write above the ceiling is refused and names it, rather than being
	// clamped silently or stored as a value that would not apply.
	_, err = service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 4, DefaultProfile: "auto"})
	if err == nil {
		t.Fatal("a budget above the ceiling was accepted")
	}
	if !strings.Contains(err.Error(), "exceeds the enforced ceiling") {
		t.Fatalf("refusal does not name the ceiling: %v", err)
	}

	// A row persisted before the ceiling was enforced is reported at the
	// effective value, and the stored row itself is left as written so the
	// decision can be reverted to what was last asked for.
	seedStoredBudget(t, d, 16)
	settings, err := service.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.MaxActiveJobs != maxActiveJobsCeiling {
		t.Fatalf("a stored 16 was reported as %d, want the effective %d",
			settings.MaxActiveJobs, maxActiveJobsCeiling)
	}
	if stored := storedBudget(t, d); stored != 16 {
		t.Fatalf("the persisted row was rewritten to %d; it must be left as written", stored)
	}

	// Construction applies the persisted value through the same clamp.
	runner2 := newJobRunner(nil, nil)
	NewSettingsService(d, runner2)
	runner2.mu.Lock()
	capacity := runner2.capacity
	runner2.mu.Unlock()
	if capacity != maxActiveJobsCeiling {
		t.Fatalf("a stored 16 seeded capacity %d at construction, want %d", capacity, maxActiveJobsCeiling)
	}
}

// seedStoredBudget writes max_active_jobs directly, standing in for a row
// persisted before the ceiling was enforced.
func seedStoredBudget(t *testing.T, d *db.DB, n int) {
	t.Helper()
	if err := d.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(),
			`INSERT INTO app_settings (key, value) VALUES ('max_active_jobs', ?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(n))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// storedBudget reads the row back, so a test can tell reporting the
// effective value apart from rewriting what was stored.
func storedBudget(t *testing.T, d *db.DB) int {
	t.Helper()
	var value string
	if err := d.Reader().QueryRow(
		`SELECT value FROM app_settings WHERE key = 'max_active_jobs'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
