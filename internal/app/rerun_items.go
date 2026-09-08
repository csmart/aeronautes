package app

import (
	"context"
	"errors"

	"github.com/csmart/aeronautes/internal/jobs"
)

var (
	errRerunRecordsUnavailable = errors.New("re-run is unavailable because retained item records cannot be read; inspect retained data before starting a fresh Copy")
	errRerunRecordsNotCaptured = errors.New("re-run is unavailable because item records were not captured; inspect retained data before starting a fresh Copy")
	errRerunEffectsUnresolved  = errors.New("re-run is unavailable because prior item effects remain unresolved; inspect the retained item records before starting a fresh Copy")
	errRerunCopiesUnverified   = errors.New("re-run is unavailable because reported copies remain unverified; inspect the retained item records before starting a fresh Copy")
)

func (s *TransferService) checkRerunItems(ctx context.Context, id string) error {
	// Zero aggregate uncertainty cannot certify an unreadable identity or
	// observation. Terminal facts are immutable, so inspect every bounded page.
	var cursor int64
	for {
		page, err := s.jobs.ReadItems(ctx, id, cursor, 100)
		if errors.Is(err, jobs.ErrItemRecordsNotCaptured) {
			return errRerunRecordsNotCaptured
		}
		if err != nil {
			return errRerunRecordsUnavailable
		}
		if page.NextCursor == 0 {
			break
		}
		cursor = page.NextCursor
	}
	totals, err := s.jobs.ReadItemTotals(ctx, id)
	if err != nil {
		return errRerunRecordsUnavailable
	}
	if totals.Unresolved > 0 {
		return errRerunEffectsUnresolved
	}
	if totals.Unverified > 0 {
		return errRerunCopiesUnverified
	}
	return nil
}
