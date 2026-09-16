// Package mutationfacts is the identity-keyed ledger for object
// metadata writes (ADR 0058). Compacted audit_events are not this
// ledger; retention must not drop these rows.
package mutationfacts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/csmart/aeronautes/internal/db"
)

// Outcome is the committed fact for one write identity.
type Outcome string

const (
	OutcomeIntent         Outcome = "intent"
	OutcomeApplied        Outcome = "applied"
	OutcomeConflict       Outcome = "conflict"
	OutcomeVerifyMismatch Outcome = "verify_mismatch"
	OutcomeFailed         Outcome = "failed"
	OutcomeUnverified     Outcome = "unverified"
)

// Fact is one identity-keyed mutation record.
type Fact struct {
	Identity   string
	BookmarkID string
	Operation  string
	Resource   string
	Outcome    Outcome
	Summary    string
	TransIDs   []string
	CreatedAt  time.Time
	DecidedAt  time.Time
}

// Intent is the required row persisted before a store POST.
type Intent struct {
	Identity   string
	BookmarkID string
	Operation  string
	Resource   string
}

// Store persists mutation facts on the shared SQLite handle.
type Store struct {
	db *db.DB
}

// NewStore builds a Store on d.
func NewStore(d *db.DB) *Store {
	return &Store{db: d}
}

// NewIdentity returns a unique write identity.
func NewIdentity() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mutation fact identity: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// RecordIntent commits the intent. A SQL abort here must precede any
// store POST (ADR 0058).
func (s *Store) RecordIntent(ctx context.Context, in Intent) error {
	if in.Identity == "" || in.Operation == "" || in.Resource == "" {
		return fmt.Errorf("mutation intent needs identity, operation, and resource")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO mutation_facts (
				identity, bookmark_id, operation, resource, outcome, summary, created_at
			) VALUES (?, ?, ?, ?, ?, '', ?)`,
			in.Identity, in.BookmarkID, in.Operation, in.Resource, string(OutcomeIntent), now)
		return err
	})
}

// RecordOutcome writes the decided outcome and runs extra in the same
// transaction so the audit row cannot commit without the fact.
func (s *Store) RecordOutcome(ctx context.Context, identity string, outcome Outcome, summary string, transIDs []string, extra func(*sql.Tx) error) error {
	if extra == nil {
		return fmt.Errorf("mutation outcome %s: missing extra writer", identity)
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.recordOutcomeTx(ctx, tx, identity, outcome, summary, transIDs); err != nil {
			return err
		}
		return extra(tx)
	})
}

func (s *Store) recordOutcomeTx(ctx context.Context, tx *sql.Tx, identity string, outcome Outcome, summary string, transIDs []string) error {
	switch outcome {
	case OutcomeApplied, OutcomeConflict, OutcomeVerifyMismatch, OutcomeFailed, OutcomeUnverified:
	default:
		return fmt.Errorf("mutation outcome %s: invalid %q", identity, outcome)
	}
	if identity == "" {
		return fmt.Errorf("mutation outcome needs identity")
	}
	var transJSON *string
	if len(transIDs) > 0 {
		encoded, err := json.Marshal(transIDs)
		if err != nil {
			return err
		}
		v := string(encoded)
		transJSON = &v
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE mutation_facts
		SET outcome = ?, summary = ?, trans_ids_json = ?, decided_at = ?
		WHERE identity = ? AND outcome = ?`,
		string(outcome), summary, transJSON, time.Now().UTC().Format(time.RFC3339),
		identity, string(OutcomeIntent))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("mutation outcome %s: expected intent row", identity)
	}
	return nil
}

// Lookup returns the fact for identity.
func (s *Store) Lookup(ctx context.Context, identity string) (Fact, error) {
	var f Fact
	var at, decided, result, transIDs string
	err := s.db.Reader().QueryRowContext(ctx, `
		SELECT identity, bookmark_id, operation, resource, outcome, summary,
			COALESCE(trans_ids_json, ''), created_at, COALESCE(decided_at, '')
		FROM mutation_facts WHERE identity = ?`, identity).Scan(
		&f.Identity, &f.BookmarkID, &f.Operation, &f.Resource, &result, &f.Summary,
		&transIDs, &at, &decided)
	if err != nil {
		return Fact{}, err
	}
	f.Outcome = Outcome(result)
	if f.CreatedAt, err = time.Parse(time.RFC3339, at); err != nil {
		return Fact{}, fmt.Errorf("mutation fact %s: bad created_at: %w", identity, err)
	}
	if decided != "" {
		if f.DecidedAt, err = time.Parse(time.RFC3339, decided); err != nil {
			return Fact{}, fmt.Errorf("mutation fact %s: bad decided_at: %w", identity, err)
		}
	}
	if transIDs != "" {
		if err := json.Unmarshal([]byte(transIDs), &f.TransIDs); err != nil {
			return Fact{}, fmt.Errorf("mutation fact %s: bad transaction ids: %w", identity, err)
		}
	}
	return f, nil
}

// All returns every fact, oldest first, for reopen and abort proofs.
func (s *Store) All(ctx context.Context) ([]Fact, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `
		SELECT identity, bookmark_id, operation, resource, outcome, summary,
			COALESCE(trans_ids_json, ''), created_at, COALESCE(decided_at, '')
		FROM mutation_facts ORDER BY rowid ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		var f Fact
		var at, decided, result, transIDs string
		if err := rows.Scan(&f.Identity, &f.BookmarkID, &f.Operation, &f.Resource, &result, &f.Summary,
			&transIDs, &at, &decided); err != nil {
			return nil, err
		}
		f.Outcome = Outcome(result)
		if f.CreatedAt, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, fmt.Errorf("mutation fact %s: bad created_at: %w", f.Identity, err)
		}
		if decided != "" {
			if f.DecidedAt, err = time.Parse(time.RFC3339, decided); err != nil {
				return nil, fmt.Errorf("mutation fact %s: bad decided_at: %w", f.Identity, err)
			}
		}
		if transIDs != "" {
			if err := json.Unmarshal([]byte(transIDs), &f.TransIDs); err != nil {
				return nil, fmt.Errorf("mutation fact %s: bad transaction ids: %w", f.Identity, err)
			}
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
