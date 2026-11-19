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

// WireAuditFilePicker attaches the shell's native directory dialog so the
// §51 audit export can write to a folder the operator chooses. A service
// without one has no dialog and refuses to export.
func WireAuditFilePicker(s *AuditService, picker FilePicker) {
	s.setFilePicker(picker)
}

// WireBackupFilePicker attaches the shell's native directory dialog so the
// settings backup can write to a folder the operator chooses (ADR 0098). A
// service without one has no dialog and refuses to export.
func WireBackupFilePicker(s *BackupService, picker FilePicker) {
	s.setFilePicker(picker)
}

// WireOverwriteConfirmer attaches the shell's native confirmation for an
// upload that would replace existing objects (ADR 0081 decision 2). A
// service without one refuses to replace anything.
func WireOverwriteConfirmer(s *TransferService, confirmer OverwriteConfirmer) {
	s.setOverwriteConfirmer(confirmer)
}

// WireConfirmer attaches the shell's native confirmation for public
// read ACLs (ADR 0073 option B).
func WireConfirmer(s *MutateService, confirmer PublicACLConfirmer) {
	s.setConfirmer(confirmer)
}

// WireDownloadEngineBoundary lets an owned fixture hold a download before
// each of its engine children starts; returning an error stops that child.
// It is composition, never a bridge method. Configure before starting jobs.
func WireDownloadEngineBoundary(s *TransferService, before func(context.Context, string) error) {
	s.downloadBoundary = before
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

// WireBenchmarkRecommendations connects the benchmark store's lookup to
// the transfer service — composition, never a bridge method.
func WireBenchmarkRecommendations(ts *TransferService, bench *BenchmarkService) {
	ts.setRecommendationSource(bench.recommendationFor)
}

// WireConnectionCap gives sessions the operator's per-host connection
// cap (ADR 0074 decision 4), read when a session is opened; a settings
// read that fails leaves the session package's default in force.
func WireConnectionCap(c *Connections, s *SettingsService) {
	c.connectionCap = func(ctx context.Context) int {
		settings, err := s.Get(ctx)
		if err != nil {
			return 0
		}
		return settings.MaxConnsPerHost
	}
}
