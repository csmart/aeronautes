package app

import (
	"context"
	"fmt"
)

// ShutdownWork joins job, Find and synchronous-writer ownership before
// the shell releases their resources. It is composition, never a
// renderer action.
func ShutdownWork(ctx context.Context, runner *jobRunner, find *FindService, mutate *MutateService) error {
	drained := find.stopFind()
	// Writers first: a held metadata record is unrecoverable, and a job
	// wait can consume a shared deadline. The shell passes Background.
	writerErr := mutate.shutdownWriters(ctx)
	jobErr := runner.Shutdown(ctx)
	var findErr error
	select {
	case <-drained:
	default:
		select {
		case <-drained:
		case <-ctx.Done():
			findErr = fmt.Errorf("wait for Find workers: %w", ctx.Err())
		}
	}
	if jobErr != nil {
		return fmt.Errorf("wait for owned jobs: %w", jobErr)
	}
	if writerErr != nil {
		return writerErr
	}
	return findErr
}
