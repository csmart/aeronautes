package app

import (
	"context"
	"testing"
)

// An import's verification flag survives an edit that verifies nothing and
// a check of typed details, and is cleared by verifying the saved bookmark
// with its saved key or by connecting it (ADR 0114 decision 9).
func TestVerifyFailureFlagLifetime(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	store := h.bookmarks.store
	saved, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "Imported", AuthURL: h.authURL, Username: "test:tester"}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	const reason = "the identity service refused these credentials"
	flag := func() string {
		t.Helper()
		list, err := h.bookmarks.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range list {
			if b.ID == saved.ID {
				return b.VerifyFailure
			}
		}
		t.Fatal("the bookmark is gone")
		return ""
	}
	_, generation, err := store.ConnectionConfig(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetVerifyFailure(ctx, saved.ID, reason); err != nil {
		t.Fatal(err)
	}
	if got := flag(); got != reason {
		t.Fatalf("flag = %q, want %q", got, reason)
	}
	if _, after, err := store.ConnectionConfig(ctx, saved.ID); err != nil || after != generation {
		t.Fatalf("setting the flag moved the generation from %d to %d (%v); it is not part of the binding", generation, after, err)
	}

	saved.DisplayName = "Imported renamed"
	if saved, err = h.bookmarks.Save(ctx, saved, ""); err != nil {
		t.Fatal(err)
	}
	if got := flag(); got != reason {
		t.Fatalf("after a rename the flag = %q, want it kept", got)
	}
	if _, err := h.bookmarks.Verify(ctx, saved, "testing"); err != nil {
		t.Fatal(err)
	}
	if got := flag(); got != reason {
		t.Fatalf("after verifying typed details the flag = %q, want it kept", got)
	}
	if _, err := h.bookmarks.Verify(ctx, saved, ""); err != nil {
		t.Fatal(err)
	}
	if got := flag(); got != "" {
		t.Fatalf("after verifying the saved bookmark the flag = %q, want it cleared", got)
	}

	if err := store.SetVerifyFailure(ctx, saved.ID, reason); err != nil {
		t.Fatal(err)
	}
	if _, err := h.browse.Connect(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if got := flag(); got != "" {
		t.Fatalf("after connecting the flag = %q, want it cleared", got)
	}
}
