//go:build unix

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// AcquireInstanceLock takes an exclusive advisory lock beside the state
// database. Two processes sharing one state directory would mark each
// other's running jobs interrupted and sweep each other's staging
// litter mid-write — the second instance refuses instead.
func AcquireInstanceLock(stateDir string) (release func(), err error) {
	path := filepath.Join(stateDir, "aeronautes.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		// LOCK_NB reports EWOULDBLOCK (EAGAIN on Linux) only when another
		// process already holds the lock; any other errno is a real locking
		// failure, not a second instance, so only the former is named as one.
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another aeronautes instance holds %s: %w", stateDir, ErrInstanceAlreadyRunning)
		}
		return nil, fmt.Errorf("lock %s: %w", stateDir, err)
	}
	return func() { f.Close() }, nil
}
