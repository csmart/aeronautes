package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func TestDestinationChunkSettings(t *testing.T) {
	for _, operation := range []string{"upload", "copy"} {
		t.Run(operation, func(t *testing.T) {
			for _, tc := range []struct {
				name, info, chunk, want, refusal string
			}{
				{name: "above_maximum", info: `{"swift":{"max_file_size":1048576}}`, chunk: "8Mi", refusal: "exceeds the destination"},
				{name: "below_minimum", chunk: "512Ki", refusal: "below 1Mi"},
				{name: "zero", chunk: "0", refusal: "below 1Mi"},
				{name: "at_maximum", info: `{"swift":{"max_file_size":1048576}}`, chunk: "1Mi", want: "1Mi"},
				{name: "capped_default", info: `{"swift":{"max_file_size":1048576}}`, want: "1048576b"},
				{name: "small_cluster_default", info: `{"swift":{"max_file_size":524288}}`, want: "524288b"},
				{name: "standard_default", want: "5Gi"},
				{name: "missing_limit", info: `{}`, want: "5Gi"},
				{name: "unavailable_info", info: `unavailable`, want: "5Gi"},
				{name: "unavailable_info_below_minimum", info: `unavailable`, chunk: "512Ki", refusal: "below 1Mi"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					h := newHarnessWithInfo(t, tc.info)
					h.connect(t)
					bookmark := h.transfer.connections.ActiveBookmarkID()
					seen := h.installEngine(nil)
					h.mock.AddContainer("chunk-source", "standard")
					h.mock.AddContainer("chunk-destination", "standard")
					seedObject(h.mock, "chunk-source", "asset", "DATA", nil)
					settings := TransferSettingsDTO{ChunkSize: tc.chunk, Transfers: 7, Checkers: 9, TPSLimit: 11, BWLimit: "10M"}
					var id string
					var err error
					if operation == "upload" {
						path := filepath.Join(t.TempDir(), "asset")
						if err := os.WriteFile(path, []byte("DATA"), 0o600); err != nil {
							t.Fatal(err)
						}
						h.transfer.setFilePicker(fakePicker{files: []string{path}})
						picked, pickErr := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{BookmarkID: bookmark, Container: "chunk-destination"})
						if pickErr != nil {
							t.Fatal(pickErr)
						}
						request := UploadRequestDTO{BookmarkID: bookmark, Container: "chunk-destination", SelectionToken: picked.Token, Settings: settings}
						preview, previewErr := h.transfer.PreflightUpload(t.Context(), request)
						if tc.refusal != "" && (previewErr == nil || !strings.Contains(previewErr.Error(), tc.refusal)) {
							t.Errorf("upload preview admitted invalid chunks: %v", previewErr)
						} else if tc.refusal == "" && (previewErr != nil || preview == nil || preview.Rejected()) {
							t.Fatalf("upload preview: %+v %v", preview, previewErr)
						}
						var previewJobs int
						if err := h.db.Reader().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM jobs").Scan(&previewJobs); err != nil || previewJobs != 0 || len(*seen) != 0 {
							t.Errorf("upload preview admitted engine/job work: jobs=%d options=%+v error=%v", previewJobs, *seen, err)
						}
						id, err = h.transfer.StartUpload(t.Context(), request)
					} else {
						request := CopyRequestDTO{SourceBookmarkID: bookmark, SourceContainer: "chunk-source", DestBookmarkID: bookmark, DestContainer: "chunk-destination", Settings: settings}
						_, planErr := h.transfer.PlanCopy(t.Context(), request)
						if tc.refusal != "" && (planErr == nil || !strings.Contains(planErr.Error(), tc.refusal)) {
							t.Errorf("Copy preview admitted invalid chunks: %v", planErr)
						} else if tc.refusal == "" && planErr != nil {
							t.Fatalf("Copy preview: %v", planErr)
						}
						id, err = h.transfer.StartCopy(t.Context(), request)
					}
					if id != "" {
						job := awaitTerminal(t, h, id)
						if tc.refusal != "" {
							t.Errorf("invalid chunk admitted: job=%+v options=%+v", job, *seen)
						} else {
							var recorded TransferSettingsDTO
							if err := json.Unmarshal([]byte(job.Settings), &recorded); err != nil {
								t.Fatal(err)
							}
							// Every job runs with an explicit buffer size, the engine's own
							// default unless overridden, and the record shows it (ADR 0074).
							want := rclone.Options{ChunkSize: tc.want, Transfers: 7, Checkers: 9, TPSLimit: 11, BWLimit: "10M", BufferSize: DefaultBufferSize}
							wantEngine := want
							// The engine of an upload and of a copy may remove a
							// replaced large object's old segments here: the
							// container keeps no versions and reclaiming is on. A copy
							// also reads stored bytes as they are and leaves the size
							// check off.
							wantEngine.NoGzipEncoding, wantEngine.IgnoreSize = operation == "copy", operation == "copy"
							if job.State != "complete" || len(*seen) != 1 || (*seen)[0] != wantEngine || recorded.options() != want {
								t.Errorf("effective settings differ: state=%s recorded=%+v engine=%+v want=%+v", job.State, recorded, *seen, want)
							}
						}
					}
					if tc.refusal == "" {
						if err != nil || id == "" {
							t.Fatalf("valid chunk refused: id=%q error=%v", id, err)
						}
						return
					}
					if id != "" || err == nil || !strings.Contains(err.Error(), tc.refusal) || len(*seen) != 0 {
						t.Errorf("invalid chunk reached engine/job: id=%q error=%v options=%+v", id, err, *seen)
					}
					for _, table := range []string{"jobs", "job_recovery", "job_items", "job_item_observations"} {
						var count int
						if err := h.db.Reader().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
							t.Errorf("refusal left %s records: count=%d error=%v", table, count, err)
						}
					}
				})
			}
		})
	}
}
