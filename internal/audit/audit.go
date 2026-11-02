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
	"errors"
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
	// ResultUnverified: the write left the process and the verify read
	// did not complete, so the effect is unknown rather than failed
	// (ADR 0058). Distinct from ResultVerifyMismatch, which observed a
	// different post-state (ADR 0046 still qualifies a completed POST).
	ResultUnverified Result = "unverified"
	// ResultObserved records something READ rather than something written:
	// the state a mutation was about to act on, entered before the write
	// so it survives a write whose own outcome is never learned. It is the
	// only value here that is not a write's outcome, and it never stands
	// in for one (ADR 0081 decision 7).
	ResultObserved Result = "observed"
)

// Database is the shared handle for ledgers that must not use compacted
// audit_events (ADR 0058).
func (s *Store) Database() *db.DB {
	return s.db
}

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

// SummaryRefusal reports the forbidden substring a summary carries, or "" when
// the summary is recordable. AppendTx is the enforcement; this is the same
// decision asked in advance.
//
// It exists because a refused summary means no event at all, and a caller that
// builds a summary out of a name it does not control cannot know that in
// advance. A container named for a credential is a perfectly valid container,
// and on a destructive path "no record" is a worse outcome than "a record
// without the name in its prose" — the name is in Event.Resource either way,
// which this filter does not touch.
func SummaryRefusal(summary string) string {
	lower := strings.ToLower(summary)
	for _, forbidden := range forbiddenSummarySubstrings {
		if strings.Contains(lower, forbidden) {
			return forbidden
		}
	}
	return ""
}

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
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		return s.AppendTx(ctx, tx, e)
	})
}

// AppendTx lets a job's terminal snapshot and audit share one commit.
// The caller owns the transaction and must roll it back on any error.
func (s *Store) AppendTx(ctx context.Context, tx *sql.Tx, e Event) error {
	if e.Operation == "" || e.Resource == "" || e.Result == "" {
		return fmt.Errorf("audit event needs operation, resource, and result: %+v", e)
	}
	if forbidden := SummaryRefusal(e.Summary); forbidden != "" {
		return fmt.Errorf("audit summary looks secret-bearing (%q); refuse to record it", forbidden)
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

	if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events (at, bookmark_id, operation, resource, summary, result, transaction_ids_json)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
		at.Format(time.RFC3339), bookmark, e.Operation, e.Resource,
		e.Summary, string(e.Result), transIDs); err != nil {
		return err
	}
	// Retention shares the transaction so a failed compaction cannot
	// leave the caller's job snapshot committed without its audit.
	_, err := tx.ExecContext(ctx, `
			DELETE FROM audit_events WHERE id IN (
				SELECT id FROM audit_events ORDER BY id DESC LIMIT -1 OFFSET ?
			)`, s.keep)
	return err
}

// ErrBookmarkRequired refuses an unscoped trail read (ADR 0059).
var ErrBookmarkRequired = errors.New("name the bookmark the read is for")

// Recent is the unscoped newest-events path. It always refuses: a trail
// read must name the bookmark, so the bridge method that still calls this
// cannot answer across accounts (ADR 0059).
func (s *Store) Recent(ctx context.Context, limit int) ([]Event, error) {
	return nil, ErrBookmarkRequired
}

// Query narrows a trail read (§50 viewer): all filters AND together.
type Query struct {
	// OperationPrefix matches operations beginning with it ("transfer.",
	// "delete.empty", …).
	OperationPrefix string
	// Result filters by exact result ("applied", "failed", …).
	Result string
	// BookmarkID filters by endpoint.
	BookmarkID string
	// BeforeID pages backwards: only events with id < BeforeID (0 = from
	// the newest).
	BeforeID int64
	Limit    int64
}

// List reads the trail newest-first under the query.
func (s *Store) List(ctx context.Context, q Query) ([]Event, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `
		SELECT id, at, COALESCE(bookmark_id,''), operation, resource, summary, result, COALESCE(transaction_ids_json,'')
		FROM audit_events WHERE 1=1`
	var args []any
	if q.OperationPrefix != "" {
		query += ` AND operation LIKE ? ESCAPE '\'`
		args = append(args, likePrefix(q.OperationPrefix))
	}
	if q.Result != "" {
		query += ` AND result = ?`
		args = append(args, q.Result)
	}
	if q.BookmarkID != "" {
		query += ` AND bookmark_id = ?`
		args = append(args, q.BookmarkID)
	}
	if q.BeforeID > 0 {
		query += ` AND id < ?`
		args = append(args, q.BeforeID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Reader().QueryContext(ctx, query, args...)
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

// ExportTo streams every event matching the query to fn, newest first,
// from a SINGLE read so the archive is one consistent snapshot even if
// retention compacts rows while it runs (§51). Paging is deliberately
// absent — the whole matching trail is read — and the one statement
// bounds memory to a row at a time while, as a reader, never holding the
// serial write lane. It is the §51 export's read; List stays the paged
// viewer read.
func (s *Store) ExportTo(ctx context.Context, q Query, fn func(Event) error) error {
	query := `
		SELECT id, at, COALESCE(bookmark_id,''), operation, resource, summary, result, COALESCE(transaction_ids_json,'')
		FROM audit_events WHERE 1=1`
	var args []any
	if q.OperationPrefix != "" {
		query += ` AND operation LIKE ? ESCAPE '\'`
		args = append(args, likePrefix(q.OperationPrefix))
	}
	if q.Result != "" {
		query += ` AND result = ?`
		args = append(args, q.Result)
	}
	if q.BookmarkID != "" {
		query += ` AND bookmark_id = ?`
		args = append(args, q.BookmarkID)
	}
	query += ` ORDER BY id DESC`
	rows, err := s.db.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		var at, result, transIDs string
		if err := rows.Scan(&e.ID, &at, &e.BookmarkID, &e.Operation, &e.Resource, &e.Summary, &result, &transIDs); err != nil {
			return err
		}
		if e.At, err = time.Parse(time.RFC3339, at); err != nil {
			return fmt.Errorf("audit event %d: bad timestamp: %w", e.ID, err)
		}
		e.Result = Result(result)
		if transIDs != "" {
			if err := json.Unmarshal([]byte(transIDs), &e.TransIDs); err != nil {
				return fmt.Errorf("audit event %d: bad transaction ids: %w", e.ID, err)
			}
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// likePrefix escapes LIKE metacharacters so a prefix filter is a
// prefix filter, never a pattern (hostile input, ADR 0011).
func likePrefix(prefix string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
	return escaped + "%"
}

// Prune deletes events older than the cutoff — §51 retention. The
// trail is a local operational record, not an immutable ledger; the
// policy is configurable and the prune itself is logged by the caller.
func (s *Store) Prune(ctx context.Context, before time.Time) (int64, error) {
	cutoff := before.UTC().Format(time.RFC3339)
	var pruned int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE at < ?`, cutoff)
		if err != nil {
			return err
		}
		pruned, err = res.RowsAffected()
		return err
	})
	return pruned, err
}
