package protocoltest

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/godbus/dbus/v5"
)

func TestPinnedCollectionSurvivesDefaultRetarget(t *testing.T) {
	f := fixture(t)
	f.setCollections([]dbus.ObjectPath{extraPath})
	if err := f.SetAlias("default", extraPath); err != nil {
		t.Fatal(err)
	}
	store, err := secrets.NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	if store.Origin().Persistent != string(extraPath) {
		t.Fatalf("persistent = %q, want extra collection", store.Origin().Persistent)
	}
	name := "fixture/" + t.Name()
	value := []byte("original-collection-value")
	if err := store.Put(t.Context(), name, value); err != nil {
		t.Fatal(err)
	}
	f.setCollections([]dbus.ObjectPath{extraPath, loginPath})
	if err := f.SetAlias("default", loginPath); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(t.Context(), name)
	if err != nil || string(got) != string(value) {
		t.Fatalf("pinned store lost the original collection: %q %v", got, err)
	}
	if _, ok := f.valueIn(extraPath, name); !ok {
		t.Fatal("original collection dropped the value")
	}
}

func TestChangedDefaultIsNotErasure(t *testing.T) {
	f := fixture(t)
	f.setCollections([]dbus.ObjectPath{extraPath})
	if err := f.SetAlias("default", extraPath); err != nil {
		t.Fatal(err)
	}
	backend, err := secrets.NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	store := bookmarks.NewStore(d, backend)
	saved, err := store.Save(t.Context(), bookmarks.Bookmark{
		DisplayName: "pin", AuthURL: "https://swift.test/auth/v1.0", Username: "test",
	}, []byte("keep-me"))
	if err != nil {
		t.Fatal(err)
	}
	f.setCollections([]dbus.ObjectPath{extraPath, loginPath})
	if err := f.SetAlias("default", loginPath); err != nil {
		t.Fatal(err)
	}
	moved, err := secrets.NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	if moved.Origin().Persistent == backend.Origin().Persistent {
		t.Fatal("new store did not follow the changed default")
	}
	_, _, err = bookmarks.NewStore(d, moved).ConnectionConfig(t.Context(), saved.ID)
	if !errors.Is(err, secrets.ErrAuthorityUnreachable) {
		t.Fatalf("changed default = %v, want unreachable", err)
	}
	if errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("changed default was reported as erasure")
	}
	for _, private := range []string{string(extraPath), string(loginPath), saved.ID, "keep-me"} {
		if strings.Contains(err.Error(), private) {
			t.Fatal("unreachable error exposed a collection path, reference or value")
		}
	}
	if _, ok := f.valueInByValue(extraPath, "keep-me"); !ok {
		t.Fatal("original collection lost the credential")
	}
	if got := bookmarkPersistent(t, d, saved.ID); got != string(extraPath) {
		t.Fatalf("journal persistent = %q", got)
	}
}

func TestMissingOriginalCollectionIsUnreachable(t *testing.T) {
	f := fixture(t)
	store, err := secrets.NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	name := "fixture/" + t.Name()
	if err := store.Put(t.Context(), name, []byte("v")); err != nil {
		t.Fatal(err)
	}
	f.setCollections([]dbus.ObjectPath{extraPath})
	_, err = store.Get(t.Context(), name)
	assertHiddenUnreachable(t, "Get", err)
	err = store.Put(t.Context(), name, []byte("again"))
	assertHiddenUnreachable(t, "Put", err)
	err = store.Delete(t.Context(), name)
	assertHiddenUnreachable(t, "Delete", err)
}

func assertHiddenUnreachable(t *testing.T, op string, err error) {
	t.Helper()
	if !errors.Is(err, secrets.ErrAuthorityUnreachable) {
		t.Fatalf("%s hidden collection = %v, want unreachable", op, err)
	}
	if errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("%s hidden collection was reported as erasure", op)
	}
	for _, private := range []string{string(extraPath), string(loginPath), t.Name()} {
		if private != "" && strings.Contains(err.Error(), private) {
			t.Fatalf("%s error exposed a collection path or reference", op)
		}
	}
}

func TestFailedAliasReadLeavesPersistentEmpty(t *testing.T) {
	f := fixture(t)
	f.setCollections([]dbus.ObjectPath{extraPath})
	if err := f.SetAlias("default", extraPath); err != nil {
		t.Fatal(err)
	}
	f.failWith("read-alias-error")
	store, err := secrets.NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	if store.Origin().Persistent != "" {
		t.Fatalf("failed alias read minted persistent %q", store.Origin().Persistent)
	}
	name := "fixture/" + t.Name()
	if err := store.Put(t.Context(), name, []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(t.Context(), name); err != nil && !errors.Is(err, secrets.ErrAuthorityUnreachable) {
		t.Fatal(err)
	}
	_, err = store.Get(t.Context(), name)
	if !errors.Is(err, secrets.ErrAuthorityUnreachable) {
		t.Fatalf("unresolved alias miss = %v, want unreachable", err)
	}
	if errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("unresolved alias miss was reported as erasure")
	}
}

func TestPinnedAbsenceMayBeErasure(t *testing.T) {
	fixture(t)
	backend, err := secrets.NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	if backend.Origin().Persistent == "" {
		t.Fatal("login collection was not recorded")
	}
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	store := bookmarks.NewStore(d, backend)
	saved, err := store.Save(t.Context(), bookmarks.Bookmark{
		DisplayName: "pin", AuthURL: "https://swift.test/auth/v1.0", Username: "test",
	}, []byte("erase-me"))
	if err != nil {
		t.Fatal(err)
	}
	ref := publishedRef(t, d, saved.ID)
	if err := backend.Delete(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.ConnectionConfig(t.Context(), saved.ID)
	if !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("pinned absence = %v, want erasure", err)
	}
	if errors.Is(err, secrets.ErrAuthorityUnreachable) {
		t.Fatal("pinned absence was reported as unreachable")
	}
}

func TestCancelledLinuxSecretServiceCallHonoursContext(t *testing.T) {
	fixture(t)
	store, err := secrets.NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.Get(ctx, "fixture/cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get = %v, want canceled", err)
	}
	if err := store.Put(ctx, "fixture/cancelled", []byte("v")); !errors.Is(err, context.Canceled) || !errors.Is(err, secrets.ErrUnresolved) {
		t.Fatalf("Put = %v, want canceled unresolved", err)
	}
	if err := store.Delete(ctx, "fixture/cancelled"); !errors.Is(err, context.Canceled) || !errors.Is(err, secrets.ErrUnresolved) {
		t.Fatalf("Delete = %v, want canceled unresolved", err)
	}
}

func (s *secretService) valueInByValue(collection dbus.ObjectPath, value string) (string, bool) {
	encoded := base64.StdEncoding.EncodeToString([]byte(value))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item.collection == collection && item.value == encoded {
			return item.attributes["username"], true
		}
	}
	return "", false
}

func bookmarkPersistent(t *testing.T, d *db.DB, id string) string {
	t.Helper()
	var persistent string
	if err := d.Reader().QueryRow(`SELECT origin_persistent FROM bookmarks WHERE id=?`, id).Scan(&persistent); err != nil {
		t.Fatal(err)
	}
	return persistent
}

func publishedRef(t *testing.T, d *db.DB, id string) string {
	t.Helper()
	var ref string
	if err := d.Reader().QueryRow(`SELECT secret_ref FROM bookmarks WHERE id=?`, id).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	return ref
}
