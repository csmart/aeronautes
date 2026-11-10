package db_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/db"
)

// ADR 0105 decision 6. While the budget was enforced at one, every save of
// any setting stored it as one, so the upgrade clears a stored one for the new
// default to apply and leaves every other stored value as written. It clears
// once: an operator who chooses one afterwards keeps it.
func TestTheBudgetMigrationClearsOnlyAStoredOne(t *testing.T) {
	const read = `SELECT value FROM app_settings WHERE key = 'max_active_jobs'`
	for _, stored := range []string{"1", "3", "16", "0"} {
		t.Run("a stored "+stored, func(t *testing.T) {
			path, raw := databaseAtVersion(t, 20)
			if _, err := raw.ExecContext(t.Context(), `INSERT INTO app_settings (key, value) VALUES ('max_active_jobs', ?)`, stored); err != nil {
				t.Fatal(err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			upgraded, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			var after string
			err = upgraded.Reader().QueryRowContext(t.Context(), read).Scan(&after)
			if stored == "1" {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("a stored one is still %q after the upgrade (%v)", after, err)
				}
			} else if err != nil || after != stored {
				t.Fatalf("a stored %s became %q after the upgrade (%v)", stored, after, err)
			}

			if err := upgraded.WriteTx(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.ExecContext(t.Context(), `INSERT INTO app_settings (key, value) VALUES ('max_active_jobs', '1')
					ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := upgraded.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if err := reopened.Reader().QueryRowContext(t.Context(), read).Scan(&after); err != nil || after != "1" {
				t.Fatalf("a one chosen after the upgrade reads %q on the next start (%v)", after, err)
			}
		})
	}
}
