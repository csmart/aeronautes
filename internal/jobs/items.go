package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

func itemError(err error) error {
	for _, known := range []error{ErrItemRecordsNotCaptured, ErrInvalidItemRecord, ErrItemRecordConflict,
		ErrItemJobNotFound, ErrItemNotFound, ErrItemJobTerminal, ErrItemCompletionRequired, ErrIncompleteItemRecords, ErrInvalidItemPage} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrItemStorage
}

func (s *Store) itemWriteFailed(err *error) {
	if *err == nil {
		return
	}
	s.healthMu.Lock()
	// A later item error must not replace an earlier terminal-save diagnostic.
	if s.unavailable == nil {
		s.unavailable = ErrItemRecordsUnavailable
	}
	visible := s.unavailable
	s.healthMu.Unlock()
	*err = errors.Join(visible, itemError(*err))
}

func itemJob(ctx context.Context, tx *sql.Tx, id string, writing bool) error {
	var state State
	var version int
	err := tx.QueryRowContext(ctx, `SELECT state, item_records_version FROM jobs WHERE id=?`, id).Scan(&state, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrItemJobNotFound
	}
	if err != nil {
		return err
	}
	if version != 1 {
		return ErrItemRecordsNotCaptured
	}
	if writing && Terminal(state) {
		return ErrItemJobTerminal
	}
	return nil
}

// AdmitItems rejects repeated intent rather than returning permission to retry.
func (s *Store) AdmitItems(ctx context.Context, jobID string, items []ItemIntent) (err error) {
	if err := s.Available(); err != nil {
		return err
	}
	defer s.itemWriteFailed(&err)
	if len(items) < 1 || len(items) > 500 {
		return ErrInvalidItemRecord
	}
	for _, item := range items {
		if !item.valid() {
			return ErrInvalidItemRecord
		}
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.Available(); err != nil {
			return err
		}
		if err := itemJob(ctx, tx, jobID, true); err != nil {
			return err
		}
		for _, item := range items {
			if _, err := insertItem(ctx, tx, jobID, item); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertItem(ctx context.Context, tx *sql.Tx, jobID string, item ItemIntent) (int64, error) {
	if item.PublicationRequired {
		if err := publicationJob(ctx, tx, jobID); err != nil {
			return 0, err
		}
	}
	source, err := json.Marshal(item.Source)
	if err != nil {
		return 0, err
	}
	destination, err := json.Marshal(item.Destination)
	if err != nil {
		return 0, err
	}
	var staging any
	if item.Staging != nil {
		encoded, err := json.Marshal(item.Staging)
		if err != nil {
			return 0, err
		}
		staging = string(encoded)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO job_items (job_id, item_key, source_json, destination_json, staging_json,
			planned, admitted, listed_bytes, publication_required, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, item.Key, string(source), string(destination), staging, item.Planned, item.Admitted,
		item.ListedBytes, item.PublicationRequired, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) RecordExecution(ctx context.Context, jobID, key string, observation ItemObservation) (err error) {
	defer s.itemWriteFailed(&err)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := itemJob(ctx, tx, jobID, true); err != nil {
			return err
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

// Comparison may discover a name outside execution; it cannot authorise a write.
func (s *Store) RecordComparison(ctx context.Context, jobID string, identity ItemIdentity, observation ItemObservation) (err error) {
	defer s.itemWriteFailed(&err)
	if !identity.valid() || !observation.valid("verification", false) {
		return ErrInvalidItemRecord
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := itemJob(ctx, tx, jobID, true); err != nil {
			return err
		}
		item, err := scanItem(tx.QueryRowContext(ctx, selectItem+` WHERE job_id=? AND item_key=?`, jobID, identity.Key).Scan)
		if errors.Is(err, sql.ErrNoRows) {
			item = ItemRecord{ItemIntent: ItemIntent{ItemIdentity: identity}, JobID: jobID}
			item.ID, err = insertItem(ctx, tx, jobID, item.ItemIntent)
		}
		if err != nil {
			return err
		}
		if item.ItemIdentity != identity {
			return ErrItemRecordConflict
		}
		return appendObservation(ctx, tx, item, "verification", observation)
	})
}

func appendObservation(ctx context.Context, tx *sql.Tx, item ItemRecord, phase string, observation ItemObservation) error {
	if !observation.valid(phase, item.Admitted) {
		return ErrInvalidItemRecord
	}
	prior, err := scanObservation(tx.QueryRowContext(ctx, selectObservation+` WHERE item_id=? AND phase=?`, item.ID, phase).Scan)
	if err == nil {
		if prior.ItemObservation == observation {
			return nil
		}
		return ErrItemRecordConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var other ItemResult
	err = tx.QueryRowContext(ctx, `SELECT result FROM job_item_observations WHERE item_id=? AND phase!=?`, item.ID, phase).Scan(&other)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if (phase == "execution" && observation.Result == ItemRefused && other == ItemVerified) ||
		(phase == "verification" && observation.Result == ItemVerified && other == ItemRefused) {
		return ErrItemRecordConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO job_item_observations
		(item_id, phase, result, effect, diagnostic_stage, diagnostic_code, http_status, copied_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, phase, observation.Result, observation.Effect,
		observation.Diagnostic.Stage, observation.Diagnostic.Code, observation.Diagnostic.HTTPStatus,
		observation.CopiedBytes, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

const selectItem = `SELECT id, job_id, item_key, source_json, destination_json, staging_json,
	planned, admitted, listed_bytes, publication_required, created_at FROM job_items`

func scanItem(scan func(...any) error) (ItemRecord, error) {
	var item ItemRecord
	var source, destination, created string
	var staging sql.NullString
	if err := scan(&item.ID, &item.JobID, &item.Key, &source, &destination, &staging,
		&item.Planned, &item.Admitted, &item.ListedBytes, &item.PublicationRequired, &created); err != nil {
		return ItemRecord{}, err
	}
	if err := decodeLocation(source, &item.Source); err != nil {
		return ItemRecord{}, err
	}
	if err := decodeLocation(destination, &item.Destination); err != nil {
		return ItemRecord{}, err
	}
	if staging.Valid {
		item.Staging = new(ItemLocation)
		if err := decodeLocation(staging.String, item.Staging); err != nil {
			return ItemRecord{}, err
		}
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil || !item.ItemIntent.valid() {
		return ItemRecord{}, ErrInvalidItemRecord
	}
	return item, nil
}

func decodeLocation(encoded string, location *ItemLocation) error {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(location); err != nil {
		return ErrInvalidItemRecord
	}
	if decoder.Decode(new(any)) != io.EOF || !location.valid() {
		return ErrInvalidItemRecord
	}
	return nil
}

const observationColumns = `result, effect, diagnostic_stage, diagnostic_code, http_status, copied_bytes, created_at`
const selectObservation = `SELECT ` + observationColumns + ` FROM job_item_observations`

func scanObservation(scan func(...any) error) (RecordedObservation, error) {
	var observation RecordedObservation
	var created string
	err := scan(&observation.Result, &observation.Effect, &observation.Diagnostic.Stage,
		&observation.Diagnostic.Code, &observation.Diagnostic.HTTPStatus, &observation.CopiedBytes, &created)
	if err != nil {
		return RecordedObservation{}, err
	}
	observation.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return observation, err
}
