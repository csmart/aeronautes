package bookmarks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func newTestStore(t *testing.T) (*Store, *db.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return NewStore(d, secrets.NewSessionStore()), d, path
}

func sample() Bookmark {
	return Bookmark{
		DisplayName: "Prod AU",
		AuthURL:     "https://swift.example/auth/v1.0",
		Username:    "acct:user",
	}
}

func TestSaveGetRoundTrip(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()

	saved, err := st.Save(ctx, sample(), []byte("swift-key"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.CreatedAt.IsZero() || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved bookmark missing id/timestamps: %+v", saved)
	}
	if !saved.HasCredential {
		t.Fatal("saved bookmark should report a credential on record")
	}
	if saved.TLSMode != swiftcontrol.TLSModeSystem || saved.SegmentPlacement != PlacementRcloneDefault {
		t.Fatalf("defaults not applied: %+v", saved)
	}

	got, err := st.Get(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != saved {
		t.Fatalf("Get = %+v, want %+v", got, saved)
	}

	key, err := st.Credential(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, []byte("swift-key")) {
		t.Fatalf("Credential = %q", key)
	}
}

type unresolvedPutStore struct{ secrets.SecretStore }

func (s unresolvedPutStore) Put(context.Context, string, []byte) error {
	return fmt.Errorf("abandoned write: %w", secrets.ErrUnresolved)
}

func TestSaveUnresolvedPutKeepsUnusedKeyWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st := NewStore(d, unresolvedPutStore{secrets.NewSessionStore()})
	_, err = st.Save(context.Background(), sample(), []byte("swift-key"))
	if err == nil || !strings.Contains(err.Error(), "an unused key may remain in credential storage") {
		t.Fatalf("unresolved Put = %v", err)
	}
}

func TestUpdateKeepsCredentialAndCreatedAt(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()

	saved, err := st.Save(ctx, sample(), []byte("swift-key"))
	if err != nil {
		t.Fatal(err)
	}

	saved.DisplayName = "Prod AU (renamed)"
	updated, err := st.Save(ctx, saved, nil) // nil secret: keep existing
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Prod AU (renamed)" {
		t.Fatalf("DisplayName = %q", updated.DisplayName)
	}
	if !updated.CreatedAt.Equal(saved.CreatedAt) {
		t.Fatal("update must not change created_at")
	}
	if !updated.HasCredential {
		t.Fatal("update with nil secret must keep the credential")
	}
	key, err := st.Credential(ctx, updated.ID)
	if err != nil || !bytes.Equal(key, []byte("swift-key")) {
		t.Fatalf("credential lost on update: %q, %v", key, err)
	}

	if _, err := st.Save(ctx, updated, []byte("rotated")); err != nil {
		t.Fatal(err)
	}
	key, err = st.Credential(ctx, updated.ID)
	if err != nil || !bytes.Equal(key, []byte("rotated")) {
		t.Fatalf("credential not rotated: %q, %v", key, err)
	}
}

func TestListOrdersByDisplayName(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()
	for _, name := range []string{"zeta", "Alpha", "midd"} {
		b := sample()
		b.DisplayName = name
		if _, err := st.Save(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].DisplayName != "Alpha" || list[1].DisplayName != "midd" || list[2].DisplayName != "zeta" {
		t.Fatalf("List order = %v", list)
	}
	if list[0].HasCredential {
		t.Fatal("bookmark saved without a secret must report no credential")
	}
}

func TestDeleteRemovesCredential(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()
	saved, err := st.Save(ctx, sample(), []byte("swift-key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v, want ErrNotFound", err)
	}
	if _, err := st.Credential(ctx, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Credential after delete: %v, want ErrNotFound", err)
	}
	if err := st.Delete(ctx, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete: %v, want ErrNotFound", err)
	}
}

func TestValidation(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()

	cases := []func(*Bookmark){
		func(b *Bookmark) { b.DisplayName = "" },
		func(b *Bookmark) { b.AuthURL = "" },
		func(b *Bookmark) { b.Username = "" },
		func(b *Bookmark) { b.TLSMode = "yolo" },
		func(b *Bookmark) { b.SegmentPlacement = "wherever" },
		func(b *Bookmark) { b.LargeObjects = "multipart" },
	}
	for i, mutate := range cases {
		b := sample()
		mutate(&b)
		if _, err := st.Save(ctx, b, nil); err == nil {
			t.Errorf("case %d: invalid bookmark saved", i)
		}
	}
}

// A bookmark's large-object kind is kept as saved, and one saved without a
// value, as every bookmark before ADR 0111 was, reads back with none, which
// an upload reads as static. Changing it is not a credential change, so the
// bookmark's generation stays and its sessions remain valid.
func TestLargeObjectsRoundTrip(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()
	saved, err := st.Save(ctx, sample(), []byte("key"))
	if err != nil {
		t.Fatal(err)
	}
	_, before, err := st.ConnectionConfig(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := st.Get(ctx, saved.ID); err != nil || got.LargeObjects != "" {
		t.Fatalf("a bookmark saved without a kind reads %q, %v", got.LargeObjects, err)
	}
	for _, kind := range []string{"dlo", "slo", ""} {
		saved.LargeObjects = kind
		if _, err := st.Save(ctx, saved, nil); err != nil {
			t.Fatal(err)
		}
		if got, err := st.Get(ctx, saved.ID); err != nil || got.LargeObjects != kind {
			t.Fatalf("saved %q, read %q, %v", kind, got.LargeObjects, err)
		}
	}
	if _, after, err := st.ConnectionConfig(ctx, saved.ID); err != nil || after != before {
		t.Errorf("generation %d became %d (%v) for a large-object change", before, after, err)
	}
}

func TestSavedEndpointPolicy(t *testing.T) {
	cases := []struct {
		name, endpoint string
		mode           swiftcontrol.TLSMode
		allowed        bool
	}{
		{"https", "https://swift.example/v1/AUTH_test", swiftcontrol.TLSModeSystem, true},
		{"loopback", "http://127.0.0.1/v1/AUTH_test", swiftcontrol.TLSModeSystem, true},
		{"private without opt in", "http://10.0.0.1/v1/AUTH_test", swiftcontrol.TLSModeSystem, false},
		{"private with opt in", "http://10.0.0.1/v1/AUTH_test", swiftcontrol.TLSModeInsecure, true},
		{"public insecure", "http://192.0.2.10/v1/AUTH_test", swiftcontrol.TLSModeInsecure, false},
		{"missing hostname", "https://:443/v1/AUTH_test", swiftcontrol.TLSModeSystem, false},
		{"empty query", "https://swift.example/v1/AUTH_test?", swiftcontrol.TLSModeSystem, false},
	}
	for _, tc := range cases {
		for _, override := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/override=%t", tc.name, override), func(t *testing.T) {
				st, _, _ := newTestStore(t)
				b := sample()
				b.TLSMode = tc.mode
				if override {
					b.AuthURL = "http://127.0.0.1/auth/v1.0"
					b.StorageURLOverride = tc.endpoint
				} else {
					b.AuthURL = tc.endpoint
				}
				saved, err := st.Save(context.Background(), b, []byte("synthetic-key"))
				if (err == nil) != tc.allowed {
					t.Fatalf("Save error = %v, allowed = %t", err, tc.allowed)
				}
				if tc.allowed {
					if _, err := swiftcontrol.New(saved.SwiftConfig([]byte("synthetic-key"), nil)); err != nil {
						t.Fatalf("saved endpoint refused by session: %v", err)
					}
				} else {
					rows, err := st.List(context.Background())
					if err != nil || len(rows) != 0 {
						t.Fatalf("invalid bookmark persisted: count=%d, error=%v", len(rows), err)
					}
				}
			})
		}
	}
}

// TestSecretNeverTouchesDatabaseFile is K-SEC-003 behaviourally: after
// saving a bookmark with a known credential, the raw database file must
// not contain the secret bytes.
func TestSecretNeverTouchesDatabaseFile(t *testing.T) {
	st, d, path := newTestStore(t)
	ctx := context.Background()

	const secret = "SUPERSECRET-swift-key-983127"
	if _, err := st.Save(ctx, sample(), []byte(secret)); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil { // checkpoint WAL into the main file
		t.Fatal(err)
	}

	for _, file := range []string{path, path + "-wal"} {
		raw, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("%s contains the credential — secrets must never reach SQLite (K-SEC-003)", file)
		}
	}
}

func TestSwiftConfig(t *testing.T) {
	b := Bookmark{
		AuthURL:            "https://swift.example/auth/v1.0",
		Username:           "acct:user",
		StorageURLOverride: "https://swift.example/v1/AUTH_other",
		TLSMode:            swiftcontrol.TLSModeCustomCA,
	}
	cfg := b.SwiftConfig([]byte("key"), []byte("pem"))
	if cfg.AuthURL != b.AuthURL || cfg.Username != b.Username ||
		cfg.APIKey != "key" || cfg.StorageURLOverride != b.StorageURLOverride {
		t.Fatalf("SwiftConfig = %+v", cfg)
	}
	if cfg.TLS.Mode != swiftcontrol.TLSModeCustomCA || string(cfg.TLS.CACertPEM) != "pem" {
		t.Fatalf("TLS config = %+v", cfg.TLS)
	}
}
