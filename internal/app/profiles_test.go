package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/db"
)

func TestResolveSettings(t *testing.T) {
	// Balanced base with a per-job override winning (§38 layering).
	resolved, err := ResolveSettings(ProfileBalanced, TransferSettingsDTO{Transfers: 12}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Transfers != 12 || resolved.Checkers != 8 {
		t.Fatalf("balanced+override = %+v", resolved)
	}
	// Aggressive raises both.
	resolved, _ = ResolveSettings(ProfileAggressive, TransferSettingsDTO{}, nil)
	if resolved.Transfers != 16 || resolved.Checkers != 32 {
		t.Fatalf("aggressive = %+v", resolved)
	}
	// Custom is the overrides verbatim — no profile base underneath.
	resolved, _ = ResolveSettings(ProfileCustom, TransferSettingsDTO{ChunkSize: "128Mi"}, nil)
	if resolved.Transfers != 0 || resolved.ChunkSize != "128Mi" {
		t.Fatalf("custom = %+v", resolved)
	}
	// Auto prefers a recorded recommendation (§42) over its fallback.
	recommendation := &TransferSettingsDTO{Transfers: 24, Checkers: 48, ChunkSize: "256Mi"}
	resolved, _ = ResolveSettings(ProfileAuto, TransferSettingsDTO{}, recommendation)
	if resolved.Transfers != 24 || resolved.ChunkSize != "256Mi" {
		t.Fatalf("auto+recommendation = %+v", resolved)
	}
	// …with per-job overrides still winning on top.
	resolved, _ = ResolveSettings(ProfileAuto, TransferSettingsDTO{Transfers: 2}, recommendation)
	if resolved.Transfers != 2 || resolved.Checkers != 48 {
		t.Fatalf("auto+recommendation+override = %+v", resolved)
	}
	// Hostile overrides are still hostile.
	if _, err := ResolveSettings(ProfileBalanced, TransferSettingsDTO{ChunkSize: "x; rm"}, nil); err == nil {
		t.Fatal("hostile chunk size resolved")
	}
	if _, err := ResolveSettings("warp-speed", TransferSettingsDTO{}, nil); err == nil {
		t.Fatal("unknown profile resolved")
	}

	// The §45 record carries the profile.
	record := effectiveJSONWithProfile(ProfileAggressive, TransferSettingsDTO{Transfers: 16})
	if !strings.Contains(record, `"profile":"aggressive"`) || !strings.Contains(record, `"transfers":16`) {
		t.Fatalf("record = %q", record)
	}
}

func TestSettingsServicePersistsAndApplies(t *testing.T) {
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	runner := newJobRunner(nil, nil)
	service := NewSettingsService(d, runner)
	ctx := context.Background()

	settings, err := service.Get(ctx)
	if err != nil || settings.MaxActiveJobs != defaultMaxActiveJobs || settings.DefaultProfile != "auto" {
		t.Fatalf("defaults = %+v %v", settings, err)
	}
	if _, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 99, DefaultProfile: "auto"}); err == nil {
		t.Fatal("out-of-range budget accepted")
	}
	if _, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 4, DefaultProfile: "ludicrous"}); err == nil {
		t.Fatal("unknown default profile accepted")
	}
	saved, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 4, DefaultProfile: "aggressive"})
	if err != nil || saved.MaxActiveJobs != 4 {
		t.Fatal(err)
	}
	// A fresh service over the same database reads it back and applies
	// the budget at construction.
	runner2 := newJobRunner(nil, nil)
	service2 := NewSettingsService(d, runner2)
	settings, err = service2.Get(ctx)
	if err != nil || settings.MaxActiveJobs != 4 || settings.DefaultProfile != "aggressive" {
		t.Fatalf("reloaded = %+v %v", settings, err)
	}
	if cap(runner2.slots) != 4 {
		t.Fatalf("budget not applied at construction: cap %d", cap(runner2.slots))
	}
}
