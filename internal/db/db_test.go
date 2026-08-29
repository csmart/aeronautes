package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpenAppliesMigrations(t *testing.T) {
	d := newTestDB(t)

	want := []string{
		"audit_events",
		"bookmarks",
		"capability_snapshots",
		"jobs",
		"performance_profiles",
		"schema_migrations",
		"segment_evidence",
		"segment_ownership",
		"ui_preferences",
	}
	for _, table := range want {
		var name string
		err := d.Reader().QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing after migration: %v", table, err)
		}
	}

	var applied int
	if err := d.Reader().QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if applied < 1 {
		t.Fatalf("schema_migrations records %d migrations, want at least 1", applied)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	ctx := context.Background()

	d1, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	var first int
	if err := d1.Reader().QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	d1.Close()

	d2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer d2.Close()
	var second int
	if err := d2.Reader().QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("re-open re-applied migrations: %d then %d", first, second)
	}
}

func TestJournalModeIsWAL(t *testing.T) {
	d := newTestDB(t)
	var mode string
	if err := d.Reader().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	d := newTestDB(t)
	err := d.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO capability_snapshots (bookmark_id, fetched_at, source, payload_json)
			 VALUES ('no-such-bookmark', '2026-08-28T00:00:00Z', 'advertised', '{}')`)
		return err
	})
	if err == nil {
		t.Fatal("insert with dangling bookmark_id succeeded; foreign keys are not enforced")
	}
}

func TestWriteTxSerialisesWriters(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()

	if err := d.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO ui_preferences (name, value) VALUES ('counter', '0')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	const workers, increments = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range increments {
				err := d.WriteTx(ctx, func(tx *sql.Tx) error {
					var v int
					if err := tx.QueryRow(`SELECT value FROM ui_preferences WHERE name='counter'`).Scan(&v); err != nil {
						return err
					}
					_, err := tx.Exec(`UPDATE ui_preferences SET value=? WHERE name='counter'`, fmt.Sprint(v+1))
					return err
				})
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent WriteTx: %v", err)
	}

	var got int
	if err := d.Reader().QueryRow(`SELECT value FROM ui_preferences WHERE name='counter'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if want := workers * increments; got != want {
		t.Fatalf("counter = %d after concurrent writes, want %d", got, want)
	}
}

// TestNoSecretShapedColumns is the K-SEC-003 static check: the schema must
// never grow a column that stores credential material. secret_ref is the
// one allowed name — it holds a SecretStore lookup name, not a secret.
func TestNoSecretShapedColumns(t *testing.T) {
	d := newTestDB(t)

	forbidden := []string{"password", "passphrase", "secret", "token", "credential", "api_key", "auth_key", "private_key"}
	allowed := map[string]bool{"secret_ref": true}

	rows, err := d.Reader().Query(`
		SELECT m.name, p.name
		FROM sqlite_master m
		JOIN pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		lc := strings.ToLower(column)
		if allowed[lc] {
			continue
		}
		for _, bad := range forbidden {
			if strings.Contains(lc, bad) {
				t.Errorf("column %s.%s looks secret-shaped (%q); secrets must live in the SecretStore (K-SEC-003)", table, column, bad)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestStepwiseUpgradeFromEveryHistoricalSchema simulates a database
// left at every prior schema version and proves Open brings each one to
// head — the §65.6 upgrade guarantee, not just the fresh-create path.
func TestStepwiseUpgradeFromEveryHistoricalSchema(t *testing.T) {
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 2 {
		t.Fatalf("expected several migrations, have %d", len(migrations))
	}

	for cut := 1; cut < len(migrations); cut++ {
		path := filepath.Join(t.TempDir(), "state.sqlite")

		// Build the historical database: raw handle, first `cut`
		// migrations, schema_migrations rows exactly as Open would leave
		// them at that point in history.
		raw, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.ExecContext(ctx, `
			CREATE TABLE schema_migrations (
				version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL
			)`); err != nil {
			t.Fatal(err)
		}
		for _, m := range migrations[:cut] {
			if _, err := raw.ExecContext(ctx, m.sql); err != nil {
				t.Fatalf("historical %04d: %v", m.version, err)
			}
			if _, err := raw.ExecContext(ctx,
				`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, 'x')`,
				m.version, m.name); err != nil {
				t.Fatal(err)
			}
		}
		raw.Close()

		d, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("upgrade from version %04d: %v", migrations[cut-1].version, err)
		}
		var head int
		if err := d.Reader().QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&head); err != nil {
			t.Fatal(err)
		}
		if head != migrations[len(migrations)-1].version {
			t.Fatalf("from %04d: head = %04d", migrations[cut-1].version, head)
		}
		// Every expected table exists after the upgrade.
		for _, table := range []string{"bookmarks", "jobs", "audit_events", "segment_ownership", "app_settings", "benchmark_results", "ca_certs"} {
			var n int
			if err := d.Reader().QueryRowContext(ctx,
				`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
				t.Fatalf("from %04d: table %s missing (%v)", migrations[cut-1].version, table, err)
			}
		}
		d.Close()
	}
}
