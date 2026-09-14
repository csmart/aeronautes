package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

// BrowseContainers takes the bookmark the caller means, because a listing
// read is bound to the account it is for and a fixture states which account
// it is asking about rather than asking the backend (ADR 0049).
func (h *Harness) BrowseContainers(ctx context.Context, bookmarkID, marker string, limit int) (app.ContainerPageDTO, *Receipt) {
	page, err := h.Browse.Containers(ctx, bookmarkID, marker, limit)
	return page, &Receipt{h: h, action: "BrowseService.Containers", err: err}
}

func (h *Harness) BrowseObjects(ctx context.Context, bookmarkID, container, prefix, marker string, limit int) (app.ObjectPageDTO, *Receipt) {
	page, err := h.Browse.Objects(ctx, bookmarkID, container, prefix, marker, limit)
	return page, &Receipt{h: h, action: "BrowseService.Objects", err: err}
}

func (h *Harness) PlanCopy(ctx context.Context, request app.CopyRequestDTO) (*app.CopyPlanDTO, *Receipt) {
	plan, err := h.Transfer.PlanCopy(ctx, request)
	return plan, &Receipt{h: h, action: "TransferService.PlanCopy", err: err}
}
