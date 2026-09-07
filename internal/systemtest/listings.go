package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

func (h *Harness) BrowseContainers(ctx context.Context, marker string, limit int) (app.ContainerPageDTO, *Receipt) {
	page, err := h.Browse.Containers(ctx, marker, limit)
	return page, &Receipt{h: h, action: "BrowseService.Containers", err: err}
}

func (h *Harness) BrowseObjects(ctx context.Context, container, prefix, marker string, limit int) (app.ObjectPageDTO, *Receipt) {
	page, err := h.Browse.Objects(ctx, container, prefix, marker, limit)
	return page, &Receipt{h: h, action: "BrowseService.Objects", err: err}
}
