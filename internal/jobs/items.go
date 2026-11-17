package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/csmart/aeronautes/internal/applog"
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

func (s *Store) itemWriteFailed(jobID string, err *error) {
	if *err == nil {
		return
	}
	if s.Available() == nil {
		// What is returned below is reduced to a fixed sentence, so the cause
		// of the session-wide stop is logged once, here.
		applog.Errorf("transfer item records are unavailable from here on: %v", *err)
	}
	// A later item error must not replace an earlier terminal-save diagnostic.
	*err = errors.Join(s.latch(jobID, ErrItemRecordsUnavailable, false), itemError(*err))
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
	defer s.itemWriteFailed(jobID, &err)
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
			if item.deletion() != kind.deletion() {
				return ErrInvalidItemRecord
			}
			if kind.deletion() && (item.Source.Kind != ItemSwift || !item.Admitted || !item.Planned) {
				// What a selected-set deletion deletes is a Swift object the
				// operator selected, and the whole selected set is admitted at
				// once (ADR 0093 decision 4): a local path is not a member, and
				// a member the ledger does not count as admitted would end the
				// job complete with it untouched.
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
	source, err := json.Marshal(item.Source)
	if err != nil {
		return 0, err
	}
	destination, err := json.Marshal(item.Destination)
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO job_items (job_id, item_key, source_json, destination_json,
			planned, admitted, listed_bytes, listed_etag, listed_slo, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, item.Key, string(source), string(destination), item.Planned, item.Admitted,
		item.ListedBytes, item.ListedETag, item.ListedSLO, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) RecordExecution(ctx context.Context, jobID, key string, observation ItemObservation) (err error) {
	defer s.itemWriteFailed(jobID, &err)
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
	defer s.itemWriteFailed(jobID, &err)
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
// deletion member records its one outcome through RecordDeletion.
func transferItemJob(ctx context.Context, tx *sql.Tx, jobID string) error {
	kind, err := itemJobType(ctx, tx, jobID, true)
	if err != nil {
		return err
	}
	if kind.deletion() {
		return ErrInvalidItemRecord
	}
	return nil
}

// HeldKeys reports which of a listing page's keys the job already holds, so
// a later pass of a sweep admits only names first listed in it without
// keeping a run-wide set of names (ADR 0095 decision 2). The page is the
// caller's bound; the ledger answers it in one statement.
func (s *Store) HeldKeys(ctx context.Context, jobID string, keys []string) (map[string]bool, error) {
	held := map[string]bool{}
	if len(keys) == 0 {
		return held, nil
	}
	if len(keys) > 1000 {
		return nil, ErrInvalidItemPage
	}
	err := s.readItems(ctx, jobID, func(tx *sql.Tx) error {
		args := make([]any, 0, len(keys)+1)
		args = append(args, jobID)
		marks := make([]string, len(keys))
		for i, key := range keys {
			args = append(args, key)
			marks[i] = "?"
		}
		rows, err := tx.QueryContext(ctx, `SELECT item_key FROM job_items WHERE job_id=? AND item_key IN (`+strings.Join(marks, ",")+`)`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				return err
			}
			held[key] = true
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return held, nil
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

// The staging, publication and baseline columns an earlier build wrote are
// not read: they belonged to the staged model (ADR 0122 decision 13).
const selectItem = `SELECT id, job_id, item_key, source_json, destination_json,
	planned, admitted, listed_bytes, listed_etag, listed_slo, created_at, issued_at FROM job_items`

func scanItem(scan func(...any) error) (ItemRecord, error) {
	var item ItemRecord
	var source, destination, created string
	var issued sql.NullString
	if err := scan(&item.ID, &item.JobID, &item.Key, &source, &destination,
		&item.Planned, &item.Admitted, &item.ListedBytes, &item.ListedETag, &item.ListedSLO, &created, &issued); err != nil {
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
