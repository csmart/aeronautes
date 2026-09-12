package app

import (
	"context"
	"errors"
	"os"

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

// downloadStageBoundaryAnchoredEngine keeps the boundary on the anchored
// route, so an owned fixture still acts between engine exit and
// placement when the child resolves the stage through a descriptor.
type downloadStageBoundaryAnchoredEngine struct {
	downloadStageBoundaryEngine
	anchored local.AnchoredEngine
}

func (e downloadStageBoundaryAnchoredEngine) CopyInto(ctx context.Context, src, dst string, anchor *os.File, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	result, err := e.anchored.CopyInto(ctx, src, dst, anchor, names, onEvent)
	return result, errors.Join(err, e.after(ctx, dst))
}

// downloadProgress keeps the runner's progress flush and, when a
// placement boundary is wired, lets the fixture act after each item
// outcome, which is between two placements on the engine route.
func (s *TransferService) downloadProgress(ctx context.Context, jobID string, onProgress func(done, total, bytes int64)) func(done, total, bytes int64) {
	boundary := s.downloadPlacementBoundary
	return func(done, total, bytes int64) {
		onProgress(done, total, bytes)
		if boundary != nil {
			boundary(ctx, jobID, done, total)
		}
	}
}

// wrapDownloadStageBoundary preserves the engine's anchored route when
// it has one; a plain engine keeps the plain wrapper.
func wrapDownloadStageBoundary(engine local.Engine, after func(context.Context, string) error) local.Engine {
	wrapped := downloadStageBoundaryEngine{Engine: engine, after: after}
	if anchored, ok := engine.(local.AnchoredEngine); ok {
		return downloadStageBoundaryAnchoredEngine{downloadStageBoundaryEngine: wrapped, anchored: anchored}
	}
	return wrapped
}
