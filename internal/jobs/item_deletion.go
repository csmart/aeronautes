package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MarkIssued records that a deletion member is about to be requested. It is
// written before the member's final HEAD so that nothing of ours, local or
// remote, runs between that HEAD and the DELETE (ADR 0085, ADR 0093 decision
// 4), and it proves only that: whether a DELETE was actually sent is what the
// member's outcome says. A member that already has an outcome cannot be
// issued, because its request is over; marking an issued member again is the
// same fact.
func (s *Store) MarkIssued(ctx context.Context, jobID, key string) (err error) {
	defer s.itemWriteFailed(jobID, &err)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.Available(); err != nil {
			return err
		}
		kind, err := itemJobType(ctx, tx, jobID, true)
		if err != nil {
			return err
		}
		if !kind.deletion() {
			return ErrInvalidItemRecord
		}
		item, err := scanItem(tx.QueryRowContext(ctx, selectItem+` WHERE job_id=? AND item_key=?`, jobID, key).Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return err
		}
		if !item.IssuedAt.IsZero() {
			return nil
		}
		var outcomes int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_item_observations WHERE item_id=? AND phase='execution'`, item.ID).Scan(&outcomes); err != nil {
			return err
		}
		if outcomes != 0 {
			return ErrItemRecordConflict
		}
		_, err = tx.ExecContext(ctx, `UPDATE job_items SET issued_at=? WHERE id=? AND issued_at IS NULL`,
			time.Now().UTC().Format(time.RFC3339Nano), item.ID)
		return err
	})
}

// RecordDeletion records a deletion member's one outcome, when its request
// settles. The same outcome again is idempotent; a different one is a
// conflict.
func (s *Store) RecordDeletion(ctx context.Context, jobID, key string, observation ItemObservation) (err error) {
	defer s.itemWriteFailed(jobID, &err)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.Available(); err != nil {
			return err
		}
		kind, err := itemJobType(ctx, tx, jobID, true)
		if err != nil {
			return err
		}
		if !kind.deletion() {
			return ErrInvalidItemRecord
		}
		item, err := scanItem(tx.QueryRowContext(ctx, selectItem+` WHERE job_id=? AND item_key=?`, jobID, key).Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return err
		}
		return appendObservation(ctx, tx, item, "execution", observation)
	})
}
