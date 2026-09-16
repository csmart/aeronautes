package app

import (
	"context"
	"fmt"

	"github.com/csmart/aeronautes/internal/audit"
)

// RecentFor is the bound §50 newest-events read (ADR 0059). It returns
// only the named bookmark's events, each restating that bookmark.
// The unscoped Recent method still exists on this type but the store
// refuses it, so it cannot answer across accounts.
func (s *AuditService) RecentFor(ctx context.Context, bookmarkID string, limit int) ([]AuditEventDTO, error) {
	if bookmarkID == "" {
		return nil, fmt.Errorf("%w: name the bookmark the read is for", ErrAccountScopeChanged)
	}
	events, err := s.audit.List(ctx, audit.Query{BookmarkID: bookmarkID, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]AuditEventDTO, 0, len(events))
	for _, e := range events {
		dto := auditEventDTO(e)
		dto.BookmarkID = e.BookmarkID
		out = append(out, dto)
	}
	return out, nil
}
