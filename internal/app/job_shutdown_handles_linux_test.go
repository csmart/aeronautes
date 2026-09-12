//go:build linux

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJobShutdownClosesDownloadHandles(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("shutdown-handles", "standard")
	seedObject(h.mock, "shutdown-handles", "scope/file", "retained contents", nil)
	barrier := newEngineBarrier(t)
	h.installEngine(barrier)
	directory := t.TempDir()
	h.transfer.setFilePicker(fakePicker{dir: directory})
	picked, err := h.transfer.PickDownloadDir(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h.transfer.selections.mu.Lock()
	pickerRoot := h.transfer.selections.dirs[picked.Token].root
	h.transfer.selections.mu.Unlock()
	if pickerRoot == nil {
		t.Fatal("picker did not retain a directory handle")
	}
	t.Cleanup(func() {
		if err := pickerRoot.Close(); err != nil {
			t.Error(err)
		}
	})
	before := shutdownDirectoryHandles(t, directory)
	if before == 0 {
		t.Fatal("native descriptor control did not find the picker handle")
	}
	id, err := h.transfer.StartDownload(t.Context(), DownloadRequestDTO{BookmarkID: bookmark,
		Container: "shutdown-handles", Prefix: "scope/", DestDirToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { barrier.unblock(); cleanupShutdownJob(t, h, id) })
	barrier.waitUntilEntered(t)
	if held := shutdownDirectoryHandles(t, directory); held <= before {
		t.Fatalf("download did not acquire its own directory handles: before=%d held=%d", before, held)
	}
	shutdownTestRunner(t, h.transfer.runner)
	if after := shutdownDirectoryHandles(t, directory); after != before {
		t.Fatalf("drain left job directory handles open: before=%d after=%d", before, after)
	}
	if removed, err := pickerRoot.Removed(); removed || err != nil {
		t.Fatalf("job shutdown closed the picker lifetime: removed=%v error=%v", removed, err)
	}
	assertJobBindingsReleased(t, h, []string{bookmark})
}

func shutdownDirectoryHandles(t *testing.T, path string) int {
	t.Helper()
	want, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		info, err := os.Stat(filepath.Join("/proc/self/fd", entry.Name()))
		if os.IsNotExist(err) {
			// ReadDir's own descriptor can close before this inspection.
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(want, info) {
			count++
		}
	}
	return count
}
