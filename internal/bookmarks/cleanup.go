package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/csmart/aeronautes/internal/secrets"
)

// CleanupResult reports one physical cleanup pass over retired credential
// references (ADR 0076).
type CleanupResult struct {
	// Removed counts references whose removal the active backend confirmed
	// and whose journal rows were then deleted.
	Removed int
	// Pending counts retired rows left in place: not recorded by the active
	// backend, still held by a live reference, or refused or unresolved by
	// the external store.
	Pending int
	// Reason says why the last row was left, empty when nothing was left.
	Reason string
}

// cleanupWait bounds one external delete for the caller. It does not
// cancel the native call: an abandoned delete is reported unresolved and
// its row stays (ADR 0055).
const cleanupWait = 15 * time.Second

type retiredRow struct {
	ref, bookmarkID string
	origin          secrets.Origin
}

// Cleanup removes retired credential references from the active backend,
// one at a time (ADR 0076). A reference is claimed under the store lock
// only when the active backend is the authority that recorded it, no
// bookmark publishes it, and no prepared write, acquisition lease or
// admitted job holds it; the lock is released before the external delete.
// A confirmed removal, nil or ErrNotFound from that verified authority,
// deletes the journal row. Any other answer keeps the row, records the
// reason and ends the pass, so a lost reply or an unavailable store never
// counts as cleaned. bookmarkID limits the pass to one bookmark's rows;
// empty means every retired row.
func (st *Store) Cleanup(ctx context.Context, bookmarkID string) (CleanupResult, error) {
	var result CleanupResult
	rows, err := st.retiredRows(ctx, bookmarkID)
	if err != nil {
		return result, err
	}
	for i, row := range rows {
		reason := st.claim(ctx, row)
		if reason != "" {
			result.Pending++
			result.Reason = reason
			continue
		}
		deleteCtx, cancel := context.WithTimeout(ctx, cleanupWait)
		deleteErr := st.secrets.Delete(deleteCtx, row.ref)
		if deleteErr == nil || errors.Is(deleteErr, secrets.ErrNotFound) {
			// A removal is confirmed by reading the key back, as a write is
			// (ADR 0017): a dismissed or ignored prompt can answer a delete
			// without removing anything.
			deleteErr = st.confirmRemoved(deleteCtx, row.ref)
		}
		cancel()
		st.mu.Lock()
		delete(st.claimed, row.ref)
		if deleteErr == nil {
			err := st.db.WriteTx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `DELETE FROM credential_work WHERE secret_ref=? AND state='retired'`, row.ref)
				return err
			})
			if err != nil {
				result.Pending += len(rows) - i
				result.Reason = "the key was removed but its journal row could not be deleted: " + err.Error()
				st.lastCleanup = result
				st.mu.Unlock()
				return result, err
			}
			result.Removed++
			st.mu.Unlock()
			continue
		}
		result.Pending += len(rows) - i
		result.Reason = deleteErr.Error()
		st.lastCleanup = result
		st.mu.Unlock()
		return result, nil
	}
	st.mu.Lock()
	st.lastCleanup = result
	st.mu.Unlock()
	return result, nil
}

func (st *Store) retiredRows(ctx context.Context, bookmarkID string) ([]retiredRow, error) {
	query := `SELECT secret_ref, bookmark_id, origin_kind, origin_instance, origin_persistent
		FROM credential_work WHERE state='retired'`
	args := []any{}
	if bookmarkID != "" {
		query += ` AND bookmark_id=?`
		args = append(args, bookmarkID)
	}
	rows, err := st.db.Reader().QueryContext(ctx, query+` ORDER BY created_at, secret_ref`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []retiredRow
	for rows.Next() {
		var r retiredRow
		var kind, instance, persistent string
		if err := rows.Scan(&r.ref, &r.bookmarkID, &kind, &instance, &persistent); err != nil {
			return nil, err
		}
		r.origin = secrets.Origin{Kind: secrets.Backend(kind), Instance: instance, Persistent: persistent}
		out = append(out, r)
	}
	return out, rows.Err()
}

// claim marks the reference as being cleaned, or says why it cannot be.
// Callers do not hold st.mu.
func (st *Store) claim(ctx context.Context, row retiredRow) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.secrets == nil || !row.origin.VerifiedBy(st.secrets.Origin()) {
		return "the store that recorded the key is not the active one"
	}
	var published int
	if err := st.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks WHERE secret_ref=?`, row.ref).Scan(&published); err != nil {
		return "the bookmark table could not be read: " + err.Error()
	}
	if published != 0 {
		return "a bookmark still publishes the key"
	}
	if st.preparing[row.ref] {
		return "a save still uses the key"
	}
	if st.leases[row.ref] != 0 {
		return "a read still uses the key"
	}
	if st.jobPins[row.bookmarkID] != 0 {
		return "a job still uses the bookmark"
	}
	if st.claimed == nil {
		st.claimed = map[string]bool{}
	}
	st.claimed[row.ref] = true
	return ""
}

// confirmRemoved reads the reference back after a delete the store
// answered: absence from the verified authority confirms the removal, a
// value still present means nothing was removed, and any other answer
// leaves the removal unresolved.
func (st *Store) confirmRemoved(ctx context.Context, ref string) error {
	_, err := st.secrets.Get(ctx, ref)
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		return nil
	case err == nil:
		return errors.New("the store still holds the key after answering the removal")
	default:
		return errors.New("the removal could not be confirmed: " + err.Error())
	}
}
