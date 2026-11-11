package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An upload or download runs with the profile and overrides its dialog
// sent, resolved as a copy's are, and records each value with where it
// came from (§38.3, §45). The case mixes all three sources: checkers from
// the profile, transfers set for this job, and the buffer size the engine
// defaults to (ADR 0074 decision 3).
func TestLocalTransfersRunWithTheirProfileAndOverrides(t *testing.T) {
	for _, route := range []string{"upload", "download"} {
		t.Run(route, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			bookmark := h.transfer.connections.ActiveBookmarkID()
			seen := h.installEngine(nil)
			h.mock.AddContainer("media", "standard")
			seedObject(h.mock, "media", "scope/asset", "DATA", nil)
			settings := TransferSettingsDTO{Transfers: 6}

			start := func(profile string) (string, error) {
				if route == "upload" {
					path := filepath.Join(t.TempDir(), "asset")
					if err := os.WriteFile(path, []byte("DATA"), 0o600); err != nil {
						t.Fatal(err)
					}
					h.transfer.setFilePicker(fakePicker{files: []string{path}})
					picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "new/"})
					if err != nil {
						t.Fatal(err)
					}
					return h.transfer.StartUpload(t.Context(), UploadRequestDTO{
						BookmarkID: bookmark, Container: "media", Prefix: "new/", SelectionToken: picked.Token,
						Profile: profile, Settings: settings,
					})
				}
				h.transfer.setFilePicker(fakePicker{dir: t.TempDir()})
				picked, err := h.transfer.PickDownloadDir(t.Context(), SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/"})
				if err != nil {
					t.Fatal(err)
				}
				return h.transfer.StartDownload(t.Context(), DownloadRequestDTO{
					BookmarkID: bookmark, Container: "media", Prefix: "scope/", DestDirToken: picked.Token,
					Profile: profile, Settings: settings,
				})
			}

			if _, err := start("turbo"); err == nil || !strings.Contains(err.Error(), "unknown performance profile") {
				t.Fatalf("an unknown profile was not refused: %v", err)
			}
			if len(*seen) != 0 {
				t.Fatalf("a refused profile reached the engine: %+v", *seen)
			}

			id, err := start(string(ProfileAggressive))
			if err != nil {
				t.Fatal(err)
			}
			job := awaitTerminal(t, h, id)
			if job.State != "complete" || len(*seen) != 1 {
				t.Fatalf("state=%s engine options=%+v", job.State, *seen)
			}
			given := (*seen)[0]
			if given.Transfers != 6 || given.Checkers != 32 || given.BufferSize != DefaultBufferSize {
				t.Fatalf("engine options = %+v, want transfers 6, checkers 32, buffer %s", given, DefaultBufferSize)
			}

			var recorded effectiveRecord
			if err := json.Unmarshal([]byte(job.Settings), &recorded); err != nil {
				t.Fatalf("recorded settings %s: %v", job.Settings, err)
			}
			if recorded.Profile != string(ProfileAggressive) || recorded.Transfers != 6 || recorded.Checkers != 32 || recorded.BufferSize != DefaultBufferSize {
				t.Fatalf("recorded settings = %s", job.Settings)
			}
			want := map[string]string{"transfers": "override", "checkers": "profile", "bufferSize": "default"}
			for knob, origin := range want {
				if recorded.Origins[knob] != origin {
					t.Errorf("origin of %s = %q, want %q (record %s)", knob, recorded.Origins[knob], origin, job.Settings)
				}
			}
			// An upload's chunk size is calculated against the destination; a
			// download segments nothing, so its record names no chunk origin.
			chunkOrigin, named := recorded.Origins["chunkSize"]
			if route == "upload" && (chunkOrigin != "calculated" || recorded.ChunkSize == "") {
				t.Errorf("upload chunk = %q origin %q", recorded.ChunkSize, chunkOrigin)
			}
			if route == "download" && named {
				t.Errorf("download record names a chunk origin %q", chunkOrigin)
			}
		})
	}
}
