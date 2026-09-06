package app

import (
	"context"
	"errors"
)

// MirrorRequestDTO preserves the bridge shape for callers that still
// request Mirror while execution is unavailable under ADR 0020.
type MirrorRequestDTO struct {
	CopyRequestDTO
	Confirm string `json:"confirm"`
}

// StartMirror refuses because a fresh HEAD cannot protect a later
// deletion from an external writer. ADR 0020 requires refusing the
// entire operation before any transfer or deletion starts.
func (s *TransferService) StartMirror(ctx context.Context, req MirrorRequestDTO) (string, error) {
	return "", errors.New("Mirror is unavailable while concurrent changes cannot be protected")
}
