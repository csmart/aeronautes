package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrInvalidHistoryPage = errors.New("invalid job history page or cursor")
	ErrHistoryUnavailable = errors.New("job history is unavailable")
)

type HistoryPage struct {
	Jobs       []Job
	NextCursor string
}

// History uses a job's immutable creation order so new jobs cannot shift an older page.
func (s *Store) History(ctx context.Context, bookmarkID, cursor string, limit int) (HistoryPage, error) {
	if limit < 0 || limit > 100 {
		return HistoryPage{}, ErrInvalidHistoryPage
	}
	if limit == 0 {
		limit = 50
	}
	if err := s.Available(); err != nil {
		return HistoryPage{}, ErrHistoryUnavailable
	}
	tx, err := s.db.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HistoryPage{}, ErrHistoryUnavailable
	}
	defer tx.Rollback()

	filter := " WHERE 1=1"
	args := []any{}
	if bookmarkID != "" {
		filter += " AND (source_bookmark_id=? OR dest_bookmark_id=?)"
		args = append(args, bookmarkID, bookmarkID)
	}
	if cursor != "" {
		var created string
		cursorArgs := append(append([]any{}, args...), cursor)
		err := tx.QueryRowContext(ctx, "SELECT created_at FROM jobs"+filter+" AND id=?", cursorArgs...).Scan(&created)
		if errors.Is(err, sql.ErrNoRows) {
			return HistoryPage{}, ErrInvalidHistoryPage
		}
		if err != nil {
			return HistoryPage{}, ErrHistoryUnavailable
		}
		if _, err := time.Parse(time.RFC3339, created); err != nil {
			return HistoryPage{}, ErrHistoryUnavailable
		}
		filter += " AND (created_at<? OR (created_at=? AND id<?))"
		args = append(args, created, created, cursor)
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, selectJob+filter+" ORDER BY created_at DESC, id DESC LIMIT ?", args...)
	if err != nil {
		return HistoryPage{}, ErrHistoryUnavailable
	}
	defer rows.Close()
	page := HistoryPage{Jobs: []Job{}}
	for rows.Next() {
		job, err := scanJob(rows.Scan)
		if err != nil {
			return HistoryPage{}, ErrHistoryUnavailable
		}
		page.Jobs = append(page.Jobs, job)
	}
	if err := rows.Err(); err != nil {
		return HistoryPage{}, ErrHistoryUnavailable
	}
	if err := rows.Close(); err != nil {
		return HistoryPage{}, ErrHistoryUnavailable
	}
	if err := tx.Commit(); err != nil {
		return HistoryPage{}, ErrHistoryUnavailable
	}
	if err := s.Available(); err != nil {
		return HistoryPage{}, ErrHistoryUnavailable
	}
	if len(page.Jobs) > limit {
		page.Jobs = page.Jobs[:limit]
		page.NextCursor = page.Jobs[limit-1].ID
	}
	return page, nil
}
