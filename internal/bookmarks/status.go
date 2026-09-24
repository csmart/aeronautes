package bookmarks

import (
	"context"
	"errors"

	"github.com/csmart/aeronautes/internal/secrets"
)

// CredentialStatus counts recorded work, not the keys still present in an
// external store. Even an empty journal cannot prove native keychain erasure.
type CredentialStatus struct {
	Retired    int64
	Unfinished int64
	Saving     int64
	// Unverified counts retired and unfinished work whose recorded
	// authority this session cannot confirm it can reach, including
	// empty-Persistent keychain and legacy rows and any recorded
	// collection that does not match the active store.
	// It uses the same predicate as the credential read, so a count and a
	// refusal cannot disagree. Zero means every retired and unfinished row
	// counted here was written by the store in use now. It says nothing
	// about the excluded active saves or about credentials generally, and
	// never means the work is finished or that a native key is reachable.
	Unverified int64
	// CleanupReason says why the last physical cleanup pass left a retired
	// row in place (ADR 0076); empty when it left none.
	CleanupReason string
}

func (st *Store) CredentialStatus(ctx context.Context) (CredentialStatus, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	var status CredentialStatus
	err := st.db.Reader().QueryRowContext(ctx, `SELECT
		COUNT(*) FILTER (WHERE state='retired'),
		COUNT(*) FILTER (WHERE state='prepared') FROM credential_work`).Scan(
		&status.Retired, &status.Unfinished)
	if err != nil {
		return CredentialStatus{}, errors.New("credential cleanup status is unavailable")
	}
	// Publication and the active-write set share this lock, so a blocked
	// keychain prompt is never presented as an abandoned save in this app.
	status.Saving = int64(len(st.preparing))
	status.Unfinished -= status.Saving

	unverified, err := st.unverifiedWork(ctx)
	if err != nil {
		return CredentialStatus{}, errors.New("credential cleanup status is unavailable")
	}
	status.Unverified = unverified
	status.CleanupReason = st.lastCleanup.Reason
	return status, nil
}

// unverifiedWork counts journal rows the active authority cannot confirm.
// Saves running in this app are excluded so the facet stays a subset of the
// retired and unfinished counts. Callers hold st.mu.
func (st *Store) unverifiedWork(ctx context.Context) (int64, error) {
	rows, err := st.db.Reader().QueryContext(ctx,
		`SELECT secret_ref, origin_kind, origin_instance, origin_persistent FROM credential_work`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	// Journal-only reporting must survive an unavailable backend. With no
	// active authority nothing can be confirmed, so every row counts.
	var active secrets.Origin
	if st.secrets != nil {
		active = st.secrets.Origin()
	}
	var count int64
	for rows.Next() {
		var ref, kind, instance, persistent string
		if err := rows.Scan(&ref, &kind, &instance, &persistent); err != nil {
			return 0, err
		}
		origin := secrets.Origin{Kind: secrets.Backend(kind), Instance: instance, Persistent: persistent}
		if !st.preparing[ref] && !origin.VerifiedBy(active) {
			count++
		}
	}
	return count, rows.Err()
}
