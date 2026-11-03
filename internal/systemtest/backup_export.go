package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

// ExportSettings wraps the ADR 0098 settings backup so a test credits its
// happy and refusal scenarios through the Receipt API.
func (h *Harness) ExportSettings(ctx context.Context) (*app.BackupResultDTO, *Receipt) {
	result, err := h.Backup.ExportSettings(ctx)
	return result, &Receipt{h: h, action: "BackupService.ExportSettings", err: err}
}
