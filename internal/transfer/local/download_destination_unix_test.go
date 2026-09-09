//go:build !windows

package local

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

func TestDownloadDestinationInspectionError(t *testing.T) {
	for _, boundary := range []string{"root_open", "root_stat", "child"} {
		for _, route := range []string{"rclone", "native", "marker", "empty"} {
			if boundary == "child" && route == "empty" {
				continue
			}
			t.Run(boundary+"/"+route, func(t *testing.T) {
				source, mock := session(t)
				owned := t.TempDir()
				root := filepath.Join(owned, "selected")
				denied := filepath.Join(root, "denied")
				if err := os.MkdirAll(denied, 0o700); err != nil {
					t.Fatal(err)
				}
				writeFile(t, denied, "keep", "retained inaccessible bytes")
				writeFile(t, owned, "outside", "outside bytes")
				prefix := "scope/"
				if route == "native" {
					prefix = "scope\n/"
				}
				plan := &DownloadPreflight{}
				if route != "empty" {
					for _, rel := range []string{"aaa-safe", "denied/target"} {
						item := DownloadItem{Name: prefix + rel, RelPath: filepath.FromSlash(rel), Route: route, Size: 5}
						body := "bytes"
						if route == "marker" {
							item.Name += "/"
							item.Dir, item.Size, item.Route, body = true, 0, "", ""
						}
						seed(mock, "media", item.Name, body)
						plan.Items = append(plan.Items, item)
					}
				}
				before := downloadDestinationSnapshot(t, owned)
				inaccessible, mode := denied, os.FileMode(0o400)
				if boundary == "root_open" {
					inaccessible, mode = root, 0
				} else if boundary == "root_stat" {
					inaccessible = root
				}
				if err := os.Chmod(inaccessible, mode); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(inaccessible, 0o700); err != nil {
						t.Error(err)
					}
				})
				var nativeErr error
				if boundary != "child" {
					opened, err := os.OpenRoot(root)
					nativeErr = err
					if opened != nil {
						if boundary == "root_stat" {
							_, nativeErr = opened.Stat(".")
						}
						if err := opened.Close(); err != nil {
							t.Fatal(err)
						}
					} else if boundary == "root_stat" {
						t.Skipf("native post-open inspection fixture unavailable: %v", err)
					}
				} else {
					_, nativeErr = os.Lstat(filepath.Join(denied, "target"))
				}
				if !errors.Is(nativeErr, os.ErrPermission) {
					t.Skipf("native permission refusal unavailable for uid %d: %v", os.Geteuid(), nativeErr)
				}
				t.Logf("native inspection refusal: uid=%d boundary=%s mode=%#o error=%v", os.Geteuid(), boundary, mode, nativeErr)
				preflight, err := PreflightDownload(t.Context(), source, "media", prefix, root)
				if err != nil || preflight == nil || !preflight.Rejected() {
					t.Errorf("native inspection error was treated as an absent creatable path: %+v error=%v", preflight, err)
				}
				requests, calls := mock.Requests(), 0
				engine := &downloadRecordingEngine{Engine: &fsEngine{session: source}}
				recorder := &DownloadRecorder{
					Admit:   func(DownloadAdmission) error { calls++; return nil },
					Execute: func(DownloadItem, transfercopy.Observation) error { calls++; return nil },
					Verify:  func(DownloadItem, transfercopy.Observation) error { calls++; return nil },
				}
				report, err := Download(t.Context(), source, engine, "remote:media", "media", prefix, root, "fixture", plan, nil, recorder)
				if err == nil || report != nil || calls != 0 || engine.calls != 0 || mock.Requests() != requests {
					t.Errorf("executor continued after native inspection failure: %+v calls=%d engine=%d requests=%d error=%v", report, calls, engine.calls, mock.Requests()-requests, err)
				}
				if err := os.Chmod(inaccessible, 0o700); err != nil {
					t.Fatal(err)
				}
				if after := downloadDestinationSnapshot(t, owned); !reflect.DeepEqual(before, after) {
					t.Errorf("inspection error changed owned entries: before=%+v after=%+v", before, after)
				}
			})
		}
	}
}
