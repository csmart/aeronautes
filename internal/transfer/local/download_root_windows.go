package local

import (
	"fmt"
	"os"
)

// Volume mount points and junctions are reparse points, which the
// ordinary-directory inspection already refuses, so no device
// comparison is needed here.
func sameDevice(os.FileInfo, os.FileInfo) bool { return true }

func refuseMountRoot(*os.Root, string) error { return nil }

// A directory with an open handle cannot lose its last name here.
func directoryUnlinked(os.FileInfo) bool { return false }

func unlinkFileAt(parent *os.Root, base, display string) error {
	info, err := parent.Lstat(base)
	if err != nil {
		return atPath(err, "lstat", display)
	}
	if info.IsDir() {
		return fmt.Errorf("entry %q is a directory; refusing to remove it as a file", display)
	}
	return atPath(parent.Remove(base), "remove", display)
}
