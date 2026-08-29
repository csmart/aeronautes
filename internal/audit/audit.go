// Package audit is the local mutation trail (§50): one event per
// mutating operation — what was asked, of which resource, with which
// transaction ids, and how it ended. The trail is a product feature with
// a retention cap (§51), not an unbounded log; and it must never carry
// secret material (K-SEC-003 spirit: summaries name resources and
// non-secret parameters only).
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/csmart/aeronautes/internal/db"
)

// Result is how an operation ended.
type Result string

const (
	ResultApplied Result = "applied"
	// ResultConflict: preflight detected a concurrent change; nothing
	// was written.
	ResultConflict Result = "conflict"
	// ResultVerifyMismatch: the write succeeded but re-reading showed a
	// different outcome than intended — surfaced, never hidden.
	ResultVerifyMismatch Result = "verify_mismatch"
	ResultFailed         Result = "failed"
)

// Event is one audit-trail entry.
type Event struct {
	ID         int64
	At         time.Time
	BookmarkID string
	// Operation is the action name ("object.metadata.update",
	// "container.create", "marker.delete", …).
	Operation string
	// Resource is the target ("container/object", "container", …).
	Resource string
	// Summary is a short human sentence of what changed; resource names
	// and non-secret parameters only.
	Summary string
	Result  Result
	// TransIDs are the Swift transaction ids touched (preflight HEAD,
	// write, verify HEAD), for cluster-side correlation.
	TransIDs []string
}

// forbiddenSummarySubstrings guards against a summary accidentally
// carrying credential-shaped content into the durable trail.
var forbiddenSummarySubstrings = []string{"password", "passphrase", "x-auth-key", "auth_key", "api_key"}

// Store persists audit events.
type Store struct {
	db *db.DB
	// keep is the retention cap: after every append, the oldest rows
	// beyond this count are compacted away (§51).
	keep int
}

// DefaultKeep is the default retention cap; generous for an operator
// tool, bounded for a SQLite file.
const DefaultKeep = 10000

// NewStore builds a Store with the default retention cap.
func NewStore(d *db.DB) *Store {
	return &Store{db: d, keep: DefaultKeep}
}

// NewStoreWithCap builds a Store keeping at most keep events.
func NewStoreWithCap(d *db.DB, keep int) *Store {
	if keep < 1 {
		keep = 1
	}
	return &Store{db: d, keep: keep}
}

// Append records one event and enforces the retention cap. Mutations
// call this after they know their outcome; a failing audit write is an
// error the caller surfaces (a mutation that cannot be accounted for
// should be loud), but the mutation itself has already happened.
func (s *Store) Append(ctx context.Context, e Event) error {
	if e.Operation == "" || e.Resource == "" || e.Result == "" {
		return fmt.Errorf("audit event needs operation, resource, and result: %+v", e)
	}
	lower := strings.ToLower(e.Summary)
	for _, forbidden := range forbiddenSummarySubstrings {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("audit summary looks secret-bearing (%q); refuse to record it", forbidden)
		}
	}
	at := e.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var transIDs *string
	if len(e.TransIDs) > 0 {
		encoded, err := json.Marshal(e.TransIDs)
		if err != nil {
			return err
		}
		v := string(encoded)
		transIDs = &v
	}
	var bookmark *string
	if e.BookmarkID != "" {
		bookmark = &e.BookmarkID
	}

	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events (at, bookmark_id, operation, resource, summary, result, transaction_ids_json)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			at.Format(time.RFC3339), bookmark, e.Operation, e.Resource,
			e.Summary, string(e.Result), transIDs); err != nil {
			return err
		}
		// Compact beyond the cap, oldest first (§51). An export path is
		// added with the audit UI; compaction is deliberately simple.
		_, err := tx.ExecContext(ctx, `
			DELETE FROM audit_events WHERE id IN (
				SELECT id FROM audit_events ORDER BY id DESC LIMIT -1 OFFSET ?
			)`, s.keep)
		return err
	})
}

// Recent returns the newest events, newest first, at most limit.
func (s *Store) Recent(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Reader().QueryContext(ctx, `
		SELECT id, at, COALESCE(bookmark_id,''), operation, resource, summary, result, COALESCE(transaction_ids_json,'')
		FROM audit_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		var at, result, transIDs string
		if err := rows.Scan(&e.ID, &at, &e.BookmarkID, &e.Operation, &e.Resource, &e.Summary, &result, &transIDs); err != nil {
			return nil, err
		}
		if e.At, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, fmt.Errorf("audit event %d: bad timestamp: %w", e.ID, err)
		}
		e.Result = Result(result)
		if transIDs != "" {
			if err := json.Unmarshal([]byte(transIDs), &e.TransIDs); err != nil {
				return nil, fmt.Errorf("audit event %d: bad transaction ids: %w", e.ID, err)
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
