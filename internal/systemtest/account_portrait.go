package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

func (h *Harness) ReadAccountPortrait(ctx context.Context, bookmarkID string) (app.AccountDTO, *Receipt) {
	portrait, err := h.Browse.Account(ctx, bookmarkID)
	return portrait, &Receipt{h: h, action: "BrowseService.Account", err: err}
}

func (h *Harness) ReadCapabilities(ctx context.Context, bookmarkID string) (app.CapabilitiesDTO, *Receipt) {
	caps, err := h.Browse.Capabilities(ctx, bookmarkID)
	return caps, &Receipt{h: h, action: "BrowseService.Capabilities", err: err}
}
