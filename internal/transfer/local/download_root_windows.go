package local

import (
	"errors"
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

// link creates the hard link by root-relative names through the held
// root, which resolves both names beneath it; the identity check after
// linking guards the window this leaves.
func (p publication) link() error {
	if err := p.root.root.Link(p.stagedRel, p.targetRel); err != nil {
		var linkErr *os.LinkError
		if errors.As(err, &linkErr) {
			return &os.LinkError{Op: "link", Old: p.stagedDisplay, New: p.targetDisplay, Err: linkErr.Err}
		}
		return err
	}
	return nil
}

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

func removeDirAt(parent *os.Root, base, display string) error {
	info, err := parent.Lstat(base)
	if err != nil {
		return atPath(err, "lstat", display)
	}
	if !downloadOrdinaryDirectory(info) {
		return fmt.Errorf("entry %q is not an ordinary directory; refusing to remove it", display)
	}
	return atPath(parent.Remove(base), "rmdir", display)
}
