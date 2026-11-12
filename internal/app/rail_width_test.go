package app

import (
	"context"
	"os"
	"strings"
	"testing"
)

// The bookmark rail's dragged width is an operator setting like the
// others: zero for the layout default, stored in pixels, refused outside
// the range a usable rail allows, and read back as the default when a
// hand-written width is outside it.
func TestRailWidthSetting(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	settings := NewSettingsService(settingsFile(t), h.transfer.runner)
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

	// A width written by hand outside the range makes the file unreadable,
	// so the default is in force rather than a width the layout cannot
	// honour, and the report says why (ADR 0109 decision 1).
	if err := os.WriteFile(settings.path, []byte(`{"maxActiveJobs": 1, "defaultProfile": "auto", "railWidthPx": 9999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err = settings.Get(ctx); err != nil || got.RailWidthPx != 0 || !strings.Contains(got.FileProblem, "rail width 9999") {
		t.Fatalf("out-of-range hand-written width read back as %d (%v, %q), want 0 and the reason", got.RailWidthPx, err, got.FileProblem)
	}

	base.RailWidthPx = 0
	if _, err := settings.Set(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	if got, err = settings.Get(ctx); err != nil || got.RailWidthPx != 0 {
		t.Fatalf("rail width after reset = %d (%v), want 0", got.RailWidthPx, err)
	}
}
