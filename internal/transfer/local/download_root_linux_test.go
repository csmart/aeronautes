//go:build linux

package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// A mount root inside the selected directory is refused by attribute on
// kernels that report it; the procfs mount is such a root on every
// Linux host that runs this suite.
func TestRefuseMountRoot(t *testing.T) {
	var stat unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, "/proc", unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BASIC_STATS, &stat); err != nil {
		t.Skipf("statx unavailable: %v", err)
	}
	if stat.Attributes_mask&unix.STATX_ATTR_MOUNT_ROOT == 0 {
		t.Skip("kernel does not report mount roots")
	}
	if stat.Attributes&unix.STATX_ATTR_MOUNT_ROOT == 0 {
		t.Skip("/proc is not a mount root here")
	}
	system, err := os.OpenRoot("/")
	if err != nil {
		t.Fatal(err)
	}
	defer system.Close()
	err = refuseMountRoot(system, "proc")
	if err == nil || !strings.Contains(err.Error(), "is a mount point") {
		t.Fatalf("mount root was not refused: %v", err)
	}
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	destination := testDestination(t, base)
	if err := refuseMountRoot(destination.root, "plain"); err != nil {
		t.Fatalf("ordinary directory refused as a mount root: %v", err)
	}
}
