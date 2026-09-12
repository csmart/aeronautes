package deletion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
)

type identityKind int

const (
	reportedFailureIdentity identityKind = 1
	unresolvedIdentity      identityKind = 2
	// issuedIdentity records a name this job has sent a destructive
	// request for, written before the request leaves, so the same job
	// never sends a second request for the name in any later pass.
	issuedIdentity            identityKind = 3
	maxIdentityFacets                      = 2000
	identityAccountingTimeout              = 10 * time.Second
)

type identityFacet struct {
	kind identityKind
	name string
}

// The executor serialises this private owner with its outcome guard.
// Its lifetime covers all passes; returning it in Result would keep the
// working database alive after the executor has finished accounting.
type identityIndex struct {
	dir      string
	path     string
	db       *sql.DB
	conn     *sql.Conn
	failure  error
	closed   bool
	closeErr error

	// Tests cancel after real inserts to check that pending membership
	// cannot escape an interrupted transaction.
	beforeCommit func(*sql.Tx)
}

func newIdentityIndex(ctx context.Context, cacheRoot string) (_ *identityIndex, err error) {
	if !filepath.IsAbs(cacheRoot) {
		return nil, errors.New("deletion identity cache location must be absolute")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, identityAccountingTimeout)
	defer cancel()
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create deletion identity cache: %w", err)
	}
	dir, err := os.MkdirTemp(cacheRoot, "delete-identities-")
	if err != nil {
		return nil, fmt.Errorf("create deletion identity directory: %w", err)
	}
	index := &identityIndex{dir: dir, path: filepath.Join(dir, "identities.sqlite")}
	defer func() {
		if err != nil {
			err = errors.Join(err, index.close())
		}
	}()
	file, err := os.OpenFile(index.path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create deletion identity file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close new deletion identity file: %w", err)
	}
	connector, err := sqlite.NewConnector(identityIndexURI(index.path))
	if err != nil {
		return nil, fmt.Errorf("open deletion identity index: %w", err)
	}
	index.db = sql.OpenDB(connector)
	index.db.SetMaxOpenConns(1)
	index.db.SetMaxIdleConns(1)
	index.conn, err = index.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect deletion identity index: %w", err)
	}
	if err := index.configure(ctx); err != nil {
		return nil, err
	}
	if _, err := index.conn.ExecContext(ctx, `CREATE TABLE seen (
		kind INTEGER NOT NULL CHECK (kind IN (1, 2, 3)),
		name BLOB NOT NULL CHECK (typeof(name) = 'blob'),
		PRIMARY KEY (kind, name)
	) WITHOUT ROWID`); err != nil {
		return nil, fmt.Errorf("create deletion identity table: %w", err)
	}
	return index, nil
}

func identityIndexURI(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw&cache=private"}).String()
}

func (index *identityIndex) configure(ctx context.Context) error {
	// Explicit setup keeps page size ahead of schema creation and avoids
	// the driver's sorted, background-context DSN pragma processing.
	for _, setting := range []struct{ set, read, want string }{
		{"PRAGMA page_size=4096", "PRAGMA page_size", "4096"},
		{"PRAGMA cache_size=256", "PRAGMA cache_size", "256"},
		{"PRAGMA mmap_size=0", "PRAGMA mmap_size", "0"},
		{"PRAGMA temp_store=FILE", "PRAGMA temp_store", "1"},
		{"PRAGMA journal_mode=DELETE", "PRAGMA journal_mode", "delete"},
		{"PRAGMA synchronous=FULL", "PRAGMA synchronous", "2"},
		{"PRAGMA busy_timeout=0", "PRAGMA busy_timeout", "0"},
	} {
		if _, err := index.conn.ExecContext(ctx, setting.set); err != nil {
			return fmt.Errorf("configure deletion index (%s): %w", setting.set, err)
		}
		var got string
		if err := index.conn.QueryRowContext(ctx, setting.read).Scan(&got); err != nil {
			return fmt.Errorf("read deletion index setting (%s): %w", setting.read, err)
		}
		if got != setting.want {
			return fmt.Errorf("deletion index setting %s is %q, expected %q", setting.read, got, setting.want)
		}
	}
	if _, err := index.conn.ExecContext(ctx, "PRAGMA cache_spill=ON"); err != nil {
		return fmt.Errorf("enable deletion index cache spill: %w", err)
	}
	var spill int
	if err := index.conn.QueryRowContext(ctx, "PRAGMA cache_spill").Scan(&spill); err != nil {
		return fmt.Errorf("read deletion index cache spill: %w", err)
	}
	if spill <= 0 {
		return errors.New("deletion index cache spill is disabled")
	}
	return nil
}

func (index *identityIndex) add(ctx context.Context, facets []identityFacet) (inserted []bool, err error) {
	if index.closed {
		return nil, errors.New("deletion identity index is closed")
	}
	if index.failure != nil {
		return nil, fmt.Errorf("deletion identity accounting has stopped: %w", index.failure)
	}
	defer func() {
		if err != nil {
			// A failed or cancelled commit cannot be replayed to recover
			// which identities were new. Never publish its pending marks.
			inserted = nil
			index.failure = err
		}
	}()
	if len(facets) > maxIdentityFacets {
		return nil, fmt.Errorf("deletion identity transaction exceeds %d facets", maxIdentityFacets)
	}
	if len(facets) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, identityAccountingTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("begin deletion identity transaction: %w", err)
	}
	tx, err := index.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin deletion identity transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback deletion identity transaction: %w", rollbackErr))
		}
	}()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO seen(kind, name) VALUES (?, ?)
		ON CONFLICT(kind, name) DO NOTHING`)
	if err != nil {
		return nil, fmt.Errorf("prepare deletion identity insert: %w", err)
	}
	defer func() {
		if stmt != nil {
			if closeErr := stmt.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close deletion identity insert: %w", closeErr))
			}
		}
	}()
	inserted = make([]bool, len(facets))
	for i, facet := range facets {
		row, err := stmt.ExecContext(ctx, facet.kind, []byte(facet.name))
		if err != nil {
			return nil, fmt.Errorf("insert deletion identity: %w", err)
		}
		count, err := row.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("count inserted deletion identity: %w", err)
		}
		if count < 0 || count > 1 {
			return nil, fmt.Errorf("deletion identity insert affected %d rows", count)
		}
		inserted[i] = count == 1
	}
	closeErr := stmt.Close()
	stmt = nil
	if closeErr != nil {
		return nil, fmt.Errorf("close deletion identity insert: %w", closeErr)
	}
	if index.beforeCommit != nil {
		index.beforeCommit(tx)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit deletion identity transaction: %w", err)
	}
	return inserted, nil
}

func (index *identityIndex) close() error {
	if index.closed {
		return index.closeErr
	}
	index.closed = true
	if index.conn != nil {
		if err := index.conn.Close(); err != nil {
			index.closeErr = fmt.Errorf("close deletion identity connection: %w", err)
		}
	}
	if index.db != nil {
		if err := index.db.Close(); err != nil {
			index.closeErr = errors.Join(index.closeErr, fmt.Errorf("close deletion identity database: %w", err))
		}
	}
	// A failed close does not establish that the database has released
	// its files. Preserve the private directory rather than unlink it.
	if index.closeErr == nil {
		if err := os.RemoveAll(index.dir); err != nil {
			index.closeErr = fmt.Errorf("remove deletion identity directory: %w", err)
		}
	}
	return index.closeErr
}
