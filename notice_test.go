package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// alreadyRunning decides whether the shell shows the "already running" notice
// or fails with a bare error, so it must recognise a held-lock refusal
// through the wrapping runDesktop adds and must not mistake any other startup
// failure for a second instance.
func TestAlreadyRunningClassifiesTheLockRefusal(t *testing.T) {
	held := fmt.Errorf("lock state directory: %w",
		fmt.Errorf("another aeronautes instance holds /state: %w", app.ErrInstanceAlreadyRunning))
	if !alreadyRunning(held) {
		t.Fatal("a held-lock refusal was not classified as already running")
	}
	for _, other := range []error{
		nil,
		errors.New("open state database: no space left on device"),
		fmt.Errorf("lock state directory: %w", errors.New("mkdir: permission denied")),
	} {
		if alreadyRunning(other) {
			t.Fatalf("an unrelated startup error was misclassified as already running: %v", other)
		}
	}
}
