//go:build unix

package app

import (
	"errors"
	"testing"
)

// A second process sharing one state directory would mark the first one's
// running jobs interrupted and sweep its staging litter, so the lock must
// refuse the second holder and name that refusal. Releasing the lock, as a
// process exit does, must let the next launch straight back in: the lock is
// a live OS lock, not a stale pid file, which is what makes the startup
// crash recovery safe to run.
func TestAcquireInstanceLockRefusesASecondHolder(t *testing.T) {
	dir := t.TempDir()
	release, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := AcquireInstanceLock(dir); !errors.Is(err, ErrInstanceAlreadyRunning) {
		t.Fatalf("second acquire = %v, want ErrInstanceAlreadyRunning", err)
	}
	release()
	again, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	again()
}
