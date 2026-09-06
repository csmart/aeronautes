package bookmarks

import (
	"context"
	"errors"
)

// CredentialStatus counts recorded work, not the keys still present in an
// external store. Even an empty journal cannot prove native keychain erasure.
type CredentialStatus struct {
	Retired    int64
	Unfinished int64
	Saving     int64
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
	return status, nil
}
