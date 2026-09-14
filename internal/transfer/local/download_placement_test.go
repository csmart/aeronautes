package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

type downloadPlacementEngine struct {
	Engine
	t     *testing.T
	fault bool
}

func (e downloadPlacementEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	result, err := e.Engine.Copy(ctx, src, dst, names, onEvent)
	if err == nil && e.fault {
		e.t.Cleanup(func() {
			if err := os.Chmod(dst, 0700); err != nil && !os.IsNotExist(err) {
				e.t.Errorf("restore owned stage permissions: %v", err)
			}
		})
		if err := os.Chmod(dst, 0555); err != nil {
			return nil, err
		}
	}
	return result, err
}

type downloadPlacementFile struct {
	Mode  fs.FileMode
	Bytes string
}

func downloadPlacementSnapshot(t *testing.T, root string) map[string]downloadPlacementFile {
	t.Helper()
	result := map[string]downloadPlacementFile{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		item := downloadPlacementFile{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item.Bytes = string(body)
		} else if !info.IsDir() {
			return fmt.Errorf("unexpected owned fixture type: %s", rel)
		}
		result[rel] = item
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDownloadPlacementOutcome(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Fatal("this owned permission probe requires an unprivileged POSIX process")
	}
	for _, fault := range []bool{false, true} {
		t.Run(fmt.Sprintf("fault_%v", fault), func(t *testing.T) {
			source, mock := session(t)
			body := "selected source bytes"
			seed(mock, "media", "scope/proof.bin", body)
			seed(mock, "media", "outside", "unselected source")
			seed(mock, "media", ".aeronautes/stage/other-job/keep", "other remote job")
			witness := uploadWitness(t, mock)
			beforeRemote := uploadSnapshot(t, witness)
			root := t.TempDir()
			writeFile(t, root, "keep", "local sentinel")
			beforeLocal := downloadPlacementSnapshot(t, root)
			preflight, err := PreflightDownload(t.Context(), source, "media", "scope/", root)
			if err != nil || preflight.Rejected() || len(preflight.Items) != 1 || preflight.Items[0].Route != "rclone" {
				t.Fatalf("placement preflight: %+v %v", preflight, err)
			}
			engine := downloadPlacementEngine{Engine: &fsEngine{session: source}, t: t, fault: fault}
			report, err := Download(t.Context(), source, engine, "remote:media", "media", "scope/", root, "permission-probe", preflight, nil)
			if err != nil {
				t.Fatal(err)
			}
			placed := filepath.Join(root, "proof.bin")
			read, err := os.ReadFile(placed)
			if err != nil || string(read) != body {
				t.Fatalf("placed bytes missing: %q %v", read, err)
			}
			beforeLocal["proof.bin"] = downloadPlacementFile{Mode: 0644, Bytes: body}
			if fault {
				stage := filepath.Join(root, ".aeronautes-stage-permission-probe")
				staged := filepath.Join(stage, "proof.bin")
				info, err := os.Stat(staged)
				if err != nil {
					t.Fatal(err)
				}
				placedInfo, err := os.Stat(placed)
				if err != nil || !os.SameFile(info, placedInfo) {
					t.Fatal("fixture did not witness actual linked placement")
				}
				if err := os.Remove(staged); !errors.Is(err, fs.ErrPermission) {
					t.Fatalf("actual unlink was not refused: %v", err)
				}
				beforeLocal[".aeronautes-stage-permission-probe"] = downloadPlacementFile{Mode: fs.ModeDir | 0555}
				beforeLocal[filepath.Join(".aeronautes-stage-permission-probe", "proof.bin")] = downloadPlacementFile{Mode: 0644, Bytes: body}
				if report.Done != 1 || report.Bytes != int64(len(body)) {
					t.Errorf("placed bytes hidden by failed staged unlink: %+v", report)
				}
				// Cleanup now attempts the planned file before the stage
				// directory, so a refused unlink is reported once and the
				// rmdir it blocks once more. Retained says so: the stage
				// really is still there (ADR 0048).
				if report.PostCommitIssues != 1 || len(report.Failures) != 0 || report.StageCleanup == nil ||
					report.StageCleanup.Issues != 2 || !report.StageCleanup.Retained || report.StageCleanup.FirstError == "" {
					t.Errorf("download dropped staging cleanup refusal: %+v", report)
				}
			} else if report.Done != 1 || report.Bytes != int64(len(body)) || len(report.Failures) != 0 || report.PostCommitIssues != 0 || report.StageCleanup == nil || report.StageCleanup.Issues != 0 {
				t.Fatalf("ordinary placement control failed: %+v", report)
			}
			if !reflect.DeepEqual(beforeLocal, downloadPlacementSnapshot(t, root)) {
				t.Error("placement changed unrelated local state")
			}
			if delta := oracle.Diff(beforeRemote, uploadSnapshot(t, witness)); !delta.Empty() {
				t.Fatalf("placement changed remote data: %s", delta)
			}
			encoded, err := json.Marshal(map[string]any{"fault": fault, "placed_bytes": len(read), "report": report})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("download placement outcome: %s", encoded)
		})
	}
}
