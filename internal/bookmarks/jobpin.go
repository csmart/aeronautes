package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/csmart/aeronautes/internal/secrets"
)

var ErrInUse = errors.New("a starting, queued or running job still uses this bookmark; wait for the job and its cleanup to finish")

// PinGeneration prevents a job's binding from changing after acquisition,
// including before its durable row exists and while cancellation cleans up.
// The caller must release on every failed start or after all job work returns.
func (st *Store) PinGeneration(ctx context.Context, id string, generation int64) (func(), error) {
	var ref sql.NullString
	err := st.WithCurrentGeneration(ctx, id, generation, func() error {
		if err := st.db.Reader().QueryRowContext(ctx, `SELECT secret_ref FROM bookmarks WHERE id=?`, id).Scan(&ref); err != nil {
			return fmt.Errorf("read job credential binding: %w", err)
		}
		if !ref.Valid || ref.String == "" {
			return secrets.ErrNotFound
		}
		st.jobPins[id]++
		st.leases[ref.String]++
		return nil
	})
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			st.mu.Lock()
			defer st.mu.Unlock()
			st.jobPins[id]--
			if st.jobPins[id] == 0 {
				delete(st.jobPins, id)
			}
			st.leases[ref.String]--
			if st.leases[ref.String] == 0 {
				delete(st.leases, ref.String)
			}
		})
	}, nil
}
