package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Completion facts may still be saved after a later admission has latched
// the store. They cannot authorise another transfer effect (ADR 0061).
func (s *Store) RecordPublication(ctx context.Context, jobID string, identity ItemIdentity, observation ItemObservation) (err error) {
	defer s.itemWriteFailed(jobID, &err)
	if !identity.valid() || !observation.validPublication() {
		return ErrInvalidItemRecord
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := itemJob(ctx, tx, jobID, true); err != nil {
			return err
		}
		if err := publicationJob(ctx, tx, jobID); err != nil {
			return err
		}
		item, err := scanItem(tx.QueryRowContext(ctx, selectItem+` WHERE job_id=? AND item_key=?`, jobID, identity.Key).Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return err
		}
		if item.ItemIdentity != identity {
			return ErrItemRecordConflict
		}
		for _, phase := range []string{"execution", "verification"} {
			observed, err := scanObservation(tx.QueryRowContext(ctx, selectObservation+` WHERE item_id=? AND phase=?`, item.ID, phase).Scan)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrInvalidItemRecord
			}
			if err != nil {
				return err
			}
			if !observed.valid(phase, item.Admitted) {
				return ErrInvalidItemRecord
			}
			if phase == "execution" {
				item.Execution = &observed
			} else {
				item.Verification = &observed
			}
		}
		if !publicationReady(item) {
			return ErrInvalidItemRecord
		}
		prior, err := scanObservation(tx.QueryRowContext(ctx, selectPublication+` WHERE item_id=?`, item.ID).Scan)
		if err == nil {
			if prior.ItemObservation == observation {
				return nil
			}
			return ErrItemRecordConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO job_item_publications
			(item_id, result, effect, diagnostic_stage, diagnostic_code, http_status, copied_bytes, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, observation.Result, observation.Effect,
			observation.Diagnostic.Stage, observation.Diagnostic.Code, observation.Diagnostic.HTTPStatus,
			observation.CopiedBytes, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}

func publicationJob(ctx context.Context, tx *sql.Tx, jobID string) error {
	var kind Type
	if err := tx.QueryRowContext(ctx, `SELECT type FROM jobs WHERE id=?`, jobID).Scan(&kind); err != nil {
		return err
	}
	if kind != TypeDownload {
		return ErrInvalidItemRecord
	}
	return nil
}

func publicationReady(item ItemRecord) bool {
	return item.PublicationRequired && item.Execution != nil && item.Execution.Result == ItemCopied &&
		item.Verification != nil && item.Verification.Result == ItemVerified
}

const selectPublication = `SELECT ` + observationColumns + ` FROM job_item_publications`

func readPublications(ctx context.Context, tx *sql.Tx, jobID string, cursor int64, items []ItemRecord) error {
	byID := make(map[int64]*ItemRecord, len(items))
	for i := range items {
		byID[items[i].ID] = &items[i]
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.item_id, p.result, p.effect,
		p.diagnostic_stage, p.diagnostic_code, p.http_status, p.copied_bytes, p.created_at
		FROM job_item_publications p JOIN job_items i ON i.id=p.item_id
		WHERE i.job_id=? AND i.id>? AND i.id<=? ORDER BY i.id`, jobID, cursor, items[len(items)-1].ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		observation, err := scanObservation(func(values ...any) error {
			return rows.Scan(append([]any{&id}, values...)...)
		})
		if err != nil {
			return err
		}
		item := byID[id]
		if item == nil || !publicationReady(*item) || !observation.validPublication() {
			return ErrInvalidItemRecord
		}
		item.Publication = &observation
	}
	return rows.Err()
}
