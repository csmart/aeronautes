package local

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// Maintained regressions for ADR 0043. The folders a download makes for a
// file are reported from observed creation, and only where the file did not
// download: a file that downloaded has its folders and its own record.
func TestTheFoldersMadeForAFileThatDidNotDownloadAreReported(t *testing.T) {
	for _, written := range []bool{false, true} {
		t.Run(map[bool]string{false: "the engine did not write it", true: "the engine wrote it"}[written], func(t *testing.T) {
			s, mock := session(t)
			seed(mock, "media", "scope/deep/inner/f.bin", "f bytes")
			dest := t.TempDir()
			preflight, err := PreflightDownload(t.Context(), s, "media", "scope/", dest)
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			engine := &scriptedEngine{run: (&fsEngine{s}).Copy}
			if !written {
				// Ended cleanly with no line for the file: not downloaded.
				engine.run = func(context.Context, string, string, []string, func(rclone.Event)) (*rclone.Result, error) {
					return &rclone.Result{}, nil
				}
			}
			report, err := Download(t.Context(), s, engine, "aeronautes_src:media", "media", "scope/", dest, preflight, nil)
			if err != nil || report.Done != map[bool]int64{false: 0, true: 1}[written] {
				t.Fatalf("report = %+v, %v", report, err)
			}
			var want []string
			if !written {
				want = []string{"deep", filepath.Join("deep", "inner")}
			}
			if !slices.Equal(report.CreatedDirectorySamples, want) || report.CreatedDirectories != int64(len(want)) {
				t.Fatalf("created folders reported = %d %q, want %q", report.CreatedDirectories, report.CreatedDirectorySamples, want)
			}
			if info, err := os.Stat(filepath.Join(dest, "deep", "inner")); err != nil || !info.IsDir() {
				t.Fatalf("the folders were not made before the engine ran: %v", err)
			}
		})
	}
}

func TestAncestorCreationIsObservedNotInferred(t *testing.T) {
	root := t.TempDir()
	// "deep" exists already and is the operator's own; only "inner" is
	// this executor's creation.
	if err := os.Mkdir(filepath.Join(root, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	destination, err := OpenDestinationRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()

	dir, created, err := destination.openDirectory(filepath.Join("deep", "inner"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if !slices.Equal(created, []string{filepath.Join("deep", "inner")}) {
		t.Fatalf("a pre-existing directory was counted as created: %v", created)
	}

	// A second walk over the same path creates nothing.
	again, createdAgain, err := destination.openDirectory(filepath.Join("deep", "inner"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if len(createdAgain) != 0 {
		t.Fatalf("an existing directory was reported as created: %v", createdAgain)
	}

	// A refused walk reports only what it created before failing. With
	// creation enabled a refusal can only come from a component that
	// already exists, because an absent one is created; so the prefix is
	// empty here, and a non-empty prefix needs a component to be won by
	// another writer between the walk creating its parent and reaching
	// it. Both are the same code path, and neither may report a
	// directory this executor did not make.
	if err := os.WriteFile(filepath.Join(root, "deep", "blocked"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, refused, err := destination.openDirectory(filepath.Join("deep", "blocked", "below"), true)
	if err == nil {
		t.Fatal("a walk through a regular file was not refused")
	}
	if len(refused) != 0 {
		t.Fatalf("a refused walk reported a directory it did not create: %v", refused)
	}
}

func TestCreatedDirectoriesReachTheReport(t *testing.T) {
	report := &Report{}
	report.createdDirectories([]string{"a", filepath.Join("a", "b")})
	report.createdDirectories(nil)
	if report.CreatedDirectories != 2 || !slices.Equal(report.CreatedDirectorySamples, []string{"a", filepath.Join("a", "b")}) {
		t.Fatalf("observed creation did not reach the report: %+v", report)
	}
	// The count is never truncated by the sample bound.
	many := make([]string, maxFailures+5)
	for i := range many {
		many[i] = filepath.Join("bulk", string(rune('a'+i%26)))
	}
	report.createdDirectories(many)
	if report.CreatedDirectories != int64(2+len(many)) || len(report.CreatedDirectorySamples) != maxFailures {
		t.Fatalf("sample bound truncated the count: %d samples=%d", report.CreatedDirectories, len(report.CreatedDirectorySamples))
	}
}

// The creation decision itself, which the walk cannot reach from a test
// without a filesystem race: an entry won by another writer between the
// inspection and the Mkdir must not count as this executor's.
func TestComponentCreatedIsObserved(t *testing.T) {
	for _, c := range []struct {
		name     string
		absent   bool
		mkdirErr error
		want     bool
	}{
		{"absent and created here", true, nil, true},
		{"absent but won by another writer", true, os.ErrExist, false},
		{"absent but wrapped ErrExist from another writer", true, &os.PathError{Op: "mkdir", Err: os.ErrExist}, false},
		{"already there", false, nil, false},
		{"already there and no mkdir attempted", false, os.ErrExist, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := componentCreated(c.absent, c.mkdirErr); got != c.want {
				t.Fatalf("componentCreated(%v, %v) = %v, want %v", c.absent, c.mkdirErr, got, c.want)
			}
		})
	}
}
