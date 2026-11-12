package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// membersHarness seeds media/files/a0..a9 and a backup container, and
// connects; every object holds its own name, so a copied object's bytes say
// where they came from.
func membersHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	h.connect(t)
	h.installEngine(nil)
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("backup", "standard")
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("files/a%d", i)
		seedObject(h.mock, "media", name, name, nil)
	}
	return h, h.transfer.connections.ActiveBookmarkID()
}

// names lists a container's object names under a prefix.
func names(t *testing.T, h *harness, bookmark, container, prefix string) []string {
	t.Helper()
	var out []string
	for row, err := range h.transfer.connections.sessions[bookmark].session.Objects(context.Background(), container,
		swiftcontrol.ObjectListOptions{Deep: true, Prefix: prefix}) {
		if err != nil {
			t.Fatal(err)
		}
		if object, ok := row.(swiftcontrol.ObjectEntry); ok {
			out = append(out, object.Name)
		}
	}
	return out
}

// A copy of two selected objects out of a folder of ten plans, writes and
// verifies exactly those two, and the job says it acted on two selected
// objects (ADR 0108 decisions 3 and 5).
func TestCopyOfSelectedMembers(t *testing.T) {
	h, bookmark := membersHarness(t)
	ctx := context.Background()
	req := CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "files/",
		DestBookmarkID: bookmark, DestContainer: "backup",
		Members: []string{"files/a1", "files/a3"},
	}
	preview, err := h.transfer.PlanCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary.New != 2 || preview.MissingMembers != 0 {
		t.Fatalf("preview = %+v, missing %d; want 2 new", preview.Summary, preview.MissingMembers)
	}
	jobID, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Counters.ObjectsCopied != 2 || job.Counters.ObjectsVerified != 2 || job.SelectedMembers != 2 {
		t.Fatalf("job = %+v", job)
	}
	if got := strings.Join(names(t, h, bookmark, "backup", ""), ","); got != "a1,a3" {
		t.Fatalf("backup holds %q, want a1,a3", got)
	}
}

// A member counts as gone only when a listing finished without it: a listing
// that fails is that failure, never a selected object reported missing
// (ADR 0108 decision 3).
func TestMembersAbsenceNeedsAFinishedListing(t *testing.T) {
	h, bookmark := membersHarness(t)
	ctx := context.Background()
	req := CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "files/",
		DestBookmarkID: bookmark, DestContainer: "backup",
		Members: []string{"files/a1", "files/a3"},
	}
	h.mock.FailNextListings(10)
	if preview, err := h.transfer.PlanCopy(ctx, req); err == nil {
		t.Fatalf("a failed listing gave a preview: %+v", preview)
	}
	// An unacknowledged copy lists only once it runs, so the run's own
	// listing is the one that fails.
	h.mock.FailNextListings(10)
	jobID, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State == "complete" || strings.Contains(job.Error, "no longer at the source") {
		t.Fatalf("a failed listing ended the copy as %s: %q", job.State, job.Error)
	}
	h.mock.FailNextListings(0)
}

// A selected object the source no longer holds is counted by the preview and
// makes the run end partial with the reason, after copying the rest.
func TestCopyOfMembersWithOneMissing(t *testing.T) {
	h, bookmark := membersHarness(t)
	ctx := context.Background()
	req := CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "files/",
		DestBookmarkID: bookmark, DestContainer: "backup",
		Members: []string{"files/a1", "files/gone"},
	}
	preview, err := h.transfer.PlanCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary.New != 1 || preview.MissingMembers != 1 {
		t.Fatalf("preview = %+v, missing %d; want 1 new and 1 missing", preview.Summary, preview.MissingMembers)
	}
	jobID, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "partial" || job.Counters.ObjectsCopied != 1 || !strings.Contains(job.Error, "1 of the selected objects were no longer at the source") {
		t.Fatalf("job = %+v", job)
	}
}

// A selection that cannot be what the operator chose is refused before any
// listing or job (ADR 0108 decision 2).
func TestCopyMembersRefusals(t *testing.T) {
	h, bookmark := membersHarness(t)
	ctx := context.Background()
	settings := NewSettingsService(h.db, h.transfer.runner)
	current, err := settings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current.DownloadObjectCeiling = minDownloadObjectCeiling
	if _, err := settings.Set(ctx, current); err != nil {
		t.Fatal(err)
	}
	h.transfer.settings = settings
	over := make([]string, minDownloadObjectCeiling+1)
	for i := range over {
		over[i] = fmt.Sprintf("files/x%d", i)
	}
	for name, tc := range map[string]struct {
		members []string
		want    string
	}{
		"empty":        {[]string{""}, "has no name"},
		"the folder":   {[]string{"files/"}, `"files/" is the folder's own marker`},
		"repeated":     {[]string{"files/a1", "files/a1"}, "named twice"},
		"outside":      {[]string{"other/a1"}, "not inside"},
		"staging":      {[]string{"files/.aeronautes/stage/j/x"}, "staging area"},
		"over ceiling": {over, "selection ceiling"},
	} {
		req := CopyRequestDTO{
			SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "files/",
			DestBookmarkID: bookmark, DestContainer: "backup", Members: tc.members,
		}
		if _, err := h.transfer.PlanCopy(ctx, req); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: PlanCopy = %v, want %q", name, err, tc.want)
		}
		if _, err := h.transfer.StartCopy(ctx, req); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: StartCopy = %v, want %q", name, err, tc.want)
		}
	}
	if list, _ := h.jobsSvc.List(ctx, "", false, 10); len(list) != 0 {
		t.Errorf("refused selections created jobs: %+v", list)
	}
}

// A copy of members reads and writes only named objects, so it may write into
// a folder inside its own; a member whose destination is another member is
// refused, naming it (ADR 0108 decision 4).
func TestCopyMembersInsideTheirOwnFolder(t *testing.T) {
	h, bookmark := membersHarness(t)
	ctx := context.Background()
	req := CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "files/",
		DestBookmarkID: bookmark, DestContainer: "media", DestPrefix: "files/archive/",
		Members: []string{"files/a1", "files/a2"},
	}
	jobID, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" || job.Counters.ObjectsCopied != 2 {
		t.Fatalf("job = %+v", job)
	}
	if got := strings.Join(names(t, h, bookmark, "media", "files/archive/"), ","); got != "files/archive/a1,files/archive/a2" {
		t.Fatalf("archive holds %q", got)
	}

	// files/a0 sorts first and would land safely, so a refusal found while
	// planning could come after it was written; the refusal comes from the
	// request, before any job or write.
	onto := req
	onto.Members = []string{"files/a0", "files/a3", "files/archive/a3"}
	if _, err := h.transfer.PlanCopy(ctx, onto); err == nil || !strings.Contains(err.Error(), `"files/archive/a3", which is itself one of the selected objects`) {
		t.Fatalf("a member landing on a member: PlanCopy = %v", err)
	}
	before, _ := h.jobsSvc.List(ctx, "", false, 10)
	if _, err := h.transfer.StartCopy(ctx, onto); err == nil || !strings.Contains(err.Error(), "itself one of the selected objects") {
		t.Fatalf("a member landing on a member: StartCopy = %v", err)
	}
	if after, _ := h.jobsSvc.List(ctx, "", false, 10); len(after) != len(before) {
		t.Fatalf("a refused member copy created a job")
	}
	if got := strings.Join(names(t, h, bookmark, "media", "files/archive/"), ","); got != "files/archive/a1,files/archive/a2" {
		t.Fatalf("a refused member copy wrote: archive holds %q", got)
	}
	// The same folder as source and destination maps every member onto itself.
	same := req
	same.DestPrefix = "files/"
	if _, err := h.transfer.PlanCopy(ctx, same); err == nil || !strings.Contains(err.Error(), "itself one of the selected objects") {
		t.Fatalf("members onto themselves: PlanCopy = %v", err)
	}
}

// Re-run of a copy of members plans the same members, not the folder, even
// when the folder has gained an object since (ADR 0108 decision 5).
func TestRerunCopyOfMembers(t *testing.T) {
	h, bookmark := membersHarness(t)
	ctx := context.Background()
	jobID, err := h.transfer.StartCopy(ctx, CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "files/",
		DestBookmarkID: bookmark, DestContainer: "backup",
		Members: []string{"files/a4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" {
		t.Fatalf("job = %+v", job)
	}
	seedObject(h.mock, "media", "files/new", "files/new", nil)
	h.mock.RemoveObject("backup", "a4")
	rerun, err := h.transfer.RerunJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, rerun); job.State != "complete" || job.SelectedMembers != 1 || job.Counters.ObjectsCopied != 1 {
		t.Fatalf("rerun = %+v", job)
	}
	if got := strings.Join(names(t, h, bookmark, "backup", ""), ","); got != "a4" {
		t.Fatalf("backup holds %q after the re-run, want a4", got)
	}
}
