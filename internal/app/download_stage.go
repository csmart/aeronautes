package app

import (
	"context"
	"errors"

	"github.com/csmart/aeronautes/internal/transfer/local"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

type downloadStageBoundaryEngine struct {
	local.Engine
	after func(context.Context, string) error
}

func (e downloadStageBoundaryEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	result, err := e.Engine.Copy(ctx, src, dst, names, onEvent)
	return result, errors.Join(err, e.after(ctx, dst))
}
