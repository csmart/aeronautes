package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) readItems(ctx context.Context, jobID string, read func(*sql.Tx) error) error {
	if err := s.Available(); err != nil {
		return err
	}
	tx, err := s.db.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ErrItemRecordsUnavailable
	}
	defer tx.Rollback()
	if err := itemJob(ctx, tx, jobID, false); err != nil {
		if errors.Is(err, ErrItemJobNotFound) || errors.Is(err, ErrItemRecordsNotCaptured) {
			return err
		}
		return ErrItemRecordsUnavailable
	}
	if err := read(tx); err != nil {
		if errors.Is(err, ErrInvalidItemPage) {
			return err
		}
		return ErrItemRecordsUnavailable
	}
	if err := tx.Commit(); err != nil {
		return ErrItemRecordsUnavailable
	}
	return s.Available()
}

func (s *Store) ReadItems(ctx context.Context, jobID string, cursor int64, limit int) (ItemPage, error) {
	if cursor < 0 || limit < 0 || limit > 200 {
		return ItemPage{}, ErrInvalidItemPage
	}
	if limit == 0 {
		limit = 100
	}
	page := ItemPage{Items: []ItemRecord{}}
	err := s.readItems(ctx, jobID, func(tx *sql.Tx) error {
		if cursor != 0 {
			var owner string
			err := tx.QueryRowContext(ctx, `SELECT job_id FROM job_items WHERE id=?`, cursor).Scan(&owner)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != jobID) {
				return ErrInvalidItemPage
			}
			if err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, selectItem+` WHERE job_id=? AND id>? ORDER BY id LIMIT ?`, jobID, cursor, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanItem(rows.Scan)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(page.Items) > limit {
			page.Items = page.Items[:limit]
			page.NextCursor = page.Items[limit-1].ID
		}
		if len(page.Items) == 0 {
			return nil
		}
		return readObservations(ctx, tx, jobID, cursor, page.Items)
	})
	if err != nil {
		return ItemPage{}, err
	}
	return page, nil
}

func readObservations(ctx context.Context, tx *sql.Tx, jobID string, cursor int64, items []ItemRecord) error {
	byID := make(map[int64]*ItemRecord, len(items))
	for i := range items {
		byID[items[i].ID] = &items[i]
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.item_id, o.phase, o.result, o.effect,
		o.diagnostic_stage, o.diagnostic_code, o.http_status, o.copied_bytes, o.created_at
		FROM job_item_observations o JOIN job_items i ON i.id=o.item_id
		WHERE i.job_id=? AND i.id>? AND i.id<=? ORDER BY i.id, o.phase`, jobID, cursor, items[len(items)-1].ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var phase, created string
		var observation RecordedObservation
		err := rows.Scan(&id, &phase, &observation.Result, &observation.Effect, &observation.Diagnostic.Stage,
			&observation.Diagnostic.Code, &observation.Diagnostic.HTTPStatus, &observation.CopiedBytes, &created)
		if err != nil {
			return err
		}
		item := byID[id]
		if item == nil || !observation.valid(phase, item.Admitted) {
			return ErrInvalidItemRecord
		}
		observation.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return err
		}
		switch phase {
		case "execution":
			item.Execution = &observation
		case "verification":
			item.Verification = &observation
		}
	}
	return rows.Err()
}

func (s *Store) ReadItemTotals(ctx context.Context, jobID string) (ItemTotals, error) {
	var totals ItemTotals
	err := s.readItems(ctx, jobID, func(tx *sql.Tx) error {
		var err error
		totals, err = itemTotals(ctx, tx, jobID)
		return err
	})
	if err != nil {
		return ItemTotals{}, err
	}
	return totals, nil
}

func itemTotals(ctx context.Context, tx *sql.Tx, jobID string) (ItemTotals, error) {
	var totals ItemTotals
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(i.planned),0), COALESCE(SUM(i.admitted),0),
		COALESCE(SUM(CASE WHEN e.result='failed' OR v.result='failed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN e.result='conflict' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN e.result='refused' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN i.admitted=1 AND (e.item_id IS NULL OR e.effect='unresolved') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN e.result='copied' AND (v.item_id IS NULL OR v.result!='verified') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN e.diagnostic_code!='' OR v.diagnostic_code!='' THEN 1 ELSE 0 END),0)
		FROM job_items i
		LEFT JOIN job_item_observations e ON e.item_id=i.id AND e.phase='execution'
		LEFT JOIN job_item_observations v ON v.item_id=i.id AND v.phase='verification'
		WHERE i.job_id=?`, jobID).Scan(&totals.Items, &totals.Planned, &totals.Admitted, &totals.Failed,
		&totals.Conflicts, &totals.Refused, &totals.Unresolved, &totals.Unverified, &totals.Problems)
	return totals, err
}

func (t ItemTotals) permitsComplete() bool {
	return t.Failed == 0 && t.Conflicts == 0 && t.Refused == 0 && t.Unresolved == 0 && t.Unverified == 0 && t.Problems == 0
}
