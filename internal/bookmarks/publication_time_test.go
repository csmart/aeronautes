package bookmarks

import (
	"database/sql"
	"testing"
	"time"
)

func TestCredentialRetirementRecordsItsOwnTime(t *testing.T) {
	for _, action := range []string{"replacement", "deletion"} {
		t.Run(action, func(t *testing.T) {
			st, d, _ := newTestStore(t)
			b, err := st.Save(t.Context(), sample(), []byte("original"))
			if err != nil {
				t.Fatal(err)
			}
			previous := storedRecord(t, st, b.ID)
			const oldTime = "2001-01-01T00:00:00Z"
			if err := d.WriteTx(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.Exec(`UPDATE bookmarks SET created_at=?, updated_at=? WHERE id=?`, oldTime, oldTime, b.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			b, err = st.Get(t.Context(), b.ID)
			if err != nil {
				t.Fatal(err)
			}
			// The stored timestamp is wall-clock metadata, so allow small clock
			// adjustments without accepting the bookmark's historical date.
			earliest := time.Now().UTC().Add(-time.Minute)
			if action == "replacement" {
				_, err = st.Save(t.Context(), b, []byte("replacement"))
			} else {
				err = st.Delete(t.Context(), b.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			latest := time.Now().UTC().Add(time.Minute)
			var recorded string
			if err := d.Reader().QueryRow(`SELECT created_at FROM credential_work WHERE secret_ref=? AND state='retired'`,
				previous.ref).Scan(&recorded); err != nil {
				t.Fatal(err)
			}
			at, err := time.Parse(time.RFC3339, recorded)
			if err != nil || at.Before(earliest) || at.After(latest) {
				t.Errorf("retirement inherited historical bookmark time: %q, %v", recorded, err)
			}
			if value, err := st.secrets.Get(t.Context(), previous.ref); err != nil || string(value) != "original" {
				t.Fatalf("recording retirement altered the old key: %q, %v", value, err)
			}
		})
	}
}
