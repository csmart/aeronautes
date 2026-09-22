package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Maintained regressions for ADR 0048. Cleanup removes the files this job
// planned into its own stage, and nothing else. What it refuses is refused
// for a reason it can state, and Retained describes the disk rather than
// the path that reached it.
func TestDownloadStageRemovesOnlyPlannedFiles(t *testing.T) {
	// The item this job planned; every case below stages something at that
	// name, or at a name the job never planned, and asserts what survives.
	planned := map[string]DownloadItem{"proof": {RelPath: filepath.Join("nested", "proof")}}

	for _, c := range []struct {
		name string
		// occupy places an entry at the planned name, or elsewhere.
		occupy func(t *testing.T, root, stage string)
		// survives is the path, relative to root, that must still exist.
		survives string
		// issues counts the cleanup refusals expected.
		issues int64
		// retained is what the flag must say about the disk afterwards.
		retained bool
	}{
		{
			name: "planned file is removed and the stage goes with it",
			occupy: func(t *testing.T, root, stage string) {
				writeDownloadStageFile(t, root, filepath.Join("stage", "nested", "proof"), "staged bytes")
			},
			retained: false,
		},
		{
			name: "a link at the planned name is retained, not followed",
			occupy: func(t *testing.T, root, stage string) {
				// The link points outside the stage. Following it would
				// unlink the operator's own file; refusing keeps both.
				if err := os.Symlink(filepath.Join(root, "outside", "keep"), filepath.Join(stage, "nested", "proof")); err != nil {
					t.Fatal(err)
				}
			},
			survives: filepath.Join("outside", "keep"),
			// The refusal, then the two directories it keeps non-empty.
			issues:   3,
			retained: true,
		},
		{
			name: "a directory at the planned name is retained",
			occupy: func(t *testing.T, root, stage string) {
				if err := os.Mkdir(filepath.Join(stage, "nested", "proof"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			issues:   3,
			retained: true,
		},
		{
			name: "a name this job never planned is never a candidate",
			occupy: func(t *testing.T, root, stage string) {
				writeDownloadStageFile(t, root, filepath.Join("stage", "unplanned"), "another writer's bytes")
			},
			survives: filepath.Join("stage", "unplanned"),
			issues:   1,
			retained: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeDownloadStageFile(t, root, filepath.Join("outside", "keep"), "unselected bytes")
			stage := filepath.Join(root, "stage")
			if err := os.MkdirAll(filepath.Join(stage, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
			destination := testDestination(t, root)
			held, err := destination.openChildDirectory(destination.root, "stage")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = held.Close() })
			c.occupy(t, root, stage)

			result := cleanupDownloadStage(destination, held, "stage", planned, nil)

			if result.Issues != c.issues || (result.FirstError != "") != (c.issues > 0) {
				t.Errorf("cleanup issues %+v, want %d", result, c.issues)
			}
			if result.Retained != c.retained {
				t.Errorf("Retained=%v, want %v: %+v", result.Retained, c.retained, result)
			}
			// Retained must agree with the disk, not merely with the code
			// path: the stage is present exactly when Retained says so.
			_, stageErr := os.Lstat(stage)
			if (stageErr == nil) != c.retained {
				t.Errorf("Retained=%v but stage present=%v", c.retained, stageErr == nil)
			}
			if c.survives != "" {
				if _, err := os.Lstat(filepath.Join(root, c.survives)); err != nil {
					t.Errorf("cleanup removed %q, which it never planned: %v", c.survives, err)
				}
			}
			// The operator's own file outside the stage is never touched.
			if data, err := os.ReadFile(filepath.Join(root, "outside", "keep")); err != nil || string(data) != "unselected bytes" {
				t.Errorf("cleanup reached outside the stage: %q %v", data, err)
			}
		})
	}
}

// Retained is taken from the stage, not from the issue count, so a refusal
// that does not keep the stage alive does not claim residue that is not
// there (ADR 0048).
func TestDownloadStageRetainedIsNotTheIssueCount(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	destination := testDestination(t, root)
	held, err := destination.openChildDirectory(destination.root, "stage")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })

	// An item whose name cannot resolve beneath the stage is refused and
	// counted, but it leaves nothing behind, so the stage still goes.
	outside := map[string]DownloadItem{"escape": {RelPath: filepath.Join("..", "escape")}}
	result := cleanupDownloadStage(destination, held, "stage", outside, nil)

	if result.Issues == 0 {
		t.Fatalf("an out-of-scope planned name was not refused: %+v", result)
	}
	if result.Retained {
		t.Errorf("Retained followed the issue count instead of the disk: %+v", result)
	}
	if _, err := os.Lstat(stage); err == nil {
		t.Error("the stage survived with Retained false")
	}
}

// A planned name that cannot resolve beneath the stage is refused by this
// code before any filesystem call, so it is never a removal candidate by
// construction rather than because a lower layer happened to stop it. The
// refusal names the cleanup scope; a path-escape error means the attempt
// was made and something else caught it (ADR 0048).
func TestDownloadStageRefusesAnEscapingNameItself(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	destination := testDestination(t, root)
	held, err := destination.openChildDirectory(destination.root, "stage")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })

	// filepath.Dir("..") is ".", which is in scope, so only the check on
	// the name itself can refuse this one.
	result := cleanupDownloadStage(destination, held, "stage",
		map[string]DownloadItem{"escape": {RelPath: ".."}}, nil)

	if result.Issues != 1 {
		t.Fatalf("escaping planned name was not refused exactly once: %+v", result)
	}
	if !strings.Contains(result.FirstError, "outside the relative cleanup scope") {
		t.Errorf("refusal did not come from the scope check: %q", result.FirstError)
	}
}

// The held stage handle follows the directory's identity rather than its
// name, which is what stops a rename redirecting a removal. The same
// property would let cleanup reach into a stage another actor has moved
// away, which ADR 0035 decision 7 retains and reports instead of touching,
// so displacement is established before any file is removed (ADR 0048).
func TestDownloadStageLeavesAMovedStageAlone(t *testing.T) {
	for _, staged := range []bool{true, false} {
		name := "staged file still present"
		if !staged {
			// The case that matters: with the planned tree already empty
			// there is no file to make an rmdir fail, so a removal issued
			// through the held handle would succeed inside the moved
			// location and go unnoticed.
			name = "planned tree already empty"
		}
		t.Run(name, func(t *testing.T) { movedStageCase(t, staged) })
	}
}

func movedStageCase(t *testing.T, staged bool) {
	t.Helper()
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.MkdirAll(filepath.Join(stage, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if staged {
		writeDownloadStageFile(t, root, filepath.Join("stage", "nested", "proof"), "staged bytes")
	}
	destination := testDestination(t, root)
	held, err := destination.openChildDirectory(destination.root, "stage")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })

	// Another actor moves the stage after the handle was bound.
	moved := filepath.Join(root, "moved-stage")
	if err := os.Rename(stage, moved); err != nil {
		t.Fatal(err)
	}

	result := cleanupDownloadStage(destination, held, "stage",
		map[string]DownloadItem{"proof": {RelPath: filepath.Join("nested", "proof")}}, nil)

	if !result.Retained || result.Issues == 0 {
		t.Errorf("a moved stage was not retained and reported: %+v", result)
	}
	// Nothing under the moved stage is touched, whether or not a staged
	// file is there to make a removal fail.
	if _, err := os.Lstat(filepath.Join(moved, "nested")); err != nil {
		t.Errorf("cleanup removed a directory inside a stage it no longer owns: %v", err)
	}
	if staged {
		data, err := os.ReadFile(filepath.Join(moved, "nested", "proof"))
		if err != nil || string(data) != "staged bytes" {
			t.Errorf("cleanup reached into a stage it no longer owns: %q %v", data, err)
		}
	}
}
