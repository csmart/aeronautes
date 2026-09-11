package local

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// StageCleanupResult describes the group, without inventing an object
// identity for a refusal to remove its remaining directories.
type StageCleanupResult struct {
	Issues     int64
	FirstError string
	Retained   bool
}

func (r *StageCleanupResult) failed(err error) {
	r.Issues++
	if r.FirstError == "" {
		r.FirstError = err.Error()
	}
}

// cleanupDownloadStage removes the directories this executor planned,
// deepest first, through the held stage handle, and finally the stage
// itself by name beneath the held root once the name still denotes it.
func cleanupDownloadStage(destination, stage *DestinationRoot, stageName string, items map[string]DownloadItem) *StageCleanupResult {
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
		if err := removeEmptyStageDirectory(destination, stage, stageName, dir); err != nil {
			result.failed(err)
		}
	}
	return result
}

// removeEmptyStageDirectory removes one planned directory only through
// handles bound to inspected directories, and only with a primitive that
// removes an empty directory, so a file placed at a directory's name is
// never unlinked. An absent directory needs no removal; anything else
// that is not an ordinary directory is retained and reported.
func removeEmptyStageDirectory(destination, stage *DestinationRoot, stageName, relative string) error {
	if relative == "." {
		displaced, err := stage.displacedAt(destination.root, stageName)
		if err != nil {
			return err
		}
		if displaced {
			return fmt.Errorf("stage directory %q no longer sits at its name under the selected directory; it was retained wherever it was moved", stage.Path())
		}
		return removeDirAt(destination.root, stageName, stage.Path())
	}
	display := filepath.Join(stage.Path(), relative)
	parent, err := stage.openDirectory(filepath.Dir(relative), false)
	if err != nil {
		if downloadChildAbsent(err) {
			return nil
		}
		return err
	}
	defer parent.Close()
	base := filepath.Base(relative)
	info, err := parent.Lstat(base)
	if err != nil {
		if downloadChildAbsent(err) {
			return nil
		}
		return atPath(err, "lstat", display)
	}
	if !downloadOrdinaryDirectory(info) {
		return fmt.Errorf("stage cleanup retained a non-directory at %q", display)
	}
	return removeDirAt(parent, base, display)
}
