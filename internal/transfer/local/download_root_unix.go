//go:build unix

package local

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// sameDevice reports whether both entries live on the filesystem of
// the held root. A bind mount of the same filesystem shares the device
// number; Linux additionally refuses mount roots by attribute.
func sameDevice(root, entry os.FileInfo) bool {
	a, okA := root.Sys().(*syscall.Stat_t)
	b, okB := entry.Sys().(*syscall.Stat_t)
	return okA && okB && a.Dev == b.Dev
}

// directoryUnlinked reports a directory whose last name was removed.
func directoryUnlinked(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 0
}

// withDirectoryFD runs fn with a descriptor for the directory the handle
// is bound to; the descriptor lives only for the call.
func withDirectoryFD(root *os.Root, fn func(fd int) error) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return fn(int(dir.Fd()))
}

// link creates the hard link relative to both directory descriptors, so
// no pathname is resolved between inspection and the effect.
func (p publication) link() error {
	return withDirectoryFD(p.stagedRoot, func(stagedFD int) error {
		return withDirectoryFD(p.targetRoot, func(targetFD int) error {
			if err := unix.Linkat(stagedFD, p.stagedBase, targetFD, p.targetBase, 0); err != nil {
				return &os.LinkError{Op: "link", Old: p.stagedDisplay, New: p.targetDisplay, Err: err}
			}
			return nil
		})
	})
}

// unlinkFileAt removes a non-directory entry by name relative to the
// handle; a directory that took the name is never removed. Errors name
// the entry's captured pathname.
func unlinkFileAt(parent *os.Root, base, display string) error {
	return withDirectoryFD(parent, func(fd int) error {
		if err := unix.Unlinkat(fd, base, 0); err != nil {
			return &os.PathError{Op: "remove", Path: display, Err: err}
		}
		return nil
	})
}

// removeDirAt removes an empty directory by name relative to the handle;
// a file that took the name is never unlinked.
func removeDirAt(parent *os.Root, base, display string) error {
	return withDirectoryFD(parent, func(fd int) error {
		if err := unix.Unlinkat(fd, base, unix.AT_REMOVEDIR); err != nil {
			return &os.PathError{Op: "rmdir", Path: display, Err: err}
		}
		return nil
	})
}
