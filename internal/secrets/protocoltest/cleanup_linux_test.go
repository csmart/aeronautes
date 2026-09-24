//go:build linux

package protocoltest

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
)

// The ADR 0076 cleanup pass against the private Secret Service: a deleted
// bookmark's key leaves the collection and its journal row, and a
// dismissed removal prompt leaves both in place with the reason recorded.
func TestCleanupRemovesRetiredKeysFromTheCollection(t *testing.T) {
	f := fixture(t)
	f.failWith("")
	t.Cleanup(func() { f.failWith("") })
	backend, err := secrets.NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	if backend.Origin().Persistent == "" {
		t.Fatal("the pinned collection identity is missing; cleanup needs a verified backend")
	}
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	store := bookmarks.NewStore(d, backend)
	saved, err := store.Save(t.Context(), bookmarks.Bookmark{DisplayName: "fixture", AuthURL: "https://original.example/auth/v1.0", Username: "account:user"}, []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	var ref string
	if err := d.Reader().QueryRowContext(t.Context(), `SELECT secret_ref FROM bookmarks WHERE id=?`, saved.ID).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if value, err := backend.Get(t.Context(), ref); err != nil || string(value) != "original" {
		t.Fatalf("the collection does not hold the published key: %v", err)
	}
	if err := store.Delete(t.Context(), saved.ID); err != nil {
		t.Fatal(err)
	}

	// A dismissed prompt is unresolved: the key and its row stay.
	f.failWith("dismiss-delete")
	result, err := store.Cleanup(t.Context(), "")
	if err != nil || result.Removed != 0 || result.Pending != 1 || result.Reason == "" {
		t.Fatalf("dismissed removal = %+v, %v", result, err)
	}
	if strings.Contains(strings.ToLower(result.Reason), "removed") {
		t.Fatalf("an unresolved removal reads as done: %q", result.Reason)
	}
	f.failWith("")
	if _, err := backend.Get(t.Context(), ref); err != nil {
		t.Fatalf("a dismissed removal took the key: %v", err)
	}
	status, err := store.CredentialStatus(t.Context())
	if err != nil || status.Retired != 1 || status.CleanupReason != result.Reason {
		t.Fatalf("status after a dismissed removal = %+v, %v", status, err)
	}

	// Once the store answers, the key leaves the collection and the row.
	result, err = store.Cleanup(t.Context(), "")
	if err != nil || result.Removed != 1 || result.Pending != 0 {
		t.Fatalf("confirmed removal = %+v, %v", result, err)
	}
	if _, err := backend.Get(t.Context(), ref); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("the key is still in the collection after a confirmed removal: %v", err)
	}
	status, err = store.CredentialStatus(t.Context())
	if err != nil || status.Retired != 0 || status.CleanupReason != "" {
		t.Fatalf("status after a confirmed removal = %+v, %v", status, err)
	}
}
