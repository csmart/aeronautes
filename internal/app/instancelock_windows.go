//go:build windows

package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

// errorSharingViolation is the Windows error a second CreateFile returns
// while the first instance holds the no-sharing lock handle.
const errorSharingViolation syscall.Errno = 32

// AcquireInstanceLock holds the state-directory lock via a Windows file
// opened with NO sharing: a second CreateFile on the same path fails
// with ERROR_SHARING_VIOLATION while the first handle lives, and the
// kernel releases it when the process dies. Two processes sharing one
// state directory would mark each other's running jobs interrupted while
// they still run (review F-01) — the second instance refuses instead.
func AcquireInstanceLock(stateDir string) (release func(), err error) {
	path := filepath.Join(stateDir, "aeronautes.lock")
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(
		pathPtr,
		syscall.GENERIC_WRITE,
		0, // no sharing: the exclusivity IS the lock
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		// A sharing violation means the first instance holds the handle; the
		// kernel releases it when that process dies. Any other CreateFile
		// error is a real locking failure, not a second instance.
		var errno syscall.Errno
		if errors.As(err, &errno) && errno == errorSharingViolation {
			return nil, fmt.Errorf("another aeronautes instance holds %s: %w", stateDir, ErrInstanceAlreadyRunning)
		}
		return nil, fmt.Errorf("lock %s: %w", stateDir, err)
	}
	// Keep the pointer alive for the handle's lifetime semantics.
	_ = unsafe.Pointer(pathPtr)
	return func() { _ = syscall.CloseHandle(handle) }, nil
}
