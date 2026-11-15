package systemtest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// A large object is deleted with its segments however it was made (ADR 0120
// proof at the service layer). Three makers, each writing real segments: this
// application's upload by the static choice and by the dynamic choice, with
// the engine writing the segments as it does for an operator, and rclone on
// its own, which is how an operator's existing data was often made. The
// static one and the one rclone made are each deleted by the route an
// object's own dialog takes, and only that object's segments go; the dynamic
// one then goes with its container by the bulk option. An object in the
// segments container that no manifest names stays throughout.
func largeObjectsGoWithTheirSegmentsHoweverMade(t *testing.T, h *Harness) {
	ctx := t.Context()
	c := h.Container(h.Primary, "makers")
	sidecar := c + "_segments"
	h.rawPut(h.Primary, "/"+sidecar, nil, nil)
	t.Cleanup(func() { h.purge(h.Primary, sidecar) })
	h.Seed(h.Primary, sidecar, "unrelated", []byte("no manifest names this"), textType)
	if _, err := h.Browse.Connect(ctx, h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	listed := func(container string) []string {
		t.Helper()
		names, err := h.Primary.Oracle.ListObjects(container)
		if err != nil {
			t.Fatalf("list %s: %v", container, err)
		}
		slices.Sort(names)
		return names
	}

	// Each maker is handed a local file a little over three chunks long, and
	// the segments it wrote are the names the segments container gained.
	type madeObject struct {
		name, class string
		segments    []string
	}
	makeObject := func(name, wantClass string, maker func(path string)) madeObject {
		t.Helper()
		path := filepath.Join(t.TempDir(), filepath.Base(name))
		if err := os.WriteFile(path, bytes.Repeat([]byte("abcdefg"), 450_000), 0o600); err != nil {
			t.Fatal(err)
		}
		before := listed(sidecar)
		maker(path)
		object := madeObject{name: name}
		for _, segment := range listed(sidecar) {
			if !slices.Contains(before, segment) {
				object.segments = append(object.segments, segment)
			}
		}
		detail, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, name)
		if err != nil {
			t.Fatalf("%s after it was made: %v", name, err)
		}
		if object.class = detail.Class; object.class != wantClass || len(object.segments) < 2 {
			t.Fatalf("%s is a %s object with segments %v, want a %s large object in several segments", name, object.class, object.segments, wantClass)
		}
		return object
	}
	upload := func(choice string) func(string) {
		return func(path string) {
			t.Helper()
			app.WireFilePicker(h.Transfer, listingFilePicker{files: []string{path}, dir: filepath.Dir(path)})
			scope := app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: c, Prefix: choice + "/"}
			picked, err := h.Transfer.PickUploadFiles(ctx, scope)
			if err != nil || picked == nil {
				t.Fatalf("pick: %+v %v", picked, err)
			}
			rec := h.StartUpload(ctx, app.UploadRequestDTO{BookmarkID: scope.BookmarkID, Container: c, Prefix: scope.Prefix,
				SelectionToken: picked.Token, Settings: app.TransferSettingsDTO{ChunkSize: "1Mi", LargeObjects: choice}})
			if rec.Err() != nil {
				t.Fatal(rec.Err())
			}
			if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
				t.Fatalf("upload by the %s choice = %s: %s", choice, job.State, job.Error)
			}
		}
	}
	static := makeObject("slo/a.bin", "slo", upload("slo"))
	dynamic := makeObject("dlo/b.bin", "dlo", upload("dlo"))
	byRclone := makeObject("rclone/c.bin", "dlo", func(path string) { rcloneCopyTo(t, h.Primary, path, c+"/rclone/c.bin") })

	remaining := slices.Concat([]string{"unrelated"}, static.segments, dynamic.segments, byRclone.segments)
	for _, object := range []madeObject{static, byRclone} {
		reviewed := h.ReviewSegmentMembership(ctx, c, object.name)
		rec := h.DeleteManifestWithSegments(ctx, app.SegmentsDeleteRequestDTO{
			BookmarkID: h.Primary.BookmarkID, Container: c, Name: object.name, Force: true, Confirm: object.name, Reviewed: reviewed})
		if rec.Err() != nil {
			t.Fatalf("%s with its segments: %v", object.name, rec.Err())
		}
		if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
			t.Fatalf("%s with its segments = %s: %s", object.name, job.State, job.Error)
		}
		remaining = slices.DeleteFunc(remaining, func(name string) bool { return slices.Contains(object.segments, name) })
		slices.Sort(remaining)
		if got := listed(sidecar); !slices.Equal(got, remaining) {
			t.Fatalf("after %s went with its segments the segments container holds %v, want %v", object.name, got, remaining)
		}
		if slices.Contains(listed(c), object.name) {
			t.Fatalf("%s is still listed", object.name)
		}
	}

	rec := h.StartDelete(ctx, app.DeleteRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: c, Mode: app.ModeDeleteContainer,
		Segments: true, Confirm: c + " and segments"})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
		t.Fatalf("the container with its segments = %s: %s", job.State, job.Error)
	}
	if _, standing := h.SnapshotNamespace(h.Primary).Containers[c]; standing {
		t.Fatalf("container %s still stands", c)
	}
	if got := listed(sidecar); !slices.Equal(got, []string{"unrelated"}) {
		t.Fatalf("after the container went with its segments the segments container holds %v, want only the object no manifest named", got)
	}
}

// rcloneCopyTo uploads one local file with the pinned rclone alone, as an
// operator does from a terminal, in chunks small enough to segment it. The
// child is given the account's storage address and token and nothing of the
// environment, as the application's own engine runs are, and it ends with
// the test, so a run that is interrupted leaves no child holding that token.
func rcloneCopyTo(t *testing.T, account *Account, path, destination string) {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, "rclone.conf")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "rclone", "--config", config, "copyto", path, "made:"+destination,
		"--swift-chunk-size", "1Mi", "--swift-use-segments-container=true")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home,
		"RCLONE_CONFIG_MADE_TYPE=swift", "RCLONE_CONFIG_MADE_ENV_AUTH=false",
		"RCLONE_CONFIG_MADE_STORAGE_URL=" + account.storageURL, "RCLONE_CONFIG_MADE_AUTH_TOKEN=" + account.token}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rclone copyto: %v\n%s", err, out)
	}
}

func TestLargeObjectsGoWithTheirSegmentsHoweverMadeMock(t *testing.T) {
	largeObjectsGoWithTheirSegmentsHoweverMade(t, findMockHarness(t))
}

func TestLargeObjectsGoWithTheirSegmentsHoweverMadeSwift(t *testing.T) {
	largeObjectsGoWithTheirSegmentsHoweverMade(t, New(t))
}
