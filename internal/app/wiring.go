package app

import (
	"context"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
)

// Composition hooks for main.go — package FUNCTIONS, never service
// methods: the Wails binding generator exposes a bound service's
// exported methods to the page, and a compromised page must not be able
// to retarget the evidence store, swap the engine factory, or trigger
// sweeps (the review wave's G19). Tests inside this package use the
// unexported methods directly.

// WireEvidenceStore attaches §28.6(b) ownership recording.
func WireEvidenceStore(s *TransferService, store *evidence.Store) {
	s.setEvidenceStore(store)
}

// WireFilePicker attaches the shell's native dialog implementation.
func WireFilePicker(s *TransferService, picker FilePicker) {
	s.setFilePicker(picker)
}

// StartupCrashSweep replays reconciliation for jobs that died before
// their own could run.
func StartupCrashSweep(ctx context.Context, s *TransferService) (int, error) {
	return s.sweepCrashLitter(ctx)
}

// RunStartupRetention applies the persisted §51 retention policy.
func RunStartupRetention(ctx context.Context, s *SettingsService, jobStore *jobs.Store, auditStore *audit.Store) {
	s.runRetention(ctx, jobStore, auditStore)
}

// BenchmarkScale overrides the benchmark workload — integration tests
// shrink it against real endpoints.
func BenchmarkScale(s *BenchmarkService, objects int, levels []int) {
	s.setScale(objects, levels)
}

// WireRecommendationSource attaches the benchmark store's per-bookmark
// recommendation lookup.
func WireRecommendationSource(s *TransferService, f func(bookmarkID string) *TransferSettingsDTO) {
	s.setRecommendationSource(f)
}

// WireBenchmarkRecommendations connects the benchmark store's lookup to
// the transfer service — composition, never a bridge method.
func WireBenchmarkRecommendations(ts *TransferService, bench *BenchmarkService) {
	ts.setRecommendationSource(bench.recommendationFor)
}

// RecoverySweepStep runs one supervised crash-sweep pass — main.go's
// loop calls this package FUNCTION so the step never becomes a bound
// bridge method (review R-13).
func RecoverySweepStep(ctx context.Context, s *TransferService) (hadWork bool, err error) {
	return s.recoverySweepStep(ctx)
}
