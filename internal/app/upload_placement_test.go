package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUploadPreviewNamesLargeObjectPlacement checks the §63.8 disclosure the
// local dialog renders: the upload preview says a file will land as a
// segmented destination DLO only when the engine route actually segments it —
// larger than the effective --swift-chunk-size read in the engine's own units,
// and never a native-routed name the engine uploads as one plain object. The
// expectations come from the engine's rule, not this code's shape.
func TestUploadPreviewNamesLargeObjectPlacement(t *testing.T) {
	const mi = 1 << 20
	for _, tc := range []struct {
		name  string
		info  string // cluster /info; "" is the 5Gi default
		file  string // destination basename; a newline forces the native route
		size  int
		chunk string // per-job --swift-chunk-size override; "" takes the default
		want  bool
	}{
		{name: "small_engine_file_under_default", file: "asset", size: 16, want: false},
		{name: "engine_file_over_capped_chunk", info: `{"swift":{"max_file_size":1048576}}`, file: "asset", size: mi + 1, want: true},
		{name: "engine_file_exactly_at_capped_chunk", info: `{"swift":{"max_file_size":1048576}}`, file: "asset", size: mi, want: false},
		// Finding 1: a native-routed name is one plain PutObject, so it is not a
		// DLO even when it is larger than the chunk size. The engine control
		// below shares its size and chunk and differs only by route.
		{name: "native_route_never_segments", file: "big\nname", size: 2 * mi, chunk: "1Mi", want: false},
		{name: "engine_route_segments_same_size", file: "asset", size: 2 * mi, chunk: "1Mi", want: true},
		// Finding 2: the pinned rclone reads a suffixless chunk as KiB, so
		// "1048576" is 1Gi to the engine and a 2Mi file is below it.
		{name: "suffixless_chunk_is_kib_to_engine", file: "asset", size: 2 * mi, chunk: "1048576", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessWithInfo(t, tc.info)
			h.connect(t)
			bookmark := h.transfer.connections.ActiveBookmarkID()
			h.mock.AddContainer("chunk-destination", "standard")

			path := filepath.Join(t.TempDir(), tc.file)
			if err := os.WriteFile(path, make([]byte, tc.size), 0o600); err != nil {
				t.Fatal(err)
			}
			h.transfer.setFilePicker(fakePicker{files: []string{path}})
			picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{BookmarkID: bookmark, Container: "chunk-destination"})
			if err != nil {
				t.Fatal(err)
			}

			preview, err := h.transfer.PreflightUpload(t.Context(), UploadRequestDTO{
				BookmarkID: bookmark, Container: "chunk-destination", SelectionToken: picked.Token,
				Settings: TransferSettingsDTO{ChunkSize: tc.chunk},
			})
			if err != nil || preview == nil || preview.Rejected() {
				t.Fatalf("clean upload preview expected: %+v %v", preview, err)
			}
			if preview.LargeObjectsAsDLO != tc.want {
				t.Errorf("LargeObjectsAsDLO = %v, want %v for file %q size %d chunk %q info %q",
					preview.LargeObjectsAsDLO, tc.want, tc.file, tc.size, tc.chunk, tc.info)
			}
		})
	}
}
