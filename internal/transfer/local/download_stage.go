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
	// Attempted says whether cleanup ran at all. It is false only when
	// required item recording failed and the run stopped before cleanup.
	Attempted bool
	// Retained says whether anything remains under the stage afterwards,
	// whatever kept it there: a refusal at a planned name, a removal that
	// failed, or cleanup never having run.
	//
	// The two are independent in both directions — cleanup can run and
	// still leave residue, and it can be skipped with nothing on disk —
	// so one flag cannot carry both without a reader having to know which
	// era it was written in (ADR 0048).
	Retained bool
}

func (r *StageCleanupResult) failed(err error) {
	r.Issues++
	if r.FirstError == "" {
		r.FirstError = err.Error()
	}
}

// cleanupDownloadStage removes what this executor planned into its own
// stage: the planned files first, then the planned directories deepest
// first, then the stage itself by name beneath the held root once the
// name still denotes it (ADR 0048).
//
// The candidate set comes from the job's own items, never from listing
// the stage, so a name this job did not plan is never a candidate. Every
// removal is issued through the held stage handle rather than through a
// pathname, so a rename of the stage cannot redirect one.
func cleanupDownloadStage(destination, stage *DestinationRoot, stageName string, items map[string]DownloadItem) *StageCleanupResult {
	result := &StageCleanupResult{Attempted: true}
	dirs := map[string]bool{".": true}
	planned := make([]string, 0, len(items))
	for _, item := range items {
		dir := filepath.Dir(item.RelPath)
		if outsideStageScope(item.RelPath) || outsideStageScope(dir) {
			result.failed(fmt.Errorf("selected stage directory is outside the relative cleanup scope"))
			continue
		}
		planned = append(planned, item.RelPath)
		for dir != "." {
			dirs[dir] = true
			dir = filepath.Dir(dir)
		}
	}
	// The held handle follows the directory's identity, not its name, so a
	// stage another actor has moved is still reachable through it. Every
	// removal issued through it would then land inside the moved location,
	// files and directories alike, which ADR 0035 decision 7 retains and
	// reports rather than touches. Nothing is removed once that is known:
	// an empty planned tree would otherwise have its directories unlinked
	// wherever the stage now is, with no file left to make the rmdir fail.
	displaced, displacedErr := stage.displacedAt(destination.root, stageName)
	if displacedErr != nil {
		result.failed(displacedErr)
		result.Retained = true
		return result
	}
	if displaced {
		result.failed(fmt.Errorf("stage directory %q no longer sits at its name under the selected directory; it was retained wherever it was moved", stage.Path()))
		result.Retained = true
		return result
	}
	sort.Strings(planned)
	for _, rel := range planned {
		if err := removePlannedStageFile(stage, rel); err != nil {
			result.failed(err)
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
	rootRemoved := false
	for _, dir := range ordered {
		if err := removeEmptyStageDirectory(destination, stage, stageName, dir); err != nil {
			result.failed(err)
			continue
		}
		if dir == "." {
			rootRemoved = true
		}
	}
	// Retained describes the disk, not the path that reached here: the
	// stage root is gone only when nothing under it survived removal
	// (ADR 0048). It was previously left false while a stage holding
	// staged bytes was retained by a failed rmdir.
	result.Retained = !rootRemoved
	return result
}

// outsideStageScope reports a planned name that cannot be resolved
// beneath the stage, so it is never a removal candidate.
func outsideStageScope(rel string) bool {
	return filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" || filepath.Clean(rel) != rel ||
		rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// removePlannedStageFile removes one file this job planned into the
// stage, through the held stage handle, and only while an ordinary file
// holds that name. An absent name needs no removal; a link, a directory
// or anything else there is retained and reported rather than removed,
// and is never followed (ADR 0048).
//
// This establishes no claim about the bytes: the engine writes these
// files as a child process and this executor never holds a descriptor to
// them, so there is no identity to compare. What is bounded is the
// directory and the set of names.
func removePlannedStageFile(stage *DestinationRoot, relative string) error {
	display := filepath.Join(stage.Path(), relative)
	// A parent this pass cannot open is not reported here. Every ancestor
	// of a planned file is itself a planned directory, so the directory
	// pass reaches the same component and reports it once; reporting it
	// again would count one condition twice and change what Retained and
	// the issue count mean.
	parent, _, err := stage.openDirectory(filepath.Dir(relative), false)
	if err != nil {
		return nil
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
	if !info.Mode().IsRegular() {
		return fmt.Errorf("stage cleanup retained a non-file at %q", display)
	}
	return unlinkFileAt(parent, base, display)
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
	parent, _, err := stage.openDirectory(filepath.Dir(relative), false)
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
