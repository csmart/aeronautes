package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

func (h *Harness) AuditRecent(ctx context.Context, limit int) ([]app.AuditEventDTO, *Receipt) {
	events, err := h.Audit.Recent(ctx, limit)
	return events, &Receipt{h: h, action: "AuditService.Recent", err: err}
}

// ExportAudit wraps the §51 archive so a test credits its happy and
// refusal scenarios through the Receipt API.
func (h *Harness) ExportAudit(ctx context.Context, q app.AuditQueryDTO) (*app.ExportResultDTO, *Receipt) {
	result, err := h.Audit.ExportAudit(ctx, q)
	return result, &Receipt{h: h, action: "AuditService.ExportAudit", err: err}
}
