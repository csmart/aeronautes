package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

func (h *Harness) UpdateContainer(ctx context.Context, name, baseFingerprint string, edit app.ContainerEditDTO) (app.MutationResultDTO, *Receipt) {
	result, err := h.Mutate.UpdateContainer(ctx, h.ActiveScope(ctx), name, baseFingerprint, edit)
	return result, &Receipt{h: h, action: "MutateService.UpdateContainer", outcome: result.Outcome, err: err}
}

func (h *Harness) CreateContainer(ctx context.Context, create app.ContainerCreateDTO) (app.ContainerDetailDTO, *Receipt) {
	result, err := h.Mutate.CreateContainer(ctx, h.ActiveScope(ctx), create)
	return result, &Receipt{h: h, action: "MutateService.CreateContainer", err: err}
}

// ActiveScope is the active connection's bookmark and generation as the
// bridge reports it, the scope a caller names on a write (ADR 0079).
func (h *Harness) ActiveScope(ctx context.Context) app.ListingScopeDTO {
	id := h.connections.ActiveBookmarkID()
	if id == "" {
		return app.ListingScopeDTO{}
	}
	capabilities, err := h.Browse.Capabilities(ctx, id)
	if err != nil {
		return app.ListingScopeDTO{BookmarkID: id}
	}
	return capabilities.Scope
}
