package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

func (h *Harness) ConnectBookmark(ctx context.Context, bookmarkID string) (app.AccountDTO, *Receipt) {
	account, err := h.Browse.Connect(ctx, bookmarkID)
	return account, &Receipt{h: h, action: "BrowseService.Connect", err: err}
}
