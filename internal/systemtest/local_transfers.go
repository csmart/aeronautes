package systemtest

import (
	"context"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/transfer/local"
)

func (h *Harness) PreflightUpload(ctx context.Context, request app.UploadRequestDTO) (*local.UploadPreflight, *Receipt) {
	plan, err := h.Transfer.PreflightUpload(ctx, request)
	return plan, &Receipt{h: h, action: "TransferService.PreflightUpload", err: err}
}

func (h *Harness) PreflightDownload(ctx context.Context, request app.DownloadRequestDTO) (*local.DownloadPreflight, *Receipt) {
	plan, err := h.Transfer.PreflightDownload(ctx, request)
	return plan, &Receipt{h: h, action: "TransferService.PreflightDownload", err: err}
}

func (h *Harness) StartUpload(ctx context.Context, request app.UploadRequestDTO) *Receipt {
	id, err := h.Transfer.StartUpload(ctx, request)
	return &Receipt{h: h, action: "TransferService.StartUpload", jobID: id, err: err}
}

func (h *Harness) StartDownload(ctx context.Context, request app.DownloadRequestDTO) *Receipt {
	id, err := h.Transfer.StartDownload(ctx, request)
	return &Receipt{h: h, action: "TransferService.StartDownload", jobID: id, err: err}
}
