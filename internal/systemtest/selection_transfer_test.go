package systemtest

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestCopySelectedMembersRealSwift: through the engine against the owned
// Swift, a copy of two selected objects out of a folder of five writes
// exactly those two, with the source's bytes, verifies two, and writes
// nothing else anywhere on the account (ADR 0108).
func TestCopySelectedMembersRealSwift(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	src := h.Container(h.Primary, "selsrc")
	dst := h.Container(h.Primary, "seldst")
	for i := range 5 {
		h.Seed(h.Primary, src, fmt.Sprintf("files/f%d", i), []byte(fmt.Sprintf("selected body %d", i)), nil)
	}
	before := h.SnapshotNamespace(h.Primary)

	rec := h.StartCopy(ctx, app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "files/",
		DestBookmarkID: h.Primary.BookmarkID, DestContainer: dst,
		Members: []string{"files/f1", "files/f3"},
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	job := h.AwaitJob(rec.JobID())
	if job.State != "complete" || job.Counters.ObjectsCopied != 2 || job.Counters.ObjectsVerified != 2 || job.SelectedMembers != 2 {
		t.Fatalf("selected copy = %+v", job)
	}

	after := h.SnapshotNamespace(h.Primary)
	from, to := before.Containers[src].Objects, after.Containers[dst].Objects
	for _, name := range []string{"f1", "f3"} {
		if to[name].Digest != from["files/"+name].Digest || to[name].Size != from["files/"+name].Size {
			t.Fatalf("%s: digest %s (%d), want the source's %s (%d)", name, to[name].Digest, to[name].Size, from["files/"+name].Digest, from["files/"+name].Size)
		}
	}
	h.AssertNoCollateral(before, after, dst+"/f1", dst+"/f3")
	rec.CreditHappy()
}

// TestDownloadSelectedMembersRealSwift: a download of two selected objects
// out of a folder of five places exactly those two files, through the
// engine, and the job says it acted on two (ADR 0108).
func TestDownloadSelectedMembersRealSwift(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	src := h.Container(h.Primary, "seldl")
	for i := range 5 {
		h.Seed(h.Primary, src, fmt.Sprintf("scope/f%d", i), []byte(fmt.Sprintf("downloaded body %d", i)), nil)
	}
	root := t.TempDir()
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: root})
	picked, err := h.Transfer.PickDownloadDir(ctx, app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: src, Prefix: "scope/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	rec := h.StartDownload(ctx, app.DownloadRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: src, Prefix: "scope/", DestDirToken: picked.Token,
		Members: []string{"scope/f1", "scope/f3"},
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	job := h.AwaitJob(rec.JobID())
	if job.State != "complete" || job.Counters.ObjectsCopied != 2 || job.Counters.ObjectsVerified != 2 || job.SelectedMembers != 2 {
		t.Fatalf("selected download = %+v", job)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	if strings.Join(files, ",") != "f1,f3" {
		t.Fatalf("destination holds %q, want f1,f3", files)
	}
	for _, name := range []string{"f1", "f3"} {
		body, err := os.ReadFile(root + "/" + name)
		if err != nil || string(body) != "downloaded body "+name[1:] {
			t.Fatalf("%s holds %q (%v)", name, body, err)
		}
	}
	rec.CreditHappy()
}
