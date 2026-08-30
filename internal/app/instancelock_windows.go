//go:build windows

package app

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

// AcquireInstanceLock holds the state-directory lock via a Windows file
// opened with NO sharing: a second CreateFile on the same path fails
// with ERROR_SHARING_VIOLATION while the first handle lives, and the
// kernel releases it when the process dies. Two processes sharing one
// state directory would mark each other's running jobs interrupted and
// sweep each other's staging litter mid-commit (review F-01) — the
// second instance refuses instead.
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
		return nil, fmt.Errorf("another aeronautes instance holds %s: %w", stateDir, err)
	}
	// Keep the pointer alive for the handle's lifetime semantics.
	_ = unsafe.Pointer(pathPtr)
	return func() { _ = syscall.CloseHandle(handle) }, nil
}
