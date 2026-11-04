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

// ImportSettings wraps the ADR 0098 settings restore so a test credits its
// happy and refusal scenarios through the Receipt API — a successful restore
// credits happy as "applied", a refused one credits refusal from the error.
func (h *Harness) ImportSettings(ctx context.Context) (*app.RestoreResultDTO, *Receipt) {
	result, err := h.Backup.ImportSettings(ctx)
	outcome := ""
	if err == nil && result != nil {
		outcome = "applied"
	}
	return result, &Receipt{h: h, action: "BackupService.ImportSettings", outcome: outcome, err: err}
}
