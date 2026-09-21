package local

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

type downloadDestinationEntry struct {
	mode fs.FileMode
	body string
	link string
}

func downloadDestinationSnapshot(t *testing.T, root string) map[string]downloadDestinationEntry {
	t.Helper()
	entries := map[string]downloadDestinationEntry{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state := downloadDestinationEntry{mode: info.Mode()}
		switch {
		case info.Mode().IsRegular():
			data, readErr := os.ReadFile(path)
			state.body, err = string(data), readErr
		case info.Mode()&os.ModeSymlink != 0:
			state.link, err = os.Readlink(path)
		case info.IsDir():
		default:
			return fmt.Errorf("unmodelled fixture entry %q: %v", path, info.Mode())
		}
		entries[path] = state
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestDownloadDestinationEntry(t *testing.T) {
	for _, route := range []string{"rclone", "native", "marker"} {
		for _, fault := range []string{"existing_file", "dangling_leaf", "parent_file", "parent_symlink", "contained_symlink", "missing_root", "root_file"} {
			for _, recorded := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/recorded_%v", route, fault, recorded), func(t *testing.T) {
					source, mock := session(t)
					owned := t.TempDir()
					root := filepath.Join(owned, "selected")
					outside := filepath.Join(owned, "outside")
					for _, directory := range []string{root, outside} {
						if err := os.Mkdir(directory, 0o700); err != nil {
							t.Fatal(err)
						}
						writeFile(t, directory, "keep", "unrelated bytes\x00\xff")
					}
					prefix := "scope/"
					if route == "native" {
						prefix = "scope\n/"
					}
					relative := "last"
					switch fault {
					case "existing_file":
						writeFile(t, root, relative, "other writer")
					case "dangling_leaf":
						if err := os.Symlink(filepath.Join(outside, "absent"), filepath.Join(root, relative)); err != nil {
							t.Fatal(err)
						}
					case "parent_file":
						writeFile(t, root, "blocked", "parent file")
						relative = "blocked/last"
					case "parent_symlink", "contained_symlink":
						target := outside
						if fault == "contained_symlink" {
							target = "."
						}
						if err := os.Symlink(target, filepath.Join(root, "redirect")); err != nil {
							t.Fatal(err)
						}
						relative = "redirect/last"
					case "missing_root", "root_file":
						if err := os.Remove(filepath.Join(root, "keep")); err != nil {
							t.Fatal(err)
						}
						if err := os.Remove(root); err != nil {
							t.Fatal(err)
						}
						if fault == "root_file" {
							writeFile(t, owned, "selected", "replacement root file")
						}
					}
					plan := &DownloadPreflight{}
					for _, rel := range []string{"first", relative} {
						item := DownloadItem{Name: prefix + rel, RelPath: filepath.FromSlash(rel), Size: 5, Route: route}
						if route == "marker" {
							item.Name += "/"
							item.Dir, item.Size, item.Route = true, 0, ""
						} else {
							seed(mock, "media", item.Name, "bytes")
						}
						plan.Items = append(plan.Items, item)
					}
					before := downloadDestinationSnapshot(t, owned)
					requests := mock.Requests()
					engine := &downloadRecordingEngine{Engine: &fsEngine{session: source}}
					callbacks := 0
					var recorders []*DownloadRecorder
					if recorded {
						recorders = []*DownloadRecorder{{
							Publication: func(DownloadItem, transfercopy.Observation) error { return nil },
							Admit:       func(DownloadAdmission) error { callbacks++; return nil },
							Execute:     func(DownloadItem, transfercopy.Observation) error { callbacks++; return nil },
							Verify:      func(DownloadItem, transfercopy.Observation) error { callbacks++; return nil },
						}}
					}
					report, err := Download(t.Context(), source, engine, "remote:media", "media", prefix, root, "fixture", plan,
						func(int64, int64, int64) { callbacks++ }, recorders...)
					if err == nil || report != nil || engine.calls != 0 || mock.Requests() != requests || callbacks != 0 {
						t.Errorf("existing hazard admitted part of a direct plan: report=%+v error=%v engine=%d requests=%d callbacks=%d", report, err, engine.calls, mock.Requests()-requests, callbacks)
					}
					if after := downloadDestinationSnapshot(t, owned); !reflect.DeepEqual(before, after) {
						t.Errorf("refused direct plan changed selected or outside entries: before=%+v after=%+v", before, after)
					}
				})
			}
		}
	}
}

func TestDownloadDestinationEmptyEntry(t *testing.T) {
	for _, state := range []string{"directory", "missing", "file"} {
		for _, marker := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/marker_%v", state, marker), func(t *testing.T) {
				owned := t.TempDir()
				root := filepath.Join(owned, "selected")
				if state == "directory" {
					if err := os.Mkdir(root, 0o700); err != nil {
						t.Fatal(err)
					}
				} else if state == "file" {
					writeFile(t, owned, "selected", "root file")
				}
				plan := &DownloadPreflight{}
				if marker {
					plan.Items = []DownloadItem{{Name: "scope/", Dir: true}}
				}
				before := downloadDestinationSnapshot(t, owned)
				calls := 0
				recorder := &DownloadRecorder{
					Publication: func(DownloadItem, transfercopy.Observation) error { return nil },
					Admit:       func(DownloadAdmission) error { calls++; return nil },
					Execute:     func(DownloadItem, transfercopy.Observation) error { calls++; return nil },
					Verify:      func(DownloadItem, transfercopy.Observation) error { calls++; return nil },
				}
				report, err := Download(t.Context(), nil, nil, "", "media", "scope/", root, "fixture", plan, nil, recorder)
				if state != "directory" {
					if err == nil || report != nil || calls != 0 {
						t.Errorf("empty or root-marker plan admitted an unavailable root: %+v calls=%d error=%v", report, calls, err)
					}
				} else {
					want := int64(len(plan.Items))
					if err != nil || report == nil || report.Done != want || report.Bytes != 0 || len(report.Failures) != 0 || calls != int(want)*3 {
						t.Errorf("existing-root control did not complete: %+v calls=%d error=%v", report, calls, err)
					}
				}
				if after := downloadDestinationSnapshot(t, owned); !reflect.DeepEqual(before, after) {
					t.Errorf("empty or root-marker plan changed owned entries: before=%+v after=%+v", before, after)
				}
			})
		}
	}
}
