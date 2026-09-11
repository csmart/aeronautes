//go:build linux

package local

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// refuseMountRoot refuses a component that is the root of a mount, which
// the device comparison cannot see for a bind mount of the same
// filesystem. Kernels that do not report the attribute leave the
// device comparison as the only mount check.
func refuseMountRoot(parent *os.Root, component string) error {
	return withDirectoryFD(parent, func(fd int) error {
		mounted, err := mountRootAt(fd, component)
		if err != nil {
			return err
		}
		if mounted {
			return fmt.Errorf("destination component %q is a mount point; mounts inside the selected directory are refused", component)
		}
		return nil
	})
}

func mountRootAt(fd int, component string) (bool, error) {
	var stat unix.Statx_t
	err := unix.Statx(fd, component, unix.AT_SYMLINK_NOFOLLOW|unix.AT_STATX_SYNC_AS_STAT, unix.STATX_BASIC_STATS, &stat)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) {
			return false, nil
		}
		return false, &os.PathError{Op: "statx", Path: component, Err: err}
	}
	if stat.Attributes_mask&unix.STATX_ATTR_MOUNT_ROOT == 0 {
		return false, nil
	}
	return stat.Attributes&unix.STATX_ATTR_MOUNT_ROOT != 0, nil
}
