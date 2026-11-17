package app

import (
	"context"
	"os"

	"github.com/csmart/aeronautes/internal/transfer/local"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// downloadBoundaryEngine asks the wired boundary before each engine child
// starts, so an owned fixture can hold a download at a known point. A
// boundary that returns an error stops that child from starting.
type downloadBoundaryEngine struct {
	local.Engine
	before func(context.Context, string) error
}

func (e downloadBoundaryEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	if err := e.before(ctx, dst); err != nil {
		return nil, err
	}
	return e.Engine.Copy(ctx, src, dst, names, onEvent)
}

// downloadBoundaryAnchoredEngine keeps the boundary on the anchored route.
type downloadBoundaryAnchoredEngine struct {
	downloadBoundaryEngine
	anchored local.AnchoredEngine
}

func (e downloadBoundaryAnchoredEngine) CopyInto(ctx context.Context, src, dst string, anchor *os.File, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	if err := e.before(ctx, dst); err != nil {
		return nil, err
	}
	return e.anchored.CopyInto(ctx, src, dst, anchor, names, onEvent)
}

// wrapDownloadBoundary preserves the engine's anchored route when it has
// one; a plain engine keeps the plain wrapper.
func wrapDownloadBoundary(engine local.Engine, before func(context.Context, string) error) local.Engine {
	wrapped := downloadBoundaryEngine{Engine: engine, before: before}
	if anchored, ok := engine.(local.AnchoredEngine); ok {
		return downloadBoundaryAnchoredEngine{downloadBoundaryEngine: wrapped, anchored: anchored}
	}
	return wrapped
}
