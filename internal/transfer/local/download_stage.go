package local

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// StageCleanupResult describes the group, without inventing an object
// identity for a refusal to remove its remaining directories.
type StageCleanupResult struct {
	Issues     int64
	FirstError string
}

func (r *StageCleanupResult) failed(err error) {
	r.Issues++
	if r.FirstError == "" {
		r.FirstError = err.Error()
	}
}

func cleanupDownloadStage(stage string, items map[string]DownloadItem) *StageCleanupResult {
	result := &StageCleanupResult{}
	dirs := map[string]bool{".": true}
	for _, item := range items {
		dir := filepath.Dir(item.RelPath)
		if filepath.IsAbs(dir) || filepath.VolumeName(dir) != "" || filepath.Clean(dir) != dir || dir == ".." || strings.HasPrefix(dir, ".."+string(filepath.Separator)) {
			result.failed(fmt.Errorf("selected stage directory is outside the relative cleanup scope"))
			continue
		}
		for dir != "." {
			dirs[dir] = true
			dir = filepath.Dir(dir)
		}
	}
	ordered := make([]string, 0, len(dirs))
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i] == "." {
			return false
		}
		if ordered[j] == "." {
			return true
		}
		if len(ordered[i]) != len(ordered[j]) {
			return len(ordered[i]) > len(ordered[j])
		}
		return ordered[i] < ordered[j]
	})
	for _, dir := range ordered {
		if err := removeEmptyStageDirectory(stage, dir); err != nil {
			result.failed(err)
		}
	}
	return result
}

func removeEmptyStageDirectory(stage, relative string) error {
	path := stage
	parts := []string{"."}
	if relative != "." {
		parts = append(parts, strings.Split(relative, string(filepath.Separator))...)
	}
	for _, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("stage cleanup retained a non-directory at %q", path)
		}
	}
	// os.Remove may unlink a file that replaces this directory after
	// Lstat. Rmdir only removes an empty directory, even in that race.
	if err := syscall.Rmdir(path); err != nil {
		return &os.PathError{Op: "rmdir", Path: path, Err: err}
	}
	return nil
}
