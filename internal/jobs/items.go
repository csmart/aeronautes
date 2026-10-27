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
	_, err := itemJobType(ctx, tx, id, writing)
	return err
}

// itemJobType is itemJob with the job's type, for the writes whose rule
// depends on whether the job is a transfer or a deletion.
func itemJobType(ctx context.Context, tx *sql.Tx, id string, writing bool) (Type, error) {
	var state State
	var kind Type
	var version int
	err := tx.QueryRowContext(ctx, `SELECT state, type, item_records_version FROM jobs WHERE id=?`, id).Scan(&state, &kind, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrItemJobNotFound
	}
	if err != nil {
		return "", err
	}
	if version != 1 {
		return "", ErrItemRecordsNotCaptured
	}
	if writing && Terminal(state) {
		return "", ErrItemJobTerminal
	}
	return kind, nil
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
		kind, err := itemJobType(ctx, tx, jobID, true)
		if err != nil {
			return err
		}
		for _, item := range items {
			// A deletion member is its source alone; a transfer item has a
			// destination. The identity cannot see the job, so the write does.
			if item.deletion() != (kind == TypeDeleteSelected) {
				return ErrInvalidItemRecord
			}
			if kind == TypeDeleteSelected && item.Source.Kind != ItemSwift {
				// What a selected-set deletion deletes is a Swift object the
				// operator selected (ADR 0093 decision 4); a local path is not one.
				return ErrInvalidItemRecord
			}
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
		if err := transferItemJob(ctx, tx, jobID); err != nil {
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
		if err := transferItemJob(ctx, tx, jobID); err != nil {
			return err
		}
		if identity.deletion() {
			return ErrInvalidItemRecord
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

// transferItemJob admits a write that belongs to the transfer phases: a
// deletion member records its one outcome through RecordDeletion, so the
// successor it observed is saved in the same transaction.
func transferItemJob(ctx context.Context, tx *sql.Tx, jobID string) error {
	kind, err := itemJobType(ctx, tx, jobID, true)
	if err != nil {
		return err
	}
	if kind == TypeDeleteSelected {
		return ErrInvalidItemRecord
	}
	return nil
}

func appendObservation(ctx context.Context, tx *sql.Tx, item ItemRecord, phase string, observation ItemObservation) error {
	valid := observation.valid(phase, item.Admitted)
	if item.deletion() {
		valid = observation.validDeletion(phase, item.Admitted)
	}
	if !valid {
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
	planned, admitted, listed_bytes, publication_required, created_at, issued_at FROM job_items`

func scanItem(scan func(...any) error) (ItemRecord, error) {
	var item ItemRecord
	var source, destination, created string
	var staging, issued sql.NullString
	if err := scan(&item.ID, &item.JobID, &item.Key, &source, &destination, &staging,
		&item.Planned, &item.Admitted, &item.ListedBytes, &item.PublicationRequired, &created, &issued); err != nil {
		return ItemRecord{}, err
	}
	if issued.Valid {
		at, err := time.Parse(time.RFC3339Nano, issued.String)
		if err != nil {
			return ItemRecord{}, ErrInvalidItemRecord
		}
		item.IssuedAt = at
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
	// A deletion member's destination is stored absent, as a zero location;
	// whether an absence is allowed where this one sits is the identity's
	// rule, checked by the caller after both halves are decoded.
	if decoder.Decode(new(any)) != io.EOF || (*location != ItemLocation{} && !location.valid()) {
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
