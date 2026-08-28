package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// migrate applies, in version order, every migrations/NNNN_name.sql whose
// version is not yet recorded in schema_migrations. Each migration runs in
// its own write transaction, so a failure leaves the database at the last
// fully-applied version.
func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.sql.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := d.sql.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := d.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
	}
	return nil
}

func (d *DB) applyMigration(ctx context.Context, m migration) error {
	return d.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			m.version, m.name, time.Now().UTC().Format(time.RFC3339))
		return err
	})
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		name := e.Name()
		version, ok := parseVersion(name)
		if !ok {
			return nil, fmt.Errorf("migration %q: name must be NNNN_description.sql", name)
		}
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: version, name: name, sql: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i := 1; i < len(ms); i++ {
		if ms[i].version == ms[i-1].version {
			return nil, fmt.Errorf("duplicate migration version %d", ms[i].version)
		}
	}
	return ms, nil
}

func parseVersion(name string) (int, bool) {
	base, ok := strings.CutSuffix(name, ".sql")
	if !ok {
		return 0, false
	}
	numPart, _, ok := strings.Cut(base, "_")
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(numPart)
	if err != nil || v < 1 {
		return 0, false
	}
	return v, true
}
