package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR 0122 decision 3: the engine removes a replaced large object's old
// segments itself as it writes over the object, and it does so even where
// the container has a versions location, whose archived version still uses
// them. So an upload decides once, at its start, whether the engine may: not
// where the container keeps versions, not where the operator's setting keeps
// replaced segments, and not where that setting cannot be read. The question
// the operator answers says which, and the engine is started on the same
// decision.
func TestUploadDecidesOnceWhetherReplacedSegmentsGo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		arrange func(t *testing.T, f *overwriteFixture)
		keeps   bool
		says    string
	}{
		{"no versions and reclaiming on", func(*testing.T, *overwriteFixture) {}, false,
			"the transfer engine REMOVES its old segments under live/ in replaceable_segments"},
		{"a versions location", func(_ *testing.T, f *overwriteFixture) {
			f.h.mock.AddContainer("archive", "standard")
			f.h.mock.EnableVersionArchive("replaceable", "archive", false)
		}, true, "KEPT, because the container keeps versions"},
		{"a history location", func(_ *testing.T, f *overwriteFixture) {
			f.h.mock.AddContainer("archive", "standard")
			f.h.mock.EnableVersionArchive("replaceable", "archive", true)
		}, true, "KEPT, because the container keeps versions"},
		{"reclaiming turned off", func(t *testing.T, f *overwriteFixture) {
			settings := NewSettingsService(settingsFile(t), f.h.transfer.runner)
			current, err := settings.Get(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			current.ReclaimReplacedFragments = false
			if _, err := settings.Set(t.Context(), current); err != nil {
				t.Fatal(err)
			}
			WireSettings(f.h.transfer, settings)
		}, true, "KEPT, because reclaiming is turned off"},
		{"a setting that cannot be read", func(t *testing.T, f *overwriteFixture) {
			file := settingsFile(t)
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
			WireSettings(f.h.transfer, NewSettingsService(file, f.h.transfer.runner))
		}, true, "KEPT, because the setting that allows removing them could not be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOverwriteFixture(t)
			seen := f.h.installEngine(nil)
			tc.arrange(t, f)
			confirmer := &recordingOverwriteConfirmer{yes: true}
			f.h.transfer.setOverwriteConfirmer(confirmer)
			id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
			if err != nil {
				t.Fatalf("a confirmed start was refused: %v", err)
			}
			if job := awaitTerminal(t, f.h, id); job.State != "complete" {
				t.Fatalf("job = %+v", job)
			}
			asked := confirmer.questions()
			if len(asked) != 1 || !strings.Contains(asked[0].Fragments, tc.says) {
				t.Fatalf("the question said %+v, want it to say %q", asked, tc.says)
			}
			if len(*seen) != 1 || !(*seen)[0].LiveDestination || (*seen)[0].KeepReplacedSegments != tc.keeps {
				t.Fatalf("engine options = %+v, want the names themselves as the destination and replaced segments kept = %v", *seen, tc.keeps)
			}
		})
	}
}

// An upload that replaces nothing asks nothing, and its engine still may not
// remove segments where the container keeps versions: a name that appeared
// after the preview is written over like any other.
func TestUploadWithNothingToReplaceStillKeepsSegmentsUnderVersions(t *testing.T) {
	for _, versions := range []bool{false, true} {
		t.Run(map[bool]string{false: "no versions", true: "a versions location"}[versions], func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			seen := h.installEngine(nil)
			h.mock.AddContainer("media", "standard")
			if versions {
				h.mock.AddContainer("archive", "standard")
				h.mock.EnableVersionArchive("media", "archive", false)
			}
			_, id := startFolderUpload(t, h, twoFileFolder(t))
			if job := awaitTerminal(t, h, id); job.State != "complete" {
				t.Fatalf("job = %+v", job)
			}
			if len(*seen) != 1 || (*seen)[0].KeepReplacedSegments != versions {
				t.Fatalf("engine options = %+v, want replaced segments kept = %v", *seen, versions)
			}
		})
	}
}
