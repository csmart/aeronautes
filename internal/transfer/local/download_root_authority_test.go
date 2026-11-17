package local

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// The executor's authority over the selected directory is the handle it
// holds from the pick. Each case replaces exactly one entry at one
// deterministic boundary the production code exposes: before the first
// effect, after the engine ran, or the progress callback between two items. The invariants are K-XFER-011 and
// P18: nothing outside the selected directory changes, every reported
// success exists inside the directory picked, residue is reported, and
// each refusal names its reason. The archived before probes under
// docs/experiments/probes/2026-09-11-download-root-authority* failed
// seven of these cases on the pathname-based executor.

type rootAuthorityFixture struct {
	owned, selected, outside, moved, movedParent string
}

func newRootAuthorityFixture(t *testing.T) *rootAuthorityFixture {
	t.Helper()
	owned := t.TempDir()
	f := &rootAuthorityFixture{
		owned:       owned,
		selected:    filepath.Join(owned, "selected"),
		outside:     filepath.Join(owned, "outside"),
		moved:       filepath.Join(owned, "moved"),
		movedParent: filepath.Join(owned, "moved-parent"),
	}
	rootAuthorityWrite(t, f.selected, "keep", "selected sentinel")
	rootAuthorityWrite(t, f.outside, "keep", "outside sentinel")
	return f
}

func rootAuthorityWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// replaceRoot moves the real selected directory away and leaves a link
// to the outside directory at the selected pathname.
func (f *rootAuthorityFixture) replaceRoot(t *testing.T) {
	t.Helper()
	if err := os.Rename(f.selected, f.moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.outside, f.selected); err != nil {
		t.Fatal(err)
	}
}

// replaceEntry moves any existing entry at rel under the selected
// directory away and plants a link to the outside directory there.
func (f *rootAuthorityFixture) replaceEntry(t *testing.T, rel string) {
	t.Helper()
	path := filepath.Join(f.selected, filepath.FromSlash(rel))
	if _, err := os.Lstat(path); err == nil {
		if err := os.Rename(path, f.movedParent); err != nil {
			t.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(f.outside, path); err != nil {
		t.Fatal(err)
	}
}

// rootAuthorityTree records every entry beneath root without following
// links, so a file behind a planted link is never mistaken for a
// contained one.
func rootAuthorityTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			result[rel] = "link:" + target
		case info.IsDir():
			result[rel] = "dir"
		case info.Mode().IsRegular():
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result[rel] = "file:" + string(body)
		default:
			return fmt.Errorf("unmodelled entry %q: %v", rel, info.Mode())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type afterEngine struct {
	inner Engine
	after func()
	calls int
}

func (e *afterEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	e.calls++
	result, err := e.inner.Copy(ctx, src, dst, names, onEvent)
	if err == nil && e.after != nil {
		e.after()
	}
	return result, err
}

func rootAuthorityBody(rel string) string { return "payload for " + rel }

func rootAuthorityFile(rel, route string) DownloadItem {
	return DownloadItem{Name: "scope/" + rel, RelPath: filepath.FromSlash(rel), Size: int64(len(rootAuthorityBody(rel))), Route: route}
}

func rootAuthorityMarker(rel string) DownloadItem {
	return DownloadItem{Name: "scope/" + rel + "/", RelPath: filepath.FromSlash(rel), Dir: true}
}

const (
	rootAuthorityRefusal      = "is not an inspectable ordinary directory"
	rootAuthorityDisplaced    = "the selected directory was relocated or replaced at its pathname"
	rootAuthorityEngineRefuse = "the engine can only be given a pathname; engine items were not attempted"
)

type rootAuthorityCase struct {
	name  string
	items []DownloadItem
	// seam: "start" acts after the handle is held and before the first
	// effect, "engine" after the engine ran, "progress" after the first
	// item, "" never.
	seam string
	// replace: "root", or a path relative to the selected directory.
	replace       string
	deleted       bool
	wantDone      int64
	wantFailed    []string
	wantFailure   string
	wantDisplaced string
	realRoot      string
}

func rootAuthorityCases() []rootAuthorityCase {
	engine := []DownloadItem{rootAuthorityFile("payload.bin", "rclone")}
	engineNested := []DownloadItem{rootAuthorityFile("redirect/payload.bin", "rclone")}
	native := []DownloadItem{rootAuthorityFile("a.bin", "native"), rootAuthorityFile("b.bin", "native")}
	nativeNested := []DownloadItem{rootAuthorityFile("a.bin", "native"), rootAuthorityFile("redirect/b.bin", "native")}
	marker := []DownloadItem{rootAuthorityFile("a.bin", "native"), rootAuthorityMarker("m")}
	markerNested := []DownloadItem{rootAuthorityFile("a.bin", "native"), rootAuthorityMarker("redirect/m")}
	return []rootAuthorityCase{
		{name: "engine_root_after_the_engine", items: engine, seam: "engine", replace: "root",
			wantDone: 1, wantDisplaced: "after the last effect", realRoot: "moved"},
		{name: "native_root_between_items", items: native, seam: "progress", replace: "root",
			wantDone: 2, wantDisplaced: "after the last effect", realRoot: "moved"},
		{name: "native_parent_between_items", items: nativeNested, seam: "progress", replace: "redirect",
			wantDone: 1, wantFailed: []string{"scope/redirect/b.bin"}, wantFailure: rootAuthorityRefusal, realRoot: "selected"},
		{name: "marker_root_between_items", items: marker, seam: "progress", replace: "root",
			wantDone: 2, wantDisplaced: "after the last effect", realRoot: "moved"},
		{name: "marker_parent_between_items", items: markerNested, seam: "progress", replace: "redirect",
			wantDone: 1, wantFailed: []string{"scope/redirect/m/"}, wantFailure: rootAuthorityRefusal, realRoot: "selected"},
		// The test engine takes a pathname, like the child on platforms
		// without a descriptor anchor: a pathname that stopped naming the
		// directory picked is never handed to it.
		{name: "engine_root_before_start", items: engine, seam: "start", replace: "root",
			wantFailed: []string{"scope/payload.bin"}, wantFailure: rootAuthorityEngineRefuse, wantDisplaced: "before the first effect", realRoot: "moved"},
		{name: "native_root_before_start", items: native, seam: "start", replace: "root",
			wantDone: 2, wantDisplaced: "before the first effect", realRoot: "moved"},
		{name: "deleted_root_before_start", items: native, seam: "start", deleted: true},
		{name: "engine_control", items: engineNested, wantDone: 1, realRoot: "selected"},
		{name: "native_control", items: nativeNested, wantDone: 2, realRoot: "selected"},
		{name: "marker_control", items: markerNested, wantDone: 2, realRoot: "selected"},
	}
}

func TestDownloadRootAuthority(t *testing.T) {
	for _, c := range rootAuthorityCases() {
		t.Run(c.name, func(t *testing.T) {
			f := newRootAuthorityFixture(t)
			s, mock := session(t)
			var objects, total int64
			for _, item := range c.items {
				if item.Dir {
					seed(mock, "media", item.Name, "")
					continue
				}
				seed(mock, "media", item.Name, rootAuthorityBody(strings.TrimPrefix(item.Name, "scope/")))
				objects++
				total += item.Size
			}
			act := func() {
				if c.replace == "root" {
					f.replaceRoot(t)
				} else {
					f.replaceEntry(t, c.replace)
				}
			}
			engine := &afterEngine{inner: &fsEngine{s}}
			if c.seam == "engine" {
				engine.after = act
			}
			replaced := false
			progress := func(done, total, bytes int64) {
				if c.seam == "progress" && done == 1 && !replaced {
					replaced = true
					act()
				}
			}
			destination := testDestination(t, f.selected)
			if c.deleted {
				if err := os.RemoveAll(f.selected); err != nil {
					t.Fatal(err)
				}
			} else if c.seam == "start" {
				act()
			}
			before := rootAuthorityTree(t, f.owned)
			plan := &DownloadPreflight{Items: c.items, Objects: objects, TotalBytes: total}
			report, err := DownloadInto(t.Context(), s, engine, "aeronautes_src:media", "media", "scope/", destination, plan, progress)
			after := rootAuthorityTree(t, f.owned)

			// Nothing outside the selected directory changes.
			for key, state := range after {
				if key == "outside" || strings.HasPrefix(key, "outside/") {
					if want, existed := before[key]; !existed || want != state {
						t.Errorf("outside entry %q changed: %q", key, state)
					}
				}
			}
			for key := range before {
				if (key == "outside" || strings.HasPrefix(key, "outside/")) && after[key] == "" {
					t.Errorf("outside entry %q disappeared", key)
				}
			}

			if c.deleted {
				if report != nil || err == nil || !strings.Contains(err.Error(), "deleted after it was picked") {
					t.Fatalf("deleted directory was not refused before any effect: %+v %v", report, err)
				}
				if engine.calls != 0 {
					t.Errorf("deleted directory reached the engine %d times", engine.calls)
				}
				return
			}
			if err != nil || report == nil {
				t.Fatalf("executor returned an error instead of item outcomes: %+v %v", report, err)
			}

			// Exact outcomes: done count, the failed names with their reason,
			// and the displacement note.
			if report.Done != c.wantDone {
				t.Errorf("done %d, want %d: %+v", report.Done, c.wantDone, report)
			}
			failed := map[string]bool{}
			for _, failure := range report.Failures {
				failed[failure.Name] = true
				if c.wantFailure == "" || !strings.Contains(failure.Err, c.wantFailure) {
					t.Errorf("failure %q lacks the expected reason %q: %q", failure.Name, c.wantFailure, failure.Err)
				}
			}
			if len(failed) != len(c.wantFailed) {
				t.Errorf("failures %v, want %v", report.Failures, c.wantFailed)
			}
			for _, name := range c.wantFailed {
				if !failed[name] {
					t.Errorf("expected failure for %q is missing: %+v", name, report.Failures)
				}
			}
			if c.wantDisplaced == "" && report.RootDisplacement != "" || c.wantDisplaced != "" && !strings.Contains(report.RootDisplacement, rootAuthorityDisplaced+" "+c.wantDisplaced) {
				t.Errorf("displacement note %q, want %q", report.RootDisplacement, c.wantDisplaced)
			}

			// Every reported success exists inside the directory picked,
			// reached without any link, and every failure left no file there.
			for _, item := range c.items {
				key := c.realRoot + "/" + filepath.ToSlash(item.RelPath)
				want := "dir"
				if !item.Dir {
					want = "file:" + rootAuthorityBody(strings.TrimPrefix(item.Name, "scope/"))
				}
				if failed[item.Name] {
					if _, present := after[key]; present {
						t.Errorf("failed item %q left an entry at %q: %q", item.Name, key, after[key])
					}
					continue
				}
				if after[key] != want {
					t.Errorf("reported success for %q is not inside the directory picked: %q=%q", item.Name, key, after[key])
				}
			}

			// The application's own temporary is gone whichever way an item
			// ended.
			for key := range after {
				if strings.Contains(key, downloadPartPrefix) {
					t.Errorf("a temporary remains: %q", key)
				}
			}
			if engine.calls > 1 {
				t.Errorf("engine invoked %d times", engine.calls)
			}
		})
	}
}
