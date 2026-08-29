package secrets

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// requireKeychain skips — never fakes — when no OS keychain is
// reachable (§65.1 item 3). Locally and in CI a real Secret Service
// (e.g. gnome-keyring under dbus-run-session) exercises this for real.
func requireKeychain(t *testing.T) *KeychainStore {
	t.Helper()
	store, err := NewKeychainStore()
	if err != nil {
		t.Skipf("no OS keychain reachable: %v", err)
	}
	return store
}

func TestKeychainRoundTrip(t *testing.T) {
	store := requireKeychain(t)
	ctx := context.Background()
	const name = "test/keychain-roundtrip"
	t.Cleanup(func() { _ = store.Delete(ctx, name) })

	// Binary-unfriendly bytes must survive the string-shaped keyring API.
	value := []byte{0x00, 0xff, 'k', 'e', 'y', 0x0a, 0x80}
	if err := store.Put(ctx, name, value); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, value) {
		t.Fatalf("Get = %x, want %x", got, value)
	}

	// Overwrite replaces.
	if err := store.Put(ctx, name, []byte("rotated")); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, name)
	if err != nil || string(got) != "rotated" {
		t.Fatalf("after rotate: %q, %v", got, err)
	}

	if err := store.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestOpenDefaultAlwaysWorks(t *testing.T) {
	store, backend := OpenDefault()
	if backend != BackendKeychain && backend != BackendSession {
		t.Fatalf("backend = %q", backend)
	}
	ctx := context.Background()
	const name = "test/open-default"
	t.Cleanup(func() { _ = store.Delete(ctx, name) })

	if err := store.Put(ctx, name, []byte("v")); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, name)
	if err != nil || string(got) != "v" {
		t.Fatalf("round-trip on %q backend failed: %q, %v", backend, got, err)
	}
}
