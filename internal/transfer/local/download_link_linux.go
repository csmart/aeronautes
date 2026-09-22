//go:build linux

package local

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// linkDescriptorAt publishes the open descriptor's inode at base beneath
// the target directory. The link source is named through the process's
// own descriptor table rather than through the staged name, so the file
// that was digested is the file that appears, whatever the staged name
// denotes by then (ADR 0070 decision 4).
func linkDescriptorAt(verified *os.File, targetRoot *os.Root, targetBase, stagedDisplay, targetDisplay string) error {
	return withDirectoryFD(targetRoot, func(targetFD int) error {
		source := fmt.Sprintf("/proc/self/fd/%d", verified.Fd())
		if err := unix.Linkat(unix.AT_FDCWD, source, targetFD, targetBase, unix.AT_SYMLINK_FOLLOW); err != nil {
			return &os.LinkError{Op: "link", Old: stagedDisplay, New: targetDisplay, Err: err}
		}
		return nil
	})
}
