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
	picked, err := h.transfer.PickDownloadDir(t.Context(), SelectionScopeDTO{BookmarkID: bookmark, Container: "shutdown-handles", Prefix: "scope/"})
	if err != nil {
		t.Fatal(err)
	}
	// A read-only lease is what a preflight takes. It is this test's own
	// handle and must survive both the start, which consumes the
	// selection, and the drain.
	ownLease, _, err := h.transfer.selections.leaseDir(picked.Token,
		activeScope(t, h, "shutdown-handles", "scope/"))
	if err != nil || ownLease == nil {
		t.Fatalf("picker did not retain a directory handle: %v %v", ownLease, err)
	}
	t.Cleanup(func() {
		if err := ownLease.Close(); err != nil {
			t.Error(err)
		}
	})
	before := shutdownDirectoryHandles(t, directory)
	if before == 0 {
		t.Fatal("native descriptor control did not find the picker handle")
	}
	// Starting consumes the selection and closes the pick's own handle
	// (ADR 0041), so the settled count is one below the pre-start one.
	settled := before - 1
	id, err := h.transfer.StartDownload(t.Context(), DownloadRequestDTO{BookmarkID: bookmark,
		Container: "shutdown-handles", Prefix: "scope/", DestDirToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { barrier.unblock(); cleanupShutdownJob(t, h, id) })
	barrier.waitUntilEntered(t)
	if held := shutdownDirectoryHandles(t, directory); held <= settled {
		t.Fatalf("download did not acquire its own directory handles: settled=%d held=%d", settled, held)
	}
	shutdownTestRunner(t, h.transfer.runner)
	if after := shutdownDirectoryHandles(t, directory); after != settled {
		t.Fatalf("drain left job directory handles open: settled=%d after=%d", settled, after)
	}
	if removed, err := ownLease.Removed(); removed || err != nil {
		t.Fatalf("job shutdown closed a derived lease: removed=%v error=%v", removed, err)
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
