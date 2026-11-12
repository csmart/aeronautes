package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
)

// SaveImport wraps the ADR 0114 save so a test credits its happy and
// refusal scenarios through the Receipt API: a save that returns credits
// happy as "applied", a refused one credits refusal from its error.
func (h *Harness) SaveImport(ctx context.Context, token string, chosen []int, verify bool) (app.ImportResultDTO, *Receipt) {
	result, err := h.Import.SaveImport(ctx, token, chosen, verify)
	outcome := ""
	if err == nil {
		outcome = "applied"
	}
	return result, &Receipt{h: h, action: "BookmarkImportService.SaveImport", outcome: outcome, err: err}
}

// ImportStatus wraps the ADR 0114 status read.
func (h *Harness) ImportStatus(token string) (app.ImportResultDTO, *Receipt) {
	result, err := h.Import.ImportStatus(token)
	return result, &Receipt{h: h, action: "BookmarkImportService.ImportStatus", err: err}
}
