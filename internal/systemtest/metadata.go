package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

func (h *Harness) UpdateContainer(ctx context.Context, name, baseFingerprint string, edit app.ContainerEditDTO) (app.MutationResultDTO, *Receipt) {
	result, err := h.Mutate.UpdateContainer(ctx, name, baseFingerprint, edit)
	return result, &Receipt{h: h, action: "MutateService.UpdateContainer", outcome: result.Outcome, err: err}
}

func (h *Harness) CreateContainer(ctx context.Context, create app.ContainerCreateDTO) (app.ContainerDetailDTO, *Receipt) {
	result, err := h.Mutate.CreateContainer(ctx, create)
	return result, &Receipt{h: h, action: "MutateService.CreateContainer", err: err}
}
