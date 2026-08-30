package copy

import (
	"fmt"
	"strings"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// ResultFailure inspects a finished child's result and returns the
// typed failure it represents, or nil for a healthy exit. The engine's
// exit status is authoritative FAILURE input (a nonzero exit, a
// cancellation, or a truncated log stream means the child did not
// finish its contract) — it is never sufficient SUCCESS proof; per-name
// verification still runs on healthy exits (§14.3).
func ResultFailure(result *rclone.Result) error {
	switch {
	case result == nil:
		return fmt.Errorf("engine returned no result")
	case result.Cancelled:
		return fmt.Errorf("engine child cancelled")
	case result.LogStreamErr != "":
		return fmt.Errorf("engine log stream incomplete (%s): segment capture cannot be trusted", result.LogStreamErr)
	case result.ExitCode != 0:
		detail := ""
		if len(result.Errors) > 0 {
			detail = ": " + result.Errors[0]
		}
		return fmt.Errorf("engine exit %d%s", result.ExitCode, detail)
	}
	return nil
}

// IsAuthFailure classifies a failed result as authentication-shaped —
// the one failure class worth one fresh child, because remotes resolve
// per spawn with refreshed tokens (§26.2, K-XFER-001).
func IsAuthFailure(result *rclone.Result) bool {
	if result == nil {
		return false
	}
	for _, msg := range result.Errors {
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized") ||
			strings.Contains(lower, "authorization") || strings.Contains(lower, "authentication") {
			return true
		}
	}
	return false
}
