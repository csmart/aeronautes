package local

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDownloadPreservesExistingStage(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			writeDownloadStageFile(t, root, "outside/keep", "unselected bytes")
			stage := filepath.Join(root, ".aeronautes-stage-existing")
			switch kind {
			case "directory":
				writeDownloadStageFile(t, stage, "keep", "other stage bytes")
			case "file":
				writeDownloadStageFile(t, root, filepath.Base(stage), "other file bytes")
			case "symlink":
				if err := os.Symlink(filepath.Join(root, "outside"), stage); err != nil {
					t.Fatal(err)
				}
			}
			source, mock := session(t)
			seed(mock, "media", "proof.bin", "selected bytes")
			plan, err := PreflightDownload(t.Context(), source, "media", "", root)
			if err != nil || plan.Rejected() {
				t.Fatalf("preflight: %+v %v", plan, err)
			}
			engine := &countedUploadEngine{inner: &fsEngine{source}}
			before := snapshotDownloadStage(t, root)
			report, err := Download(t.Context(), source, engine, "remote:media", "media", "", root, "existing", plan, nil)
			if !errors.Is(err, fs.ErrExist) || report == nil || report.Done != 0 || report.Bytes != 0 || report.StageCleanup != nil || engine.calls != 0 {
				t.Errorf("existing stage was admitted: %+v %v calls=%d", report, err, engine.calls)
			}
			if after := snapshotDownloadStage(t, root); !reflect.DeepEqual(before, after) {
				t.Errorf("existing stage changed: before=%v after=%v", before, after)
			}
		})
	}
}

func TestDownloadRequiresPlanBeforeStage(t *testing.T) {
	root := t.TempDir()
	writeDownloadStageFile(t, root, "keep", "unselected bytes")
	before := snapshotDownloadStage(t, root)
	report, err := Download(t.Context(), nil, nil, "", "", "", root, "no-plan", nil, nil)
	if report != nil || err == nil || err.Error() != "download needs a preflight plan" {
		t.Fatalf("missing plan outcome: %+v %v", report, err)
	}
	if after := snapshotDownloadStage(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("missing plan changed local state: %v", after)
	}
}

type stageFileState struct {
	Mode fs.FileMode
	Body string
	Link string
}

func writeDownloadStageFile(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Dir(path), filepath.Base(path), body)
}

func snapshotDownloadStage(t *testing.T, root string) map[string]stageFileState {
	t.Helper()
	result := map[string]stageFileState{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state := stageFileState{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			body, readErr := os.ReadFile(path)
			state.Body, err = string(body), readErr
		} else if info.Mode()&os.ModeSymlink != 0 {
			state.Link, err = os.Readlink(path)
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		result[rel] = state
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDownloadStageCleanupPreservesOtherEntries(t *testing.T) {
	for _, name := range []string{"empty", "missing", "foreign_file", "foreign_directory", "foreign_empty_directory", "foreign_symlink", "partial_file", "selected_file", "selected_symlink", "stage_file"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeDownloadStageFile(t, root, "outside/keep", "unselected bytes")
			stage := filepath.Join(root, "stage")
			if err := os.MkdirAll(filepath.Join(stage, "nested", "leaf"), 0700); err != nil {
				t.Fatal(err)
			}
			// The handles are bound before anything replaces the stage,
			// exactly as the executor binds them when it creates the stage.
			destination := testDestination(t, root)
			held, err := destination.openChildDirectory(destination.root, "stage")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = held.Close() })
			remove := func(relative string) {
				if err := os.Remove(filepath.Join(root, relative)); err != nil {
					t.Fatal(err)
				}
			}
			link := func(relative string) {
				if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(root, relative)); err != nil {
					t.Fatal(err)
				}
			}
			issues := int64(0)
			switch name {
			case "missing":
				remove("stage/nested/leaf")
				remove("stage/nested")
			case "foreign_file":
				writeDownloadStageFile(t, root, "stage/other", "other writer bytes")
				issues = 1
			case "foreign_directory":
				writeDownloadStageFile(t, root, "stage/other/keep", "other writer bytes")
				issues = 1
			case "foreign_empty_directory":
				if err := os.Mkdir(filepath.Join(stage, "other"), 0o700); err != nil {
					t.Fatal(err)
				}
				issues = 1
			case "foreign_symlink":
				link("stage/other")
				issues = 1
			case "partial_file":
				writeDownloadStageFile(t, root, "stage/nested/leaf/proof", "partial bytes")
				issues = 3
			case "selected_file":
				remove("stage/nested/leaf")
				writeDownloadStageFile(t, root, "stage/nested/leaf", "replacement bytes")
				issues = 3
			case "selected_symlink":
				if err := os.Mkdir(filepath.Join(root, "outside", "leaf"), 0o700); err != nil {
					t.Fatal(err)
				}
				remove("stage/nested/leaf")
				remove("stage/nested")
				link("stage/nested")
				issues = 3
			case "stage_file":
				remove("stage/nested/leaf")
				remove("stage/nested")
				remove("stage")
				writeDownloadStageFile(t, root, "stage", "replacement root bytes")
				// The planned directories are absent in the held stage, so only
				// the foreign file at the stage's name is an issue; it is retained.
				issues = 1
			}
			want := snapshotDownloadStage(t, root)
			if issues < 3 {
				delete(want, filepath.Join("stage", "nested", "leaf"))
				delete(want, filepath.Join("stage", "nested"))
				if issues == 0 {
					delete(want, "stage")
				}
			}
			result := cleanupDownloadStage(destination, held, "stage", map[string]DownloadItem{"proof": {RelPath: "nested/leaf/proof"}})
			if result.Issues != issues || (result.FirstError != "") != (issues > 0) {
				t.Errorf("cleanup outcome differs: %+v; want %d issues", result, issues)
			}
			if got := snapshotDownloadStage(t, root); !reflect.DeepEqual(got, want) {
				t.Errorf("cleanup changed unrelated entries: got=%v want=%v", got, want)
			}
		})
	}
}
