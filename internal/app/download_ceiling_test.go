package app

import (
	"fmt"
	"testing"
)

// ADR 0072 decision 4: the download selection ceiling the operator sets in
// the application settings is the ceiling the transfer service applies at
// preflight, so the dialog's refusal and StartDownload's refusal are one
// and the same; an unwired service applies the default.
func TestDownloadPreflightAppliesTheSettingsCeiling(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.connect(t)
	h.installEngine(nil)
	h.mock.AddContainer("media", "standard")
	const n = minDownloadObjectCeiling + 1
	for i := range n {
		seedObject(h.mock, "media", fmt.Sprintf("scope/o-%05d.bin", i), "x", nil)
	}
	dest := t.TempDir()
	h.transfer.setFilePicker(fakePicker{dir: dest})
	bookmark := h.transfer.connections.ActiveBookmarkID()
	picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	req := DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/", DestDirToken: picked.Token}

	// Unwired: the default ceiling, far above this scope.
	if h.transfer.downloadObjectCeiling(ctx) != defaultDownloadObjectCeiling {
		t.Fatalf("unwired ceiling = %d", h.transfer.downloadObjectCeiling(ctx))
	}
	view, err := h.transfer.PreflightDownload(ctx, req)
	if err != nil || view == nil || view.Rejected || view.Objects != n {
		t.Fatalf("default ceiling refused a scope of %d: %+v %v", n, view, err)
	}

	settings := NewSettingsService(settingsFile(t), h.transfer.runner)
	if _, err := settings.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", DownloadObjectCeiling: minDownloadObjectCeiling}); err != nil {
		t.Fatal(err)
	}
	WireSettings(h.transfer, settings)
	view, err = h.transfer.PreflightDownload(ctx, req)
	if err != nil || view == nil || !view.Rejected || view.Objects != 0 || view.ProblemCount != 1 || len(view.ProblemSamples) != 1 ||
		view.ProblemSamples[0].Kind != "selection-ceiling" {
		t.Fatalf("settings ceiling %d did not refuse a scope of %d: %+v %v", minDownloadObjectCeiling, n, view, err)
	}
	if _, err := h.transfer.StartDownload(ctx, req); err == nil {
		t.Fatal("StartDownload started a refused selection")
	}
	if _, err := settings.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", DownloadObjectCeiling: n}); err != nil {
		t.Fatal(err)
	}
	view, err = h.transfer.PreflightDownload(ctx, req)
	if err != nil || view == nil || view.Rejected || view.Objects != n {
		t.Fatalf("a raised ceiling did not plan the scope: %+v %v", view, err)
	}
}
