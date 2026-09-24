package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ADR 0074 decision 3: every job runs with an explicit buffer size, the
// engine's own default unless overridden, within a bounded range.
func TestBufferSizeDefaultsAndBounds(t *testing.T) {
	for _, profile := range []Profile{ProfileBalanced, ProfileAuto, ProfileAggressive, ProfileCustom} {
		settings, err := ResolveSettings(profile, TransferSettingsDTO{}, nil)
		if err != nil || settings.BufferSize != DefaultBufferSize {
			t.Fatalf("%s buffer size = %+v %v", profile, settings, err)
		}
		settings, err = ResolveSettings(profile, TransferSettingsDTO{BufferSize: "32Mi"}, nil)
		if err != nil || settings.BufferSize != "32Mi" {
			t.Fatalf("%s override = %+v %v", profile, settings, err)
		}
	}
	for _, size := range []string{"512k", "2Gi", "abc", "0"} {
		if err := (TransferSettingsDTO{BufferSize: size}).Validate(); err == nil {
			t.Fatalf("buffer size %q accepted", size)
		}
	}
	for _, size := range []string{"1Mi", "16Mi", "1Gi", "256M"} {
		if err := (TransferSettingsDTO{BufferSize: size}).Validate(); err != nil {
			t.Fatalf("buffer size %q refused: %v", size, err)
		}
	}
}

// The download route, which resolves no profile, gives its child the
// default or the override and records what it gave.
func TestDownloadRunsWithAnExplicitBufferSize(t *testing.T) {
	for _, override := range []string{"", "32Mi"} {
		t.Run("override_"+override, func(t *testing.T) {
			h := newHarness(t)
			ctx := t.Context()
			h.connect(t)
			seen := h.installEngine(nil)
			h.mock.AddContainer("media", "standard")
			for i := range 3 {
				seedObject(h.mock, "media", fmt.Sprintf("scope/o-%d.bin", i), "x", nil)
			}
			h.transfer.setFilePicker(fakePicker{dir: t.TempDir()})
			bookmark := h.transfer.connections.ActiveBookmarkID()
			picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/"})
			if err != nil || picked == nil {
				t.Fatalf("pick: %+v %v", picked, err)
			}
			id, err := h.transfer.StartDownload(ctx, DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/", DestDirToken: picked.Token, Settings: TransferSettingsDTO{BufferSize: override}})
			if err != nil {
				t.Fatal(err)
			}
			job := awaitTerminal(t, h, id)
			want := override
			if want == "" {
				want = DefaultBufferSize
			}
			if job.State != "complete" || len(*seen) != 1 || (*seen)[0].BufferSize != want {
				t.Fatalf("engine options: state=%s seen=%+v want buffer %s", job.State, *seen, want)
			}
			var recorded TransferSettingsDTO
			if err := json.Unmarshal([]byte(job.Settings), &recorded); err != nil || recorded.BufferSize != want || !strings.Contains(job.Settings, `"bufferSize":"`+want+`"`) {
				t.Fatalf("recorded settings %s: %+v %v", job.Settings, recorded, err)
			}
		})
	}
}
