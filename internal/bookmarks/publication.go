package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

var (
	ErrChanged               = errors.New("bookmark changed during the operation; reload and try again")
	ErrPublicationUnresolved = errors.New("could not confirm the bookmark change; reload when local state is available")
)

type record struct {
	Bookmark
	ref        string
	origin     secrets.Origin
	generation int64
	revision   int64
	ca         sql.NullString
}

type rowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readRecord(ctx context.Context, reader rowReader, id string) (record, error) {
	r, err := scanRecord(reader.QueryRowContext(ctx, selectColumns+" WHERE id=?", id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return record{}, ErrNotFound
	}
	return r, err
}

func recordWork(ctx context.Context, tx *sql.Tx, r record, state string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO credential_work (secret_ref, bookmark_id, origin_kind,
			origin_instance, state, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		r.ref, r.ID, string(r.origin.Kind), r.origin.Instance, state,
		r.UpdatedAt.Format(time.RFC3339))
	return err
}

// A failed begin or rejected callback cannot commit. Distinguish those from
// a lost commit reply so a second read failure cannot pause an unchanged row.
func (st *Store) writePublication(ctx context.Context, fn func(*sql.Tx) error) (commitPossible bool, err error) {
	err = st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		err := fn(tx)
		commitPossible = err == nil
		return err
	})
	return commitPossible, err
}

func publishRecord(ctx context.Context, tx *sql.Tx, previous, proposed record, caPEM []byte, isNew bool) error {
	if caPEM != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ca_certs (id, pem, added_at) VALUES (?, ?, ?)`,
			proposed.TLSCAID, string(caPEM), proposed.UpdatedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	if isNew {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO bookmarks (id, display_name, auth_url, username,
				storage_url_override, secret_ref, tls_mode, tls_ca_id,
				performance_profile, segment_placement, chunk_size_bytes,
				created_at, updated_at, origin_kind, origin_instance, generation, revision)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			proposed.ID, proposed.DisplayName, proposed.AuthURL, proposed.Username,
			nullable(proposed.StorageURLOverride), nullable(proposed.ref), string(proposed.TLSMode), nullable(proposed.TLSCAID),
			proposed.PerformanceProfile, string(proposed.SegmentPlacement), nullableInt(proposed.ChunkSizeBytes),
			proposed.CreatedAt.Format(time.RFC3339), proposed.UpdatedAt.Format(time.RFC3339),
			string(proposed.origin.Kind), proposed.origin.Instance, proposed.generation, proposed.revision)
		if err != nil {
			return err
		}
	} else {
		result, err := tx.ExecContext(ctx, `
			UPDATE bookmarks SET display_name=?, auth_url=?, username=?,
				storage_url_override=?, secret_ref=?, tls_mode=?, tls_ca_id=?,
				performance_profile=?, segment_placement=?, chunk_size_bytes=?,
				updated_at=?, origin_kind=?, origin_instance=?, generation=?, revision=?
			WHERE id=? AND generation=? AND revision=?`,
			proposed.DisplayName, proposed.AuthURL, proposed.Username,
			nullable(proposed.StorageURLOverride), nullable(proposed.ref), string(proposed.TLSMode), nullable(proposed.TLSCAID),
			proposed.PerformanceProfile, string(proposed.SegmentPlacement), nullableInt(proposed.ChunkSizeBytes),
			proposed.UpdatedAt.Format(time.RFC3339), string(proposed.origin.Kind), proposed.origin.Instance,
			proposed.generation, proposed.revision, proposed.ID, previous.generation, previous.revision)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrChanged
		}
	}
	if previous.ref != "" && previous.ref != proposed.ref {
		if err := recordWork(ctx, tx, previous, "retired"); err != nil {
			return err
		}
	}
	if proposed.ref != "" && proposed.ref != previous.ref {
		result, err := tx.ExecContext(ctx, `DELETE FROM credential_work WHERE secret_ref=? AND state='prepared'`, proposed.ref)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("prepared credential record is missing")
		}
	}
	return nil
}

// Commit can succeed even when its response fails. Keep coordination while
// resolving it, and never compensate a candidate that might be published.
func (st *Store) publicationResult(ctx context.Context, previous, proposed record, cause error, isNew bool) error {
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	current, err := readRecord(check, st.db.Reader(), proposed.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		st.unresolved[proposed.ID] = true
		return ErrPublicationUnresolved
	}
	delete(st.unresolved, proposed.ID)
	if err == nil && samePublication(current, proposed) {
		return nil
	}
	if (isNew && errors.Is(err, ErrNotFound)) || (!isNew && err == nil && samePublication(current, previous)) {
		return cause
	}
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return ErrChanged
}

func samePublication(a, b record) bool {
	return a.Bookmark == b.Bookmark && a.ref == b.ref && a.origin == b.origin &&
		a.generation == b.generation && a.revision == b.revision
}

// Available prevents new cache acquisition while a commit outcome cannot be
// read back. Held-session revocation belongs to the generation-lifetime slice.
func (st *Store) Available(ctx context.Context, id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.unresolved[id] {
		return nil
	}
	_, err := readRecord(ctx, st.db.Reader(), id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return ErrPublicationUnresolved
	}
	delete(st.unresolved, id)
	return err
}

// ConnectionConfig captures configuration, CA, reference and generation in
// one SQL read. The lease covers the blocking external read, after which a
// changed binding refuses acquisition instead of mixing generations.
func (st *Store) ConnectionConfig(ctx context.Context, id string) (swiftcontrol.Config, int64, error) {
	st.mu.Lock()
	r, err := readRecord(ctx, st.db.Reader(), id)
	if err != nil {
		st.mu.Unlock()
		return swiftcontrol.Config{}, 0, err
	}
	delete(st.unresolved, id)
	if r.ref == "" || !r.origin.ReadableBy(st.secrets.Origin()) {
		st.mu.Unlock()
		return swiftcontrol.Config{}, 0, secrets.ErrNotFound
	}
	if r.TLSMode == swiftcontrol.TLSModeCustomCA && !r.ca.Valid {
		st.mu.Unlock()
		return swiftcontrol.Config{}, 0, errors.New("custom CA bundle is unavailable")
	}
	st.leases[r.ref]++
	st.mu.Unlock()

	key, keyErr := st.secrets.Get(ctx, r.ref)

	st.mu.Lock()
	defer st.mu.Unlock()
	defer func() {
		st.leases[r.ref]--
		if st.leases[r.ref] == 0 {
			delete(st.leases, r.ref)
		}
	}()
	defer clear(key)
	current, err := readRecord(ctx, st.db.Reader(), id)
	if err != nil {
		return swiftcontrol.Config{}, 0, err
	}
	if current.generation != r.generation || current.ref != r.ref || st.unresolved[id] {
		return swiftcontrol.Config{}, 0, ErrChanged
	}
	if keyErr != nil {
		if errors.Is(keyErr, secrets.ErrNotFound) {
			return swiftcontrol.Config{}, 0, secrets.ErrNotFound
		}
		return swiftcontrol.Config{}, 0, errors.New("credential backend could not read the saved key")
	}
	var ca []byte
	if r.TLSMode == swiftcontrol.TLSModeCustomCA {
		ca = []byte(r.ca.String)
	}
	return r.SwiftConfig(key, ca), r.generation, nil
}
