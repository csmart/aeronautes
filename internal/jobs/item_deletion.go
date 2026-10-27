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
	defer s.itemWriteFailed(&err)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.Available(); err != nil {
			return err
		}
		kind, err := itemJobType(ctx, tx, jobID, true)
		if err != nil {
			return err
		}
		if kind != TypeDeleteSelected {
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

// RecordDeletion records a deletion member's one outcome, when its verifying
// read settles, together with what that read observed at the name. The two
// are one transaction because a successor without its outcome, or an outcome
// without the successor it was decided on, would be half a fact. The same
// outcome and observation again is idempotent; a different one is a
// conflict. Only a deleted or an unverified member can have observed a
// successor: a refused, conflicting or failed member sent nothing that could
// have been followed by one.
func (s *Store) RecordDeletion(ctx context.Context, jobID, key string, observation ItemObservation, successor *ItemSuccessor) (err error) {
	defer s.itemWriteFailed(&err)
	if successor != nil {
		if !successor.valid() || (observation.Result != ItemDeleted && observation.Result != ItemUnverified) {
			return ErrInvalidItemRecord
		}
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.Available(); err != nil {
			return err
		}
		kind, err := itemJobType(ctx, tx, jobID, true)
		if err != nil {
			return err
		}
		if kind != TypeDeleteSelected {
			return ErrInvalidItemRecord
		}
		item, err := scanItem(tx.QueryRowContext(ctx, selectItem+` WHERE job_id=? AND item_key=?`, jobID, key).Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return err
		}
		var recorded int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_item_observations WHERE item_id=? AND phase='execution'`, item.ID).Scan(&recorded); err != nil {
			return err
		}
		if err := appendObservation(ctx, tx, item, "execution", observation); err != nil {
			return err
		}
		prior, err := readSuccessor(ctx, tx, item.ID)
		switch {
		case err == nil && successor == nil:
			return ErrItemRecordConflict
		case err == nil:
			if prior.sameAs(*successor) {
				return nil
			}
			return ErrItemRecordConflict
		case !errors.Is(err, sql.ErrNoRows):
			return err
		case successor == nil:
			return nil
		case recorded != 0:
			// The outcome was recorded with nothing observed; a successor
			// arriving afterwards would be another observation attached to
			// the earlier attempt, so the pair is immutable as a whole.
			return ErrItemRecordConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO job_item_successors (item_id, present, class, length, etag, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, item.ID, successor.Present, successor.Class, successor.Length, successor.ETag,
			time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}

func readSuccessor(ctx context.Context, tx *sql.Tx, itemID int64) (ItemSuccessor, error) {
	var observed ItemSuccessor
	var created string
	err := tx.QueryRowContext(ctx, `SELECT present, class, length, etag, created_at FROM job_item_successors WHERE item_id=?`, itemID).
		Scan(&observed.Present, &observed.Class, &observed.Length, &observed.ETag, &created)
	if err != nil {
		return ItemSuccessor{}, err
	}
	observed.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return ItemSuccessor{}, err
	}
	return observed, nil
}

// readSuccessors fills a page's deletion members with what their verifying
// reads observed.
func readSuccessors(ctx context.Context, tx *sql.Tx, jobID string, cursor int64, items []ItemRecord) error {
	byID := make(map[int64]*ItemRecord, len(items))
	for i := range items {
		byID[items[i].ID] = &items[i]
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.item_id, s.present, s.class, s.length, s.etag, s.created_at
		FROM job_item_successors s JOIN job_items i ON i.id=s.item_id
		WHERE i.job_id=? AND i.id>? AND i.id<=? ORDER BY i.id`, jobID, cursor, items[len(items)-1].ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var observed ItemSuccessor
		var created string
		if err := rows.Scan(&id, &observed.Present, &observed.Class, &observed.Length, &observed.ETag, &created); err != nil {
			return err
		}
		observed.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return err
		}
		item := byID[id]
		if item == nil || !observed.valid() {
			return ErrInvalidItemRecord
		}
		item.Successor = &observed
	}
	return rows.Err()
}
