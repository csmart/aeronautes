package app

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// downloadMembersHarness seeds media/scope/a0..a9, picks a destination
// directory and returns a request for the scope.
func downloadMembersHarness(t *testing.T) (*harness, DownloadRequestDTO, string) {
	t.Helper()
	h := newHarness(t)
	h.connect(t)
	h.installEngine(nil)
	h.mock.AddContainer("media", "standard")
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("scope/a%d", i)
		seedObject(h.mock, "media", name, name, nil)
	}
	dest := t.TempDir()
	h.transfer.setFilePicker(fakePicker{dir: dest})
	bookmark := h.transfer.connections.ActiveBookmarkID()
	picked, err := h.transfer.PickDownloadDir(t.Context(), SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	return h, DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/", DestDirToken: picked.Token}, dest
}

// A download of two selected objects out of ten plans and writes exactly
// those two files, and the job says it acted on two (ADR 0108).
func TestDownloadOfSelectedMembers(t *testing.T) {
	h, req, dest := downloadMembersHarness(t)
	req.Members = []string{"scope/a1", "scope/a3"}
	view, err := h.transfer.PreflightDownload(t.Context(), req)
	if err != nil || view == nil || view.Rejected || view.Objects != 2 {
		t.Fatalf("preflight: %+v %v", view, err)
	}
	jobID, err := h.transfer.StartDownload(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" || job.Counters.ObjectsCopied != 2 || job.SelectedMembers != 2 {
		t.Fatalf("job = %+v", job)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	if strings.Join(files, ",") != "a1,a3" {
		t.Fatalf("destination holds %q, want a1,a3", files)
	}
}

// A selected object the source no longer holds refuses the preflight by
// name, the last check before a download starts; a selection that cannot
// be what the operator chose is refused outright (ADR 0108 decisions 2-3).
func TestDownloadMembersRefusals(t *testing.T) {
	h, req, _ := downloadMembersHarness(t)
	ctx := t.Context()
	missing := req
	missing.Members = []string{"scope/a1", "scope/gone"}
	view, err := h.transfer.PreflightDownload(ctx, missing)
	if err != nil || view == nil || !view.Rejected || len(view.ProblemSamples) != 1 ||
		view.ProblemSamples[0].Kind != "selection-missing" || view.ProblemSamples[0].Name != "scope/gone" {
		t.Fatalf("a missing member did not refuse the preflight: %+v %v", view, err)
	}
	if _, err := h.transfer.StartDownload(ctx, missing); err == nil {
		t.Fatal("StartDownload started with a missing member")
	}
	// A listing that fails is that failure, not a missing member.
	h.mock.FailNextListings(10)
	present := req
	present.Members = []string{"scope/a1"}
	if view, err := h.transfer.PreflightDownload(ctx, present); err == nil {
		t.Fatalf("a failed listing gave a preflight: %+v", view)
	}
	h.mock.FailNextListings(0)

	settings := NewSettingsService(h.db, h.transfer.runner)
	if _, err := settings.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", DownloadObjectCeiling: minDownloadObjectCeiling}); err != nil {
		t.Fatal(err)
	}
	WireSettings(h.transfer, settings)
	over := make([]string, minDownloadObjectCeiling+1)
	for i := range over {
		over[i] = fmt.Sprintf("scope/x%d", i)
	}
	for name, tc := range map[string]struct {
		members []string
		want    string
	}{
		"outside":      {[]string{"other/a1"}, "not inside"},
		"repeated":     {[]string{"scope/a1", "scope/a1"}, "named twice"},
		"over ceiling": {over, "selection ceiling"},
	} {
		bad := req
		bad.Members = tc.members
		if _, err := h.transfer.PreflightDownload(ctx, bad); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: PreflightDownload = %v, want %q", name, err, tc.want)
		}
	}
	if list, _ := h.jobsSvc.List(ctx, "", false, 10); len(list) != 0 {
		t.Errorf("refused selections created jobs: %+v", list)
	}
}
