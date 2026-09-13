package systemtest

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
)

// This service action only reads local state; using real SQLite and a
// synthetic memory backend keeps its proof independent of Swift availability.
func credentialHarness(t *testing.T) (*Harness, *db.DB) {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	store := bookmarks.NewStore(d, secrets.NewSessionStore())
	return &Harness{T: t, Bookmarks: app.NewBookmarkService(store, app.NewConnections(store), nil)}, d
}

func TestCredentialCleanupStatusAfterDeletedBookmark(t *testing.T) {
	h, d := credentialHarness(t)
	if status, read := h.CredentialStatus(t.Context()); read.Err() != nil || status != (app.CredentialStatusDTO{}) {
		t.Fatalf("empty status = %+v, %v", status, read.Err())
	}
	b, err := h.Bookmarks.Save(t.Context(), app.BookmarkDTO{
		DisplayName: "synthetic", AuthURL: "https://swift.example/auth/v1.0", Username: "account:user",
	}, "synthetic-first-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Bookmarks.Save(t.Context(), b, "synthetic-second-key"); err != nil {
		t.Fatal(err)
	}
	if err := h.Bookmarks.Delete(t.Context(), b.ID); err != nil {
		t.Fatal(err)
	}
	status, read := h.CredentialStatus(t.Context())
	// The session store that recorded this work is still the active one.
	if read.Err() != nil || status != (app.CredentialStatusDTO{Retired: 2}) {
		t.Fatalf("deleted bookmark's retained work = %+v, %v", status, read.Err())
	}
	encoded, err := json.Marshal(status)
	if err != nil || string(encoded) != `{"retired":2,"unfinished":0,"saving":0,"unverified":0}` {
		t.Fatalf("status bridge must expose only counts: %s, %v", encoded, err)
	}
	list, err := h.Bookmarks.List(t.Context())
	if err != nil || len(list) != 0 {
		t.Fatalf("status restored the deleted bookmark: %+v, %v", list, err)
	}
	var retained int
	if err := d.Reader().QueryRow(`SELECT COUNT(*) FROM credential_work`).Scan(&retained); err != nil || retained != 2 {
		t.Fatalf("reading status removed journal evidence: %d, %v", retained, err)
	}
	read.CreditRead()
}

func TestCredentialCleanupStatusRefusesUnavailableState(t *testing.T) {
	h, d := credentialHarness(t)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	status, read := h.CredentialStatus(t.Context())
	if read.Err() == nil || status != (app.CredentialStatusDTO{}) {
		t.Fatalf("failed read claimed empty work: %+v, %v", status, read.Err())
	}
	if read.Err().Error() != "credential cleanup status is unavailable" {
		t.Fatalf("unexpected bridge error: %v", read.Err())
	}
	read.CreditRefusal()
}
