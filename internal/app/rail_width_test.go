package app

import (
	"context"
	"database/sql"
	"testing"
)

// The bookmark rail's dragged width is an operator setting like the
// others: zero for the layout default, stored in pixels, refused outside
// the range a usable rail allows, and read back as the default when a
// stored row is outside it.
func TestRailWidthSetting(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	settings := NewSettingsService(h.db, h.transfer.runner)
	base := AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto"}

	got, err := settings.Get(ctx)
	if err != nil || got.RailWidthPx != 0 {
		t.Fatalf("default rail width = %d (%v), want 0", got.RailWidthPx, err)
	}

	base.RailWidthPx = 200
	if _, err := settings.Set(ctx, base); err != nil {
		t.Fatal(err)
	}
	if got, err = settings.Get(ctx); err != nil || got.RailWidthPx != 200 {
		t.Fatalf("rail width after Set = %d (%v), want 200", got.RailWidthPx, err)
	}

	for _, width := range []int{63, 481, -1} {
		base.RailWidthPx = width
		if _, err := settings.Set(ctx, base); err == nil {
			t.Errorf("rail width %d px accepted, want a refusal", width)
		}
	}
	if got, err = settings.Get(ctx); err != nil || got.RailWidthPx != 200 {
		t.Fatalf("rail width after refused writes = %d (%v), want the stored 200", got.RailWidthPx, err)
	}

	// A row written before the range existed, or by hand, reads back as
	// the default rather than as a width the layout cannot honour.
	err = h.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE app_settings SET value = '9999' WHERE key = 'rail_width_px'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err = settings.Get(ctx); err != nil || got.RailWidthPx != 0 {
		t.Fatalf("out-of-range stored width read back as %d (%v), want 0", got.RailWidthPx, err)
	}

	base.RailWidthPx = 0
	if _, err := settings.Set(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	if got, err = settings.Get(ctx); err != nil || got.RailWidthPx != 0 {
		t.Fatalf("rail width after reset = %d (%v), want 0", got.RailWidthPx, err)
	}
}
