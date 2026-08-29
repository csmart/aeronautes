package copy

import (
	"context"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// BindRunner adapts the pinned rclone Runner to the Engine interface
// with the job's remotes fixed (both endpoints of a cross-account copy;
// tokens stay in the child environment per the runner's contract).
func BindRunner(runner *rclone.Runner, remotes []rclone.Remote) Engine {
	return boundRunner{runner: runner, remotes: remotes}
}

type boundRunner struct {
	runner  *rclone.Runner
	remotes []rclone.Remote
}

func (b boundRunner) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	return b.runner.Copy(ctx, b.remotes, src, dst, names, onEvent)
}

func (b boundRunner) CopyTo(ctx context.Context, src, dst string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	return b.runner.CopyTo(ctx, b.remotes, src, dst, onEvent)
}
