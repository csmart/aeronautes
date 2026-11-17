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
// Swift, a copy of two selected objects and a selected folder out of a
// folder of five objects and two folders writes exactly those two and
// everything in that folder, with the source's bytes and the folder's name
// kept, and writes nothing else anywhere on the account (ADR 0108 decisions
// 3 and 7).
func TestCopySelectedMembersRealSwift(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	src := h.Container(h.Primary, "selsrc")
	dst := h.Container(h.Primary, "seldst")
	for i := range 5 {
		h.Seed(h.Primary, src, fmt.Sprintf("files/f%d", i), []byte(fmt.Sprintf("selected body %d", i)), nil)
	}
	for _, name := range []string{"files/docs/x", "files/docs/deep/y", "files/docsier/z"} {
		h.Seed(h.Primary, src, name, []byte("folder body "+name), nil)
	}
	before := h.SnapshotNamespace(h.Primary)

	rec := h.StartCopy(ctx, app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "files/",
		DestBookmarkID: h.Primary.BookmarkID, DestContainer: dst,
		Members: []string{"files/f1", "files/f3", "files/docs/"},
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	job := h.AwaitJob(rec.JobID())
	if job.State != "complete" || job.Counters.ObjectsCopied != 4 || job.Counters.ObjectsVerified != 4 || job.SelectedMembers != 3 {
		t.Fatalf("selected copy = %+v", job)
	}

	after := h.SnapshotNamespace(h.Primary)
	from, to := before.Containers[src].Objects, after.Containers[dst].Objects
	for _, name := range []string{"f1", "f3", "docs/x", "docs/deep/y"} {
		if to[name].Digest != from["files/"+name].Digest || to[name].Size != from["files/"+name].Size {
			t.Fatalf("%s: digest %s (%d), want the source's %s (%d)", name, to[name].Digest, to[name].Size, from["files/"+name].Digest, from["files/"+name].Size)
		}
	}
	h.AssertNoCollateral(before, after, dst+"/f1", dst+"/f3", dst+"/docs/x", dst+"/docs/deep/y")
	rec.CreditHappy()
}

// TestDownloadSelectedMembersRealSwift: a download of two selected objects
// and a selected folder places exactly those two files and everything in the
// folder, under the folder's name, through the engine, and the job says it
// acted on three selected (ADR 0108 decisions 3 and 7).
func TestDownloadSelectedMembersRealSwift(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	src := h.Container(h.Primary, "seldl")
	for i := range 5 {
		h.Seed(h.Primary, src, fmt.Sprintf("scope/f%d", i), []byte(fmt.Sprintf("downloaded body %d", i)), nil)
	}
	h.Seed(h.Primary, src, "scope/docs/x", []byte("folder body x"), nil)
	h.Seed(h.Primary, src, "scope/docsier/z", []byte("folder body z"), nil)
	root := t.TempDir()
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: root})
	picked, err := h.Transfer.PickDownloadDir(ctx, app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: src, Prefix: "scope/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	rec := h.StartDownload(ctx, app.DownloadRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: src, Prefix: "scope/", DestDirToken: picked.Token,
		Members: []string{"scope/f1", "scope/f3", "scope/docs/"},
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	job := h.AwaitJob(rec.JobID())
	if job.State != "complete" || job.Counters.ObjectsCopied != 3 || job.Counters.ObjectsVerified != 3 || job.SelectedMembers != 3 {
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
	if strings.Join(files, ",") != "docs,f1,f3" {
		t.Fatalf("destination holds %q, want docs,f1,f3", files)
	}
	if body, err := os.ReadFile(root + "/docs/x"); err != nil || string(body) != "folder body x" {
		t.Fatalf("docs/x holds %q (%v)", body, err)
	}
	for _, name := range []string{"f1", "f3"} {
		body, err := os.ReadFile(root + "/" + name)
		if err != nil || string(body) != "downloaded body "+name[1:] {
			t.Fatalf("%s holds %q (%v)", name, body, err)
		}
	}
	rec.CreditHappy()
}
