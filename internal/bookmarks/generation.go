package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// WithCurrentGeneration coordinates local cache use and job admission with
// publication. A separate check would let an edit invalidate the binding
// before the old session is installed or pinned. use must not perform external
// I/O or reenter the store; nil checks the generation without a local action.
func (st *Store) WithCurrentGeneration(ctx context.Context, id string, generation int64, use func() error) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	var current int64
	err := st.db.Reader().QueryRowContext(ctx, `SELECT generation FROM bookmarks WHERE id=?`, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		delete(st.unresolved, id)
		return ErrNotFound
	}
	if err != nil {
		if st.unresolved[id] {
			return ErrPublicationUnresolved
		}
		return fmt.Errorf("read bookmark generation: %w", err)
	}
	delete(st.unresolved, id)
	if current != generation {
		return ErrChanged
	}
	if use != nil {
		return use()
	}
	return nil
}
