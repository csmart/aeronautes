package copy

import (
	"context"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// RemoteSource yields the remotes for ONE child spawn. Long jobs
// outlive tokens, and a child cannot re-authenticate (its environment
// carries a token, never credentials) — so remotes are pulled fresh
// per spawn, not captured once (§26.2/ADR 0009).
type RemoteSource func(ctx context.Context) ([]rclone.Remote, error)

// BindRunner adapts the pinned rclone Runner with a FIXED remote set —
// for short-lived uses and tests; jobs use BindRunnerSource.
func BindRunner(runner *rclone.Runner, remotes []rclone.Remote) Engine {
	return BindRunnerSource(runner, func(context.Context) ([]rclone.Remote, error) {
		return remotes, nil
	})
}

// BindRunnerSource adapts the Runner with per-spawn remote resolution.
func BindRunnerSource(runner *rclone.Runner, source RemoteSource) Engine {
	return boundRunner{runner: runner, source: source}
}

type boundRunner struct {
	runner *rclone.Runner
	source RemoteSource
}

func (b boundRunner) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	remotes, err := b.source(ctx)
	if err != nil {
		return nil, err
	}
	return b.runner.Copy(ctx, remotes, src, dst, names, onEvent)
}
