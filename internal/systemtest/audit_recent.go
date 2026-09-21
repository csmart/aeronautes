package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

func (h *Harness) AuditRecent(ctx context.Context, limit int) ([]app.AuditEventDTO, *Receipt) {
	events, err := h.Audit.Recent(ctx, limit)
	return events, &Receipt{h: h, action: "AuditService.Recent", err: err}
}
