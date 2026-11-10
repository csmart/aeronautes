package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ItemBaseline is what the operator was shown at a name when they acknowledged
// replacing it (ADR 0085). It is HEAD-grade on purpose: a listing row cannot
// establish an object's class, and a dynamic manifest's row is zero-byte
// carrying the manifest object's own hash rather than a digest of what it
// assembles, so neither can identify what a replacement is about to destroy.
//
// It is not the same record as ItemPredecessor and does not replace it. That one
// is written by the route immediately before it publishes; this one is written
// when the acknowledgement is taken. The comparison ADR 0085 requires is between
// this baseline and a FRESH observation at the fixed point, never between the two
// stored records — both describe moments before the preparation that decision
// refuses to leave inside the window, so they can agree while the replacement
// lands between them.
type ItemBaseline struct {
	// Class is the large-object classification from an authoritative HEAD.
	Class string
	// Length is the logical size: a manifest's assembled size rather than its
	// stored bytes.
	Length int64
	// ETag is the entity tag the store reported, unquoted.
	ETag string
}

func (b ItemBaseline) valid() bool {
	return b.Class != "" && b.ETag != "" && b.Length >= 0
}

// RecordAcknowledgedBaseline stores what was at a name when the operator
// acknowledged replacing it. It is written before the job runs, so unlike the
// predecessor record there is nothing it has to precede; what it has to survive
// is the preparation between the acknowledgement and the publication.
//
// Recording the same baseline twice is accepted, because an acknowledgement
// re-taken for the same item that saw the same object is the same fact. A
// DIFFERENT one is refused: two acknowledgements that saw different objects are
// two decisions, and silently keeping either would leave the comparison matching
// against something the operator may not have been shown.
func (s *Store) RecordAcknowledgedBaseline(ctx context.Context, jobID string, identity ItemIdentity, baseline ItemBaseline) (err error) {
	defer s.itemWriteFailed(jobID, &err)
	if !identity.valid() || !baseline.valid() {
		return ErrInvalidItemRecord
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.Available(); err != nil {
			return err
		}
		if err := itemJob(ctx, tx, jobID, true); err != nil {
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
		prior, err := readBaseline(ctx, tx, item.ID)
		if err == nil {
			if prior == baseline {
				return nil
			}
			return ErrItemRecordConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO job_item_acknowledged_baselines
			(item_id, class, length, etag, created_at) VALUES (?, ?, ?, ?, ?)`,
			item.ID, baseline.Class, baseline.Length, baseline.ETag,
			time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}

// ReadAcknowledgedBaseline is what the operator was shown for one item. An item
// that was never admitted is ErrItemNotFound; an admitted item whose
// acknowledgement captured nothing is a zero value with ok false, which ADR 0085
// makes a refusal to overwrite rather than a licence to proceed.
func (s *Store) ReadAcknowledgedBaseline(ctx context.Context, jobID, key string) (ItemBaseline, bool, error) {
	var baseline ItemBaseline
	var missing, found bool
	err := s.readItems(ctx, jobID, func(tx *sql.Tx) error {
		item, err := scanItem(tx.QueryRowContext(ctx, selectItem+` WHERE job_id=? AND item_key=?`, jobID, key).Scan)
		if errors.Is(err, sql.ErrNoRows) {
			missing = true
			return nil
		}
		if err != nil {
			return err
		}
		baseline, err = readBaseline(ctx, tx, item.ID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			baseline = ItemBaseline{}
			return nil
		case err != nil:
			return err
		}
		found = true
		return nil
	})
	switch {
	case err != nil:
		return ItemBaseline{}, false, err
	case missing:
		return ItemBaseline{}, false, ErrItemNotFound
	}
	return baseline, found, nil
}

func readBaseline(ctx context.Context, tx *sql.Tx, itemID int64) (ItemBaseline, error) {
	var baseline ItemBaseline
	err := tx.QueryRowContext(ctx, `SELECT class, length, etag
		FROM job_item_acknowledged_baselines WHERE item_id=?`, itemID).
		Scan(&baseline.Class, &baseline.Length, &baseline.ETag)
	return baseline, err
}
