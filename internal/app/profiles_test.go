package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
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
	if err != nil || settings.MaxActiveJobs != defaultMaxActiveJobs || settings.DefaultProfile != "auto" || settings.DownloadObjectCeiling != defaultDownloadObjectCeiling {
		t.Fatalf("defaults = %+v %v", settings, err)
	}
	// The download selection ceiling (ADR 0072) is refused outside its
	// range, kept at the default when a document omits it, and read back
	// as saved inside the range.
	for _, ceiling := range []int{-1, 1, 999, 5_000_001} {
		if _, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", DownloadObjectCeiling: ceiling}); err == nil {
			t.Fatalf("ceiling %d accepted", ceiling)
		}
	}
	if saved, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", DownloadObjectCeiling: 12_345}); err != nil || saved.DownloadObjectCeiling != 12_345 {
		t.Fatalf("ceiling save = %+v %v", saved, err)
	}
	if read, err := service.Get(ctx); err != nil || read.DownloadObjectCeiling != 12_345 {
		t.Fatalf("ceiling read = %+v %v", read, err)
	}
	if saved, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto"}); err != nil || saved.DownloadObjectCeiling != defaultDownloadObjectCeiling {
		t.Fatalf("omitted ceiling = %+v %v", saved, err)
	}
	// The browser retention ceiling and the queue depth (ADR 0074
	// decisions 1 and 2) take the same treatment, and the depth reaches
	// the runner on every save.
	if settings.BrowserRetentionCeiling != defaultBrowserRetentionCeiling || settings.MaxQueuedJobs != defaultMaxQueuedJobs {
		t.Fatalf("workload defaults = %+v", settings)
	}
	for _, retention := range []int{-1, 1, 999, 5_000_001} {
		if _, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", BrowserRetentionCeiling: retention}); err == nil {
			t.Fatalf("retention ceiling %d accepted", retention)
		}
	}
	for _, depth := range []int{-1, 1_001} {
		if _, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", MaxQueuedJobs: depth}); err == nil {
			t.Fatalf("queue depth %d accepted", depth)
		}
	}
	if saved, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", BrowserRetentionCeiling: 2_000, MaxQueuedJobs: 3}); err != nil ||
		saved.BrowserRetentionCeiling != 2_000 || saved.MaxQueuedJobs != 3 {
		t.Fatalf("workload save = %+v %v", saved, err)
	}
	if read, err := service.Get(ctx); err != nil || read.BrowserRetentionCeiling != 2_000 || read.MaxQueuedJobs != 3 {
		t.Fatalf("workload read = %+v %v", read, err)
	}
	if depth := runner.queueDepth(); depth != 3 {
		t.Fatalf("runner queue depth after save = %d", depth)
	}
	if saved, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto"}); err != nil ||
		saved.BrowserRetentionCeiling != defaultBrowserRetentionCeiling || saved.MaxQueuedJobs != defaultMaxQueuedJobs {
		t.Fatalf("omitted workload bounds = %+v %v", saved, err)
	}
	if depth := runner.queueDepth(); depth != defaultMaxQueuedJobs {
		t.Fatalf("runner queue depth after an omitted save = %d", depth)
	}
	// The native client's per-host connection cap (ADR 0074 decision 4)
	// takes the same treatment.
	if settings.MaxConnsPerHost != defaultMaxConnsPerHost {
		t.Fatalf("connection cap default = %+v", settings)
	}
	for _, cap := range []int{-1, 257} {
		if _, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", MaxConnsPerHost: cap}); err == nil {
			t.Fatalf("connection cap %d accepted", cap)
		}
	}
	if saved, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", MaxConnsPerHost: 3}); err != nil || saved.MaxConnsPerHost != 3 {
		t.Fatalf("connection cap save = %+v %v", saved, err)
	}
	if read, err := service.Get(ctx); err != nil || read.MaxConnsPerHost != 3 {
		t.Fatalf("connection cap read = %+v %v", read, err)
	}
	if saved, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto"}); err != nil || saved.MaxConnsPerHost != defaultMaxConnsPerHost {
		t.Fatalf("omitted connection cap = %+v %v", saved, err)
	}
	// A runner built for a store that already holds a depth starts with it.
	if depth := NewSettingsService(d, newJobRunner(nil, nil)).runner.queueDepth(); depth != defaultMaxQueuedJobs {
		t.Fatalf("fresh runner queue depth = %d", depth)
	}
	if _, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", MaxQueuedJobs: 5}); err != nil {
		t.Fatal(err)
	}
	if depth := NewSettingsService(d, newJobRunner(nil, nil)).runner.queueDepth(); depth != 5 {
		t.Fatalf("runner queue depth at construction = %d", depth)
	}
	if _, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 99, DefaultProfile: "auto"}); err == nil {
		t.Fatal("out-of-range budget accepted")
	}
	if _, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 4, DefaultProfile: "ludicrous"}); err == nil {
		t.Fatal("unknown default profile accepted")
	}
	// Four was a valid save until the §38 ceiling became a limit rather
	// than a default (ADR 0047). It is now refused, naming the ceiling,
	// rather than stored as a value that would not apply.
	if _, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 4, DefaultProfile: "aggressive"}); err == nil {
		t.Fatal("a budget above the enforced ceiling was accepted")
	}
	saved, err := service.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "aggressive"})
	if err != nil || saved.MaxActiveJobs != 1 {
		t.Fatal(err)
	}
	// A fresh service over the same database reads it back and applies
	// the budget at construction.
	runner2 := newJobRunner(nil, nil)
	service2 := NewSettingsService(d, runner2)
	settings, err = service2.Get(ctx)
	if err != nil || settings.MaxActiveJobs != 1 || settings.DefaultProfile != "aggressive" {
		t.Fatalf("reloaded = %+v %v", settings, err)
	}
	runner2.mu.Lock()
	capacity := runner2.capacity
	runner2.mu.Unlock()
	if capacity != 1 {
		t.Fatalf("budget not applied at construction: capacity %d", capacity)
	}
}

func TestRetentionPrunesExactly(t *testing.T) {
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	jobStore := jobs.NewStore(d)
	auditStore := audit.NewStore(d)
	runner := newJobRunner(jobStore, auditStore)
	service := NewSettingsService(d, runner)

	old := time.Now().AddDate(0, 0, -120).UTC().Format(time.RFC3339)
	// Old terminal job; old terminal job WITH recovery records; old
	// RUNNING job; fresh terminal job.
	mk := func(state string) string {
		j, _ := jobStore.Create(ctx, jobs.Job{Type: jobs.TypeCopy})
		_ = jobStore.Transition(ctx, j.ID, jobs.StatePlanning)
		_ = jobStore.Transition(ctx, j.ID, jobs.StateRunning)
		if state != "running" {
			_ = jobStore.Transition(ctx, j.ID, jobs.StateCancelled)
		}
		return j.ID
	}
	oldDone := mk("done")
	oldWithRecovery := mk("done")
	running := mk("running")
	freshDone := mk("done")
	// Backdate the two old terminal rows.
	if err := d.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET finished_at=? WHERE id IN (?, ?)`, old, oldDone, oldWithRecovery)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := jobStore.RecoveryAdd(ctx, jobs.RecoveryRecord{
		JobID: oldWithRecovery, BookmarkID: "bm", Kind: jobs.RecoveryStaging, Container: "c", Prefix: "p/",
	}); err != nil {
		t.Fatal(err)
	}
	// Old and fresh audit events.
	_ = auditStore.Append(ctx, audit.Event{BookmarkID: "bm", Operation: "x.old", Resource: "r", Summary: "s", Result: audit.ResultApplied})
	_ = auditStore.Append(ctx, audit.Event{BookmarkID: "bm", Operation: "x.new", Resource: "r", Summary: "s", Result: audit.ResultApplied})
	if err := d.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE audit_events SET at=? WHERE operation='x.old'`, old)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	service.runRetention(ctx, jobStore, auditStore)

	if _, err := jobStore.Get(ctx, oldDone); err == nil {
		t.Fatal("old terminal job survived the prune")
	}
	for name, id := range map[string]string{"recovery-held": oldWithRecovery, "running": running, "fresh": freshDone} {
		if _, err := jobStore.Get(ctx, id); err != nil {
			t.Fatalf("%s job wrongly pruned: %v", name, err)
		}
	}
	events, _ := auditStore.List(ctx, audit.Query{Limit: 10})
	for _, e := range events {
		if e.Operation == "x.old" {
			t.Fatal("old audit event survived")
		}
	}
	saw := false
	for _, e := range events {
		if e.Operation == "x.new" {
			saw = true
		}
	}
	if !saw {
		t.Fatal("fresh audit event pruned")
	}

	// Retention 0 disables: nothing further is pruned.
	if _, err := service.Set(ctx, AppSettingsDTO{
		MaxActiveJobs: 1, DefaultProfile: "auto", AuditRetentionDays: 0, JobRetentionDays: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET finished_at=? WHERE id=?`, old, freshDone)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	service.runRetention(ctx, jobStore, auditStore)
	if _, err := jobStore.Get(ctx, freshDone); err != nil {
		t.Fatal("prune ran with retention disabled")
	}
}
