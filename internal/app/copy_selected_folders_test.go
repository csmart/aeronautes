package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// folderHarness adds a folder of three, one of them deeper, beside the ten
// objects of membersHarness, and an empty folder marker.
func folderHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h, bookmark := membersHarness(t)
	for _, name := range []string{"files/docs/x", "files/docs/y", "files/docs/deep/z", "files/docsier/w"} {
		seedObject(h.mock, "media", name, name, nil)
	}
	return h, bookmark
}

// ADR 0108 decision 7: a selected folder is taken with everything in it,
// beside the selected objects, and keeps its name at the destination.
func TestACopyOfFilesAndAFolderTakesEverythingInTheFolder(t *testing.T) {
	h, bookmark := folderHarness(t)
	ctx := context.Background()
	req := CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "files/",
		DestBookmarkID: bookmark, DestContainer: "backup",
		Members: []string{"files/a1", "files/a2", "files/docs/"},
	}
	preview, err := h.transfer.PlanCopy(ctx, req)
	if err != nil || preview.Summary.New != 5 || preview.MissingMembers != 0 {
		t.Fatalf("preview = %+v %v; want five new", preview, err)
	}
	id, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, id); job.State != "complete" || job.Counters.ObjectsCopied != 5 || job.SelectedMembers != 3 {
		t.Fatalf("job = %+v", job)
	}
	if got := strings.Join(names(t, h, bookmark, "backup", ""), ","); got != "a1,a2,docs/deep/z,docs/x,docs/y" {
		t.Fatalf("backup holds %q", got)
	}
	// Re-run plans the same members under the current state, so a file
	// added to the folder since is taken too (decision 5).
	seedObject(h.mock, "media", "files/docs/later", "files/docs/later", nil)
	rerun, err := h.transfer.RerunJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, rerun); job.State != "complete" || job.SelectedMembers != 3 {
		t.Fatalf("rerun = %+v", job)
	}
	if got := strings.Join(names(t, h, bookmark, "backup", "docs/later"), ","); got != "docs/later" {
		t.Fatalf("the re-run did not take the folder's new file: %q", got)
	}
}

// A folder with nothing under it is not at the source, as a missing object
// is; a member under a selected folder names nothing new and is refused.
func TestASelectedFolderWithNothingInItIsMissingAndOneInsideAnotherIsRefused(t *testing.T) {
	h, bookmark := folderHarness(t)
	ctx := context.Background()
	req := CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "files/",
		DestBookmarkID: bookmark, DestContainer: "backup",
		Members: []string{"files/a1", "files/gone/"},
	}
	if preview, err := h.transfer.PlanCopy(ctx, req); err != nil || preview.MissingMembers != 1 {
		t.Fatalf("preview = %+v %v; want the empty folder missing", preview, err)
	}
	id, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, id); job.State != "partial" || job.Counters.ObjectsCopied != 1 ||
		!strings.Contains(job.Error, "1 of the selected objects and folders were no longer at the source") {
		t.Fatalf("job = %+v", job)
	}
	nested := req
	nested.Members = []string{"files/docs/", "files/docs/x"}
	if _, err := h.transfer.PlanCopy(ctx, nested); err == nil || !strings.Contains(err.Error(), `"files/docs/x" is inside the selected folder "files/docs/"`) {
		t.Fatalf("a member inside a selected folder: %v", err)
	}
}

// In one container a write that could land on something the same run reads
// is refused before any listing or job, naming what it would write; a
// destination inside the source folder is allowed where nothing overlaps.
func TestACopyOfSelectedFoldersIsRefusedWhereItWouldWriteOverWhatItReads(t *testing.T) {
	h, bookmark := folderHarness(t)
	ctx := context.Background()
	within := func(dest string, members ...string) CopyRequestDTO {
		return CopyRequestDTO{SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "files/",
			DestBookmarkID: bookmark, DestContainer: "media", DestPrefix: dest, Members: members}
	}
	for name, tc := range map[string]struct {
		req  CopyRequestDTO
		want string
	}{
		"a folder into itself":                      {within("files/docs/", "files/docs/"), `"files/docs/docs/" inside the selected folder "files/docs/"`},
		"a folder into a selected sibling":          {within("files/docsier/", "files/docs/", "files/docsier/"), `"files/docsier/docs/" inside the selected folder "files/docsier/"`},
		"an object into a selected folder":          {within("files/docs/", "files/a1", "files/docs/"), `"files/docs/a1" inside the selected folder "files/docs/"`},
		"a folder over a selected object inside it": {within("files/up/", "files/up/docs/x", "files/docs/"), `the folder "files/up/docs/" over what is selected inside it`},
		"the source folder itself":                  {within("files/", "files/docs/"), `the selected folder "files/docs/" onto itself`},
	} {
		before, _ := h.jobsSvc.List(ctx, "", false, 50)
		if _, err := h.transfer.StartCopy(ctx, tc.req); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: StartCopy = %v, want %q", name, err, tc.want)
		}
		if after, _ := h.jobsSvc.List(ctx, "", false, 50); len(after) != len(before) {
			t.Errorf("%s: a refused copy created a job", name)
		}
	}
	id, err := h.transfer.StartCopy(ctx, within("files/backup/", "files/docs/"))
	if err != nil {
		t.Fatalf("a folder into a folder beside it inside the source: %v", err)
	}
	if job := awaitTerminal(t, h, id); job.State != "complete" || job.Counters.ObjectsCopied != 3 {
		t.Fatalf("job = %+v", job)
	}
	if got := strings.Join(names(t, h, bookmark, "media", "files/backup/"), ","); got != "files/backup/docs/deep/z,files/backup/docs/x,files/backup/docs/y" {
		t.Fatalf("files/backup/ holds %q", got)
	}
}

// The question before a selected copy replaces anything asks about the
// selected names alone: one in a selected folder and a selected object, and
// not an unselected object that also differs at the destination.
func TestASelectedCopysQuestionNamesOnlyWhatIsSelected(t *testing.T) {
	h, bookmark := folderHarness(t)
	for _, name := range []string{"a1", "a5", "docs/x"} {
		seedObject(h.mock, "backup", name, "an older "+name, nil)
	}
	confirmer := &recordingOverwriteConfirmer{yes: true}
	h.transfer.setOverwriteConfirmer(confirmer)
	id, err := h.transfer.StartCopy(context.Background(), CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "files/",
		DestBookmarkID: bookmark, DestContainer: "backup",
		Members: []string{"files/a1", "files/docs/"}, AcknowledgeOverwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, id); job.State != "complete" || job.Counters.Overwrites != 2 {
		t.Fatalf("job = %+v", job)
	}
	if asked := confirmer.questions(); len(asked) != 1 || !reflect.DeepEqual(asked[0].Names, []string{"a1", "docs/x"}) {
		t.Fatalf("asked %+v, want one question naming a1 and docs/x", asked)
	}
}
