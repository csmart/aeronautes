package evidence

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ExclusiveOwnershipProof (§28.6b): a unique segment prefix an
// Aeronautes job itself wrote, recorded at write time. It is the ONLY
// v1 basis for destructive segment operations (Phase 6) — and it is a
// distinct positive proof, not a confidence level: Confirmed evidence
// (§24.1) proves a segment is *referenced*, never that it is *unshared*,
// and MUST NOT be promoted to ownership.

// Ownership is one recorded proof.
type Ownership struct {
	JobID     string
	Container string
	Prefix    string
	CreatedAt time.Time
}

// RecordOwnership stores a proof for a bookmark-scoped job-written
// prefix. Recording the same (bookmark, job, container, prefix) again is
// a no-op.
func (s *Store) RecordOwnership(ctx context.Context, bookmarkID, jobID, container, prefix string) error {
	if bookmarkID == "" || jobID == "" || container == "" || prefix == "" {
		return fmt.Errorf("ownership proof needs bookmark, job, container, and prefix")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var exists int
		err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM segment_ownership
			WHERE bookmark_id=? AND job_id=? AND container=? AND prefix=?`,
			bookmarkID, jobID, container, prefix).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			return nil
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO segment_ownership (bookmark_id, job_id, container, prefix, created_at)
			VALUES (?, ?, ?, ?, ?)`,
			bookmarkID, jobID, container, prefix, now)
		return err
	})
}

// HasExclusiveOwnership reports whether a proof exists for EXACTLY this
// (container, prefix) under the bookmark. Exact match only: a recorded
// parent prefix never covers a child and a child never covers its
// parent — the proof is the unique prefix as written, nothing broader.
func (s *Store) HasExclusiveOwnership(ctx context.Context, bookmarkID, container, prefix string) (bool, error) {
	var count int
	err := s.db.Reader().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM segment_ownership
		WHERE bookmark_id=? AND container=? AND prefix=?`,
		bookmarkID, container, prefix).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// Ownerships lists the recorded proofs for one container, newest first —
// the Backstage view's "these prefixes are ours" data.
func (s *Store) Ownerships(ctx context.Context, bookmarkID, container string) ([]Ownership, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `
		SELECT job_id, container, prefix, created_at FROM segment_ownership
		WHERE bookmark_id=? AND container=? ORDER BY id DESC`,
		bookmarkID, container)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Ownership
	for rows.Next() {
		var o Ownership
		var created string
		if err := rows.Scan(&o.JobID, &o.Container, &o.Prefix, &created); err != nil {
			return nil, err
		}
		if o.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
			return nil, fmt.Errorf("ownership row: bad created_at: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
