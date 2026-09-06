package bookmarks

import (
	"context"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func TestStoredCredentialBindingChangesRequireReentry(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Bookmark)
	}{
		{"scheme", func(b *Bookmark) { b.AuthURL = "https://127.0.0.1:8080/auth/v1.0" }},
		{"host", func(b *Bookmark) { b.AuthURL = "http://127.0.0.2:8080/auth/v1.0" }},
		{"port", func(b *Bookmark) { b.AuthURL = "http://127.0.0.1:8081/auth/v1.0" }},
		{"path", func(b *Bookmark) { b.AuthURL = "http://127.0.0.1:8080/other/auth/v1.0" }},
		{"username", func(b *Bookmark) { b.Username = "other:user" }},
		{"storage override", func(b *Bookmark) { b.StorageURLOverride = "http://127.0.0.1:8080/v1/AUTH_other" }},
		{"trust", func(b *Bookmark) { b.TLSMode = swiftcontrol.TLSModeInsecure }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _, _ := newTestStore(t)
			ctx := context.Background()
			b := sample()
			b.AuthURL = "http://127.0.0.1:8080/auth/v1.0"
			before, err := st.Save(ctx, b, []byte("original"))
			if err != nil {
				t.Fatal(err)
			}
			edited := before
			edited.HasCredential = false
			edited.DisplayName = "also changed"
			tc.edit(&edited)
			if _, err := st.Save(ctx, edited, nil); err == nil || !strings.Contains(err.Error(), "re-enter") {
				t.Errorf("binding change returned %v, want key reentry refusal", err)
			}
			after, err := st.Get(ctx, before.ID)
			if err != nil || after != before {
				t.Errorf("refused edit changed the bookmark: %v", err)
			}
			if key, err := st.Credential(ctx, before.ID); err != nil || string(key) != "original" {
				t.Errorf("refused edit changed the stored key: %v", err)
			}
			if _, err := st.Save(ctx, edited, []byte("replacement")); err != nil {
				t.Fatalf("explicit reentry failed: %v", err)
			}
			if key, err := st.Credential(ctx, before.ID); err != nil || string(key) != "replacement" {
				t.Errorf("replacement key was not saved: %v", err)
			}
		})
	}
}

func TestCosmeticEditKeepsCredentialBinding(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()
	b, err := st.Save(ctx, sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	b.DisplayName = "renamed"
	b.PerformanceProfile = "lan"
	b.SegmentPlacement = PlacementSidecar
	b.ChunkSizeBytes = 1024
	b.TLSMode = ""
	b.HasCredential = false
	saved, err := st.Save(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.HasCredential || saved.TLSMode != swiftcontrol.TLSModeSystem || saved.DisplayName != b.DisplayName ||
		saved.PerformanceProfile != b.PerformanceProfile || saved.SegmentPlacement != b.SegmentPlacement || saved.ChunkSizeBytes != b.ChunkSizeBytes {
		t.Fatalf("cosmetic edit was not retained: %+v", saved)
	}
	if key, err := st.Credential(ctx, b.ID); err != nil || string(key) != "original" {
		t.Fatalf("cosmetic edit lost the stored key: %v", err)
	}
}

func TestCallerCannotAssertCredentialPresence(t *testing.T) {
	st, _, _ := newTestStore(t)
	b := sample()
	b.HasCredential = true
	saved, err := st.Save(context.Background(), b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if saved.HasCredential {
		t.Fatal("caller created a credential reference without a key")
	}
	saved.AuthURL = "https://other.example/auth/v1.0"
	if _, err := st.Save(context.Background(), saved, nil); err != nil {
		t.Fatalf("keyless bookmark cannot change identity: %v", err)
	}
}

func TestStaleEditCannotRestorePreviousCredentialBinding(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()
	stale, err := st.Save(ctx, sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	replacement := stale
	replacement.AuthURL = "https://replacement.example/auth/v1.0"
	current, err := st.Save(ctx, replacement, []byte("replacement"))
	if err != nil {
		t.Fatal(err)
	}
	stale.DisplayName = "stale editor rename"
	if _, err := st.Save(ctx, stale, nil); err == nil {
		t.Fatal("stale editor restored the old identity with the replacement key")
	}
	if after, err := st.Get(ctx, current.ID); err != nil || after != current {
		t.Fatalf("stale edit changed the published binding: %v", err)
	}
}
