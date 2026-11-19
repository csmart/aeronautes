// Package db owns Aeronautes' durable, non-secret local state: one SQLite
// database in WAL mode, with schema migrations applied on open.
//
// Secrets never live here (K-SEC-003); credentials belong to the
// SecretStore. The database serves many concurrent readers, but every
// write goes through a single writer (WriteTx), because SQLite allows one
// writer at a time and Aeronautes is highly concurrent (DESIGN.md §51).
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"modernc.org/sqlite"
)

// DB is the handle to the local state database.
type DB struct {
	sql *sql.DB

	// writeMu serialises write transactions. Funnelling writers here keeps
	// contention out of SQLite's busy handler, where it would surface as
	// opaque SQLITE_BUSY errors instead of an orderly queue.
	writeMu sync.Mutex

	// writes counts committed write transactions since this handle opened.
	// Every write goes through WriteTx, so this is the one place that can
	// count them, which lets a proof measure a path's write cost (the work
	// done) rather than infer it from the rows left behind (ADR 0095).
	writes atomic.Int64
}

// Open opens (creating if needed) the database at path, configures the
// connection pragmas, and applies any pending migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	if strings.ContainsRune(path, 0) {
		return nil, errors.New("database path contains NUL")
	}
	// SQLite decodes URI paths: escape filename characters before adding
	// options, or a question mark, fragment or percent escape selects a
	// different file. Escaping only the path preserves relative/drive paths.
	file := url.URL{Path: filepath.ToSlash(path)}
	// _pragma values apply to every pooled connection, not just the first.
	dsn := "file:" + file.EscapedPath() + "?" + strings.Join([]string{
		"_pragma=busy_timeout(5000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=foreign_keys(1)",
		"_pragma=synchronous(FULL)",
	}, "&")

	connector, err := sqlite.NewConnector(dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	h := sql.OpenDB(durableConnector{connector})

	d := &DB{sql: h}
	if err := d.migrate(ctx); err != nil {
		h.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return d, nil
}

// Reader returns the handle for read-side queries. Reads may run
// concurrently with a writer under WAL. All writes must go through
// WriteTx; do not write on this handle.
func (d *DB) Reader() *sql.DB {
	return d.sql
}

// WriteTx runs fn inside the single write-transaction slot. If fn returns
// an error the transaction is rolled back and the error returned.
func (d *DB) WriteTx(ctx context.Context, fn func(*sql.Tx) error) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin write tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit write tx: %w", err)
	}
	d.writes.Add(1)
	return nil
}

// WritesCommitted reports how many write transactions have committed through
// WriteTx since this handle opened. It measures a path's write cost directly,
// for a proof that counts the work done rather than the rows retained.
func (d *DB) WritesCommitted() int64 {
	return d.writes.Load()
}

// Close closes the database.
func (d *DB) Close() error {
	return d.sql.Close()
}
