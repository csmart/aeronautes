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
// This refusal is deliberately NOT routed through the v1 capability gate,
// although the gate's table classifies Mirror as deferred (ADR 0052
// decision 5). ADR 0020 is the reason Mirror refuses, and it must keep
// refusing when the v1 gate is lifted; making the gate the only check
// would silently enable Mirror the day somebody narrows that table.
func (s *TransferService) StartMirror(ctx context.Context, req MirrorRequestDTO) (string, error) {
	return "", errors.New("Mirror is unavailable while concurrent changes cannot be protected")
}
