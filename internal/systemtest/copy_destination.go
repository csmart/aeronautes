package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

// DestinationContainers wraps the ADR 0115 destination listing.
func (h *Harness) DestinationContainers(ctx context.Context, bookmarkID string) (app.DestinationContainersDTO, *Receipt) {
	page, err := h.Transfer.DestinationContainers(ctx, bookmarkID)
	return page, &Receipt{h: h, action: "TransferService.DestinationContainers", err: err}
}

// CreateDestinationContainer wraps the ADR 0115 destination create so a test
// credits its happy and refusal scenarios: a create that returns credits
// happy as "applied", a refused one credits refusal from its error.
func (h *Harness) CreateDestinationContainer(ctx context.Context, missing app.DestinationMissingDTO) (app.ContainerDetailDTO, *Receipt) {
	detail, err := h.Mutate.CreateDestinationContainer(ctx, missing)
	outcome := ""
	if err == nil {
		outcome = "applied"
	}
	return detail, &Receipt{h: h, action: "MutateService.CreateDestinationContainer", outcome: outcome, err: err}
}
