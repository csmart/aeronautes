package local

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func downloadRootEntryTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state := info.Mode().String()
		if info.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state += ":" + string(body)
		} else if !info.IsDir() {
			return fmt.Errorf("unexpected fixture type at %q: %v", path, info.Mode())
		}
		result[path] = state
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDownloadRejectsDestinationBeforeEffects(t *testing.T) {
	for _, route := range []string{"directory", "native", "rclone"} {
		t.Run(route, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "keep", "unselected local bytes")
			captured := root + string(os.PathSeparator) + "."
			before := downloadRootEntryTree(t, root)
			s, mock := session(t)
			seed(mock, "media", "proof.bin", "selected bytes")
			engine := &countedUploadEngine{inner: &fsEngine{s}}
			item := DownloadItem{Name: "proof.bin", RelPath: "proof.bin", Size: 14, Route: route}
			if route == "directory" {
				item.Dir, item.Size = true, 0
			}
			plan := &DownloadPreflight{Items: []DownloadItem{item}, Objects: 1, TotalBytes: item.Size}
			report, err := Download(t.Context(), s, engine, "aeronautes_src:media", "media", "", captured, plan, nil)
			if report != nil || err == nil || err.Error() != downloadRootTestRefusal || engine.calls != 0 {
				t.Errorf("changed destination reached execution: %+v %v engine_calls=%d", report, err, engine.calls)
			}
			if after := downloadRootEntryTree(t, root); !reflect.DeepEqual(before, after) {
				t.Errorf("changed destination touched owned filesystem: before=%v after=%v", before, after)
			}
		})
	}
}
