package app

import (
	"context"
	"fmt"
)

// ShutdownWork joins job and Find ownership before the shell releases
// their resources. It is composition, never a renderer action.
func ShutdownWork(ctx context.Context, runner *jobRunner, find *FindService) error {
	drained := find.stopFind()
	if err := runner.Shutdown(ctx); err != nil {
		return fmt.Errorf("wait for owned jobs: %w", err)
	}
	select {
	case <-drained:
		return nil
	default:
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for Find workers: %w", ctx.Err())
	}
}
