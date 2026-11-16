package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/transfer/local"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func TestCopyRepresentationFactoryPolicy(t *testing.T) {
	for _, operation := range []string{"copy", "upload", "download"} {
		t.Run(operation, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			bookmark := h.transfer.connections.ActiveBookmarkID()
			session, err := h.transfer.connections.SessionFor(t.Context(), bookmark)
			if err != nil {
				t.Fatal(err)
			}
			seen := h.installEngine(nil)
			// Every job runs with an explicit buffer size (ADR 0074); naming it
			// here keeps the resolved copy settings equal to the request.
			settings := TransferSettingsDTO{ChunkSize: "1Mi", Transfers: 7, Checkers: 9, TPSLimit: 11, BWLimit: "10M", BufferSize: DefaultBufferSize}
			if operation == "copy" {
				_, effective, _, err := h.transfer.buildEngine(t.Context(), CopyRequestDTO{SourceBookmarkID: bookmark, Settings: settings}, session, session, true)
				if err != nil || effective != settings {
					t.Fatalf("Copy settings = %+v; error=%v", effective, err)
				}
			} else {
				remote := "aeronautes_dst"
				if operation == "download" {
					remote = "aeronautes_src"
				}
				if _, err := h.transfer.localEngine(t.Context(), session, remote, settings.options()); err != nil {
					t.Fatal(err)
				}
			}
			want := rclone.Options{ChunkSize: "1Mi", Transfers: 7, Checkers: 9, TPSLimit: 11, BWLimit: "10M", BufferSize: DefaultBufferSize}
			policy := want
			if operation == "copy" {
				// A copy's engine reads stored bytes as they are, writes each
				// object at its name, keeps a replaced large object's old
				// segments when it is told to, and leaves the size check off
				// (ADR 0122 decision 4).
				policy.NoGzipEncoding, policy.LiveDestination, policy.KeepReplacedSegments, policy.IgnoreSize = true, true, true, true
			}
			if len(*seen) != 1 || (*seen)[0] != policy {
				t.Errorf("engine policy = %+v; want %+v", *seen, policy)
			}
			if settings.options() != want {
				t.Errorf("shared settings changed the local policy: %+v", settings.options())
			}
		})
	}
}

func TestCopyRepresentationRunnerPolicy(t *testing.T) {
	for _, operation := range []string{"copy", "upload", "download"} {
		t.Run(operation, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			bookmark := h.transfer.connections.ActiveBookmarkID()
			session, err := h.transfer.connections.SessionFor(t.Context(), bookmark)
			if err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(t.TempDir(), "rclone")
			// This child proves constructor wiring; real-pin system tests
			// separately prove that the policy preserves stored object bytes.
			body := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then echo 'rclone %s'; exit 0; fi\nprintf '%%s\\n' \"$@\" > \"$0.args\"\n", rclone.PinnedVersion)
			if err := os.WriteFile(bin, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
			settings := TransferSettingsDTO{ChunkSize: "1Mi", Transfers: 7, Checkers: 9, TPSLimit: 11, BWLimit: "10M"}
			var result *rclone.Result
			if operation == "copy" {
				engine, _, _, err := h.transfer.buildEngine(t.Context(), CopyRequestDTO{SourceBookmarkID: bookmark, Settings: settings}, session, session, false)
				if err != nil {
					t.Fatal(err)
				}
				result, err = engine.Copy(t.Context(), "aeronautes_src:source", "aeronautes_dst:destination/folder", []string{"asset"}, nil)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				remote := "aeronautes_dst"
				if operation == "download" {
					remote = "aeronautes_src"
				}
				engine, err := h.transfer.localEngine(t.Context(), session, remote, settings.options())
				if err != nil {
					t.Fatal(err)
				}
				if operation == "upload" {
					result, err = engine.Copy(t.Context(), t.TempDir(), "aeronautes_dst:destination/.aeronautes/stage/job/batch", []string{"asset"}, nil)
				} else if rclone.AnchorSupported {
					anchored, ok := engine.(local.AnchoredEngine)
					if !ok {
						t.Fatal("local constructor lost the download descriptor path")
					}
					dir := t.TempDir()
					anchor, openErr := os.Open(dir)
					if openErr != nil {
						t.Fatal(openErr)
					}
					result, err = anchored.CopyInto(t.Context(), "aeronautes_src:source", dir, anchor, []string{"asset"}, nil)
					if closeErr := anchor.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				} else {
					result, err = engine.Copy(t.Context(), "aeronautes_src:source", t.TempDir(), []string{"asset"}, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if result == nil || result.ExitCode != 0 || result.Cancelled || result.LogStreamErr != "" {
				t.Fatalf("constructor child result = %+v", result)
			}
			captured, err := os.ReadFile(bin + ".args")
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSuffix(string(captured), "\n"), "\n")
			separator := slices.Index(args, "--")
			if len(args) < 4 || args[0] != "copy" || separator != len(args)-3 {
				t.Fatalf("unexpected child command: %q", args)
			}
			count := 0
			for i, arg := range args {
				if arg == "--no-gzip-encoding" {
					count++
					if i >= separator {
						t.Error("encoding policy follows the positional separator")
					}
				}
			}
			want := 0
			if operation == "copy" {
				want = 1
			}
			if count != want {
				t.Errorf("encoding flag count = %d; want %d", count, want)
			}
			for flag, value := range map[string]string{"--retries": "1", "--low-level-retries": "2", "--swift-chunk-size": "1Mi", "--transfers": "7", "--checkers": "9", "--tpslimit": "11", "--bwlimit": "10M"} {
				i := slices.Index(args[:separator], flag)
				if i < 0 || i+1 >= separator || args[i+1] != value {
					t.Errorf("pacing option %s changed", flag)
				}
			}
		})
	}
}
