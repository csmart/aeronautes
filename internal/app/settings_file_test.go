package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settingsFile is a settings path in a directory that does not exist yet, so
// the first save also proves the directory is created.
func settingsFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "aeronautes", "settings.json")
}

// A save writes one private file in a private directory, which a fresh
// service reads back; keys the file leaves out keep their defaults, so
// reclaiming stays on, and keys it does not know are ignored and not written
// back (ADR 0109 decision 1).
func TestSettingsLiveInTheirOwnFile(t *testing.T) {
	ctx := context.Background()
	path := settingsFile(t)
	service := NewSettingsService(path, newJobRunner(nil, nil))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a service with no save made a settings file: %v", err)
	}
	saved := defaults()
	saved.MaxActiveJobs, saved.RailWidthPx, saved.GPUAcceleration = 3, 200, false
	if _, err := service.Set(ctx, saved); err != nil {
		t.Fatal(err)
	}
	if read, err := NewSettingsService(path, newJobRunner(nil, nil)).Get(ctx); err != nil || read != saved {
		t.Fatalf("a fresh service read %+v (%v), want %+v", read, err, saved)
	}
	for name, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		if info, err := os.Stat(name); err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s has mode %v (%v), want %v", name, info.Mode().Perm(), err, want)
		}
	}

	if err := os.WriteFile(path, []byte(`{"maxActiveJobs": 4, "fromANewerVersion": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := service.Get(ctx)
	if err != nil || read.FileProblem != "" || read.MaxActiveJobs != 4 || !read.ReclaimReplacedFragments || !read.GPUAcceleration || read.DefaultProfile != "auto" {
		t.Fatalf("a file with keys left out read as %+v (%v); want them at their defaults", read, err)
	}
	if _, err := service.Set(ctx, read); err != nil {
		t.Fatal(err)
	}
	var written map[string]any
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	if _, kept := written["fromANewerVersion"]; kept {
		t.Fatal("an unknown key was written back")
	}
	if _, kept := written["fileProblem"]; kept {
		t.Fatal("the read-only report was written to the file")
	}
}

// A file that cannot be used whole is not used at all: the defaults are in
// force, the report says why, and the file is left as written until a save,
// which keeps it aside as settings.json.unreadable; a later good read clears
// the report (ADR 0109 decision 1).
func TestAnUnreadableSettingsFileIsKeptAndReported(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		body, reason string
	}{
		"not JSON":     {`{"maxActiveJobs": 3,`, "unexpected end of JSON input"},
		"a wrong type": {`{"maxActiveJobs": "three"}`, "cannot unmarshal string"},
		"out of range": {`{"maxActiveJobs": 3, "downloadObjectCeiling": 10}`, "download object ceiling 10 out of range"},
	} {
		t.Run(name, func(t *testing.T) {
			path := settingsFile(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+".unreadable", []byte("an earlier one"), 0o600); err != nil {
				t.Fatal(err)
			}
			service := NewSettingsService(path, newJobRunner(nil, nil))
			read, err := service.Get(ctx)
			want := defaults()
			want.FileProblem = read.FileProblem
			if err != nil || read != want || !strings.Contains(read.FileProblem, tc.reason) || !strings.Contains(read.FileProblem, path) {
				t.Fatalf("read %+v (%v); want the defaults and a report naming the file and %q", read, err, tc.reason)
			}
			if kept, _ := os.ReadFile(path); string(kept) != tc.body {
				t.Fatalf("a read changed the file to %q", kept)
			}

			// Saved as the Settings dialog saves it: the document it read,
			// report and all, with one change.
			saved := read
			saved.MaxActiveJobs = 1
			if _, err := service.Set(ctx, saved); err != nil {
				t.Fatal(err)
			}
			if aside, _ := os.ReadFile(path + ".unreadable"); string(aside) != tc.body {
				t.Fatalf("the save kept %q aside, want the unreadable file", aside)
			}
			if written, _ := os.ReadFile(path); strings.Contains(string(written), "fileProblem") {
				t.Fatalf("the report was written into the file: %s", written)
			}
			if read, err := service.Get(ctx); err != nil || read.FileProblem != "" || read.MaxActiveJobs != 1 {
				t.Fatalf("after the save the settings read %+v (%v)", read, err)
			}
		})
	}
}

// A save that fails before its rename leaves the old file whole, and the
// temporary file goes with it.
func TestAFailedSettingsSaveLeavesTheOldFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory, so the save cannot be made to fail this way")
	}
	ctx := context.Background()
	path := settingsFile(t)
	service := NewSettingsService(path, newJobRunner(nil, nil))
	if _, err := service.Set(ctx, defaults()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	changed := defaults()
	changed.MaxActiveJobs = 4
	if _, err := service.Set(ctx, changed); err == nil {
		t.Fatal("a save into a read-only directory succeeded")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatalf("a failed save changed the file to %q", after)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("a failed save left %d entries in the directory", len(entries))
	}
}
