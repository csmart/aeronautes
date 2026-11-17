package systemtest

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
)

// ADR 0122 decision 12, with the real engine: after the confirmation an
// upload or a download replaces what differs and leaves what is the same, as
// rclone copy does used alone. Each case runs a plain transfer first, so the
// two sides start the same, then changes one file's size, one file's time
// alone and each large object's time alone, and runs the confirmed transfer.
func TestReplaceWhatDiffersMock(t *testing.T)      { replaceWhatDiffersCases(t, findMockHarness) }
func TestReplaceWhatDiffersRealSwift(t *testing.T) { replaceWhatDiffersCases(t, New) }

// longAgo is a modification time no source file in these cases has.
var longAgo = time.Date(2004, 11, 9, 11, 33, 20, 0, time.UTC)

func replaceWhatDiffersCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Helper()
	t.Run("a confirmed download", func(t *testing.T) {
		h := makeHarness(t)
		source := h.Container(h.Primary, "replace-download")
		segments := h.Container(h.Primary, "replace-download-segments")
		for name, body := range map[string]string{"same.txt": "same bytes", "resized.txt": "resized bytes", "touched.txt": "touched bytes"} {
			h.Seed(h.Primary, source, "scope/"+name, []byte(body), nil)
		}
		h.Seed(h.Primary, segments, "dlo/0", []byte("dynamic "), nil)
		h.Seed(h.Primary, segments, "dlo/1", []byte("segments"), nil)
		h.Seed(h.Primary, source, "scope/dlo.bin", nil, map[string]string{"X-Object-Manifest": segments + "/dlo/"})
		h.Seed(h.Primary, segments, "slo/0", []byte("static "), nil)
		h.Seed(h.Primary, segments, "slo/1", []byte("segments"), nil)
		h.SeedSLO(h.Primary, source, "scope/slo.bin", fmt.Sprintf(`[{"path":"/%s/slo/0","etag":"%x","size_bytes":7},{"path":"/%s/slo/1","etag":"%x","size_bytes":8}]`,
			segments, md5.Sum([]byte("static ")), segments, md5.Sum([]byte("segments"))))
		if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
			t.Fatal(err)
		}
		dest := t.TempDir()
		download := func(acknowledge bool) *app.JobDTO {
			t.Helper()
			app.WireFilePicker(h.Transfer, listingFilePicker{dir: dest})
			picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: "scope/"})
			if err != nil || picked == nil {
				t.Fatalf("pick: %+v %v", picked, err)
			}
			id, err := h.Transfer.StartDownload(t.Context(), app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source,
				Prefix: "scope/", DestDirToken: picked.Token, AcknowledgeOverwrite: acknowledge})
			if err != nil {
				t.Fatalf("start (acknowledged %t): %v", acknowledge, err)
			}
			return h.AwaitJob(id)
		}
		if first := download(false); first.State != "complete" || first.Counters.ObjectsCopied != 5 {
			t.Fatalf("the first download = %s %+v", first.State, first.Counters)
		}
		sourceTime := statLocal(t, dest, "touched.txt").ModTime()
		writeLocal(t, dest, "resized.txt", "resized bytes, and longer")
		for _, name := range []string{"touched.txt", "dlo.bin", "slo.bin"} {
			if err := os.Chtimes(filepath.Join(dest, name), longAgo, longAgo); err != nil {
				t.Fatal(err)
			}
		}
		before := map[string]os.FileInfo{}
		for _, name := range []string{"same.txt", "resized.txt", "touched.txt", "dlo.bin", "slo.bin"} {
			before[name] = statLocal(t, dest, name)
		}
		confirmer := &serviceConfirmer{yes: true}
		app.WireOverwriteConfirmer(h.Transfer, confirmer)
		job := download(true)
		if job.State != "complete" || job.Counters.ObjectsCopied != 3 || job.Counters.ObjectsSame != 2 ||
			job.Counters.Overwrites != 3 || job.Counters.ObjectsVerified != 5 {
			t.Fatalf("the confirmed download = %s %+v; want three replaced and two already the same", job.State, job.Counters)
		}
		if asked := confirmer.questions(); len(asked) != 1 || len(asked[0].Names) != 5 || asked[0].Folder != dest {
			t.Fatalf("asked %+v, want one question naming the folder and all five files", asked)
		}
		// A replaced file is a new file at the name, renamed over the old
		// one; a file left as it was is the same file.
		for name, replaced := range map[string]bool{"same.txt": false, "touched.txt": false, "resized.txt": true, "dlo.bin": true, "slo.bin": true} {
			if got := !os.SameFile(before[name], statLocal(t, dest, name)); got != replaced {
				t.Errorf("%s replaced = %t, want %t", name, got, replaced)
			}
		}
		for name, want := range map[string]string{"resized.txt": "resized bytes", "dlo.bin": "dynamic segments", "slo.bin": "static segments", "touched.txt": "touched bytes"} {
			if got := readLocalFile(t, dest, name); got != want {
				t.Errorf("%s holds %q, want %q", name, got, want)
			}
		}
		if got := statLocal(t, dest, "touched.txt").ModTime(); !got.Equal(sourceTime) {
			t.Errorf("touched.txt's time is %s, want the source's %s", got, sourceTime)
		}
	})

	t.Run("a confirmed upload", func(t *testing.T) {
		h := makeHarness(t)
		dst := h.Container(h.Primary, "replace-upload")
		// The engine puts a large file's segments in the container's sidecar.
		t.Cleanup(func() { h.purge(h.Primary, dst+"_segments") })
		if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		big := strings.Repeat("a large file in segments ", 64*1024) // 1.6 MiB, two segments of 1 MiB
		files := map[string]string{"same.txt": "same bytes", "resized.txt": "resized bytes", "touched.txt": "touched bytes", "big.bin": big}
		var paths []string
		for name, body := range files {
			writeLocal(t, dir, name, body)
			paths = append(paths, filepath.Join(dir, name))
		}
		upload := func(acknowledge bool) *app.JobDTO {
			t.Helper()
			app.WireFilePicker(h.Transfer, listingFilePicker{files: paths, dir: dir})
			picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: dst, Prefix: "up/"})
			if err != nil || picked == nil {
				t.Fatalf("pick: %+v %v", picked, err)
			}
			id, err := h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: dst, Prefix: "up/",
				SelectionToken: picked.Token, AcknowledgeOverwrite: acknowledge, Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"}})
			if err != nil {
				t.Fatalf("start (acknowledged %t): %v", acknowledge, err)
			}
			return h.AwaitJob(id)
		}
		if first := upload(false); first.State != "complete" || first.Counters.ObjectsCopied != 4 {
			t.Fatalf("the first upload = %s %+v", first.State, first.Counters)
		}
		writeLocal(t, dir, "resized.txt", "resized bytes, and longer")
		for _, name := range []string{"touched.txt", "big.bin"} {
			if err := os.Chtimes(filepath.Join(dir, name), longAgo, longAgo); err != nil {
				t.Fatal(err)
			}
		}
		before := h.Snapshot(h.Primary, dst).Containers[dst].Objects
		app.WireOverwriteConfirmer(h.Transfer, &serviceConfirmer{yes: true})
		job := upload(true)
		if job.State != "complete" || job.Counters.ObjectsCopied != 2 || job.Counters.ObjectsSame != 2 ||
			job.Counters.Overwrites != 2 || job.Counters.ObjectsVerified != 4 {
			t.Fatalf("the confirmed upload = %s %+v; want two replaced and two already the same", job.State, job.Counters)
		}
		after := h.Snapshot(h.Primary, dst).Containers[dst].Objects
		if !reflect.DeepEqual(before["up/same.txt"], after["up/same.txt"]) {
			t.Errorf("same.txt changed: %+v -> %+v", before["up/same.txt"], after["up/same.txt"])
		}
		if b, a := before["up/touched.txt"], after["up/touched.txt"]; a.Digest != b.Digest || a.Meta["Mtime"] == b.Meta["Mtime"] {
			t.Errorf("touched.txt should keep its bytes and take the file's time: %+v -> %+v", b, a)
		}
		if a := after["up/resized.txt"]; a.Size != int64(len("resized bytes, and longer")) {
			t.Errorf("resized.txt was not replaced: %+v", a)
		}
		// A large object has no MD5 to compare, so one whose time alone
		// differs is sent again, whole, on new segments.
		if b, a := before["up/big.bin"], after["up/big.bin"]; a.Digest != b.Digest || a.Size != int64(len(big)) || a.Class == "plain" ||
			len(a.Segments) == 0 || len(b.Segments) == 0 || a.Segments[0].Name == b.Segments[0].Name {
			t.Errorf("big.bin should be replaced whole on new segments: %+v -> %+v", b, a)
		}
	})
}

func statLocal(t *testing.T, dir, name string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func writeLocal(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readLocalFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
