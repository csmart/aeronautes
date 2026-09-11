package app

import (
	"fmt"

	"github.com/csmart/aeronautes/internal/deletion"
)

// Keep all callers' evidence and qualification together: the exceptional
// records describe admitted attempts and cannot supply distinct totals.
func incompleteDeletionReason(result *deletion.Result) string {
	reason := fmt.Sprintf("deletion accounting is incomplete: final failure and unresolved totals are unavailable; some requests may have taken effect; last confirmed progress: %d credited deletions, %d reported failures, %d unresolved names",
		result.Deleted, result.Failed, result.Unresolved)
	path, err := retainDeletionAttempts(result.UnindexedAttempts)
	switch {
	case err != nil:
		reason += "; issued identities could not all be retained: " + err.Error()
	case path == "":
		reason += "; issued identities could not all be retained"
	default:
		reason += "; admitted-attempt records (not distinct totals): " + path
	}
	return reason
}
