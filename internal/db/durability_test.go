package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"modernc.org/sqlite"
)

func TestConnectionsUseDurableWAL(t *testing.T) {
	d := newTestDB(t)
	d.Reader().SetMaxIdleConns(0)
	ctx := context.Background()

	for _, name := range []string{"initial", "replacement"} {
		t.Run(name, func(t *testing.T) {
			// Holding the connections forces the pool to open distinct ones;
			// disabling idle reuse also exercises replacement connections.
			for range 4 {
				conn, err := d.Reader().Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				var mode string
				var synchronous int
				if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
					t.Fatal(err)
				}
				if err := conn.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
					t.Fatal(err)
				}
				if mode != "wal" || synchronous != 2 {
					t.Errorf("journal_mode=%q synchronous=%d; want wal and FULL (2)", mode, synchronous)
				}
			}
		})
	}
}

func TestOpenRefusesMemoryJournal(t *testing.T) {
	d, err := Open(context.Background(), ":memory:")
	if err == nil {
		d.Close()
		t.Fatal("Open accepted a database that cannot use WAL")
	}
	if !strings.Contains(err.Error(), "journal_mode") {
		t.Fatalf("Open returned an unrelated error: %v", err)
	}
}

func TestConnectionRefusesIneffectiveDurability(t *testing.T) {
	for _, settings := range []string{
		"_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		"_pragma=journal_mode(WAL)&_pragma=synchronous(OFF)",
		"_pragma=journal_mode(DELETE)&_pragma=synchronous(FULL)",
	} {
		t.Run(settings, func(t *testing.T) {
			path := filepath.ToSlash(filepath.Join(t.TempDir(), "state.sqlite"))
			connector, err := sqlite.NewConnector("file:" + path + "?" + settings)
			if err != nil {
				t.Fatal(err)
			}
			h := sql.OpenDB(durableConnector{connector})
			defer h.Close()
			if err := h.PingContext(context.Background()); err == nil || !strings.Contains(err.Error(), "require wal and FULL") {
				t.Fatalf("connection with ineffective durability returned %v", err)
			}
		})
	}
}

func BenchmarkWriteTx(b *testing.B) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(b.TempDir(), "state.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	defer d.Close()
	if err := d.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE durability_bench (id INTEGER PRIMARY KEY, payload TEXT NOT NULL)`)
		return err
	}); err != nil {
		b.Fatal(err)
	}
	payload := strings.Repeat("x", 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := d.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO durability_bench (id, payload) VALUES (?, ?)`, i, payload)
			return err
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}
