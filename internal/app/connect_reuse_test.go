package app

import (
	"context"
	"testing"
)

// Switching back to a bookmark whose session is still cached under its
// current generation neither signs in nor reads /info again, while the
// portrait is read on every connect; a changed bookmark signs in again, an
// expired token renews itself, and a revoked key fails the connect without
// changing which bookmark is active (ADR 0110 decision 1).
func TestConnectReusesACurrentSession(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	first := h.connected
	other, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "Other", AuthURL: h.authURL, Username: "test:tester"}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.browse.Connect(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	auth, info, storage := h.mock.AuthRequests(), h.mock.InfoRequests(), h.mock.Requests()

	if _, err := h.browse.Connect(ctx, first); err != nil {
		t.Fatal(err)
	}
	if h.mock.AuthRequests() != auth || h.mock.InfoRequests() != info {
		t.Fatalf("switching back signed in %d and read /info %d more times, want neither", h.mock.AuthRequests()-auth, h.mock.InfoRequests()-info)
	}
	if h.mock.Requests() == storage {
		t.Fatal("switching back did not read the account portrait")
	}
	if got := h.browse.connections.ActiveBookmarkID(); got != first {
		t.Fatalf("active bookmark %q, want %q", got, first)
	}

	// A changed bookmark drops its session, so the next connect signs in.
	list, err := h.bookmarks.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var renamed BookmarkDTO
	for _, b := range list {
		if b.ID == first {
			renamed = b
		}
	}
	renamed.DisplayName = "Mock renamed"
	if _, err := h.bookmarks.Save(ctx, renamed, ""); err != nil {
		t.Fatal(err)
	}
	auth = h.mock.AuthRequests()
	if _, err := h.browse.Connect(ctx, first); err != nil {
		t.Fatal(err)
	}
	if h.mock.AuthRequests() != auth+1 {
		t.Fatalf("a changed bookmark signed in %d times on connect, want once", h.mock.AuthRequests()-auth)
	}

	// An expired token renews on the reused session's first 401.
	h.mock.ExpireToken()
	auth = h.mock.AuthRequests()
	if _, err := h.browse.Connect(ctx, other.ID); err != nil {
		t.Fatalf("a reused session with an expired token: %v", err)
	}
	if h.mock.AuthRequests() != auth+1 {
		t.Fatalf("an expired token renewed with %d sign-ins, want one", h.mock.AuthRequests()-auth)
	}

	// A key revoked on the cluster fails the connect once the token goes,
	// at the portrait read after the reused session became active, and the
	// connect is undone: the bookmark active before stays active.
	h.mock.RevokeKey()
	h.mock.ExpireToken()
	if _, err := h.browse.Connect(ctx, first); err == nil {
		t.Fatal("a revoked key connected")
	}
	if got := h.browse.connections.ActiveBookmarkID(); got != other.ID {
		t.Fatalf("after a failed connect the active bookmark is %q, want the earlier %q", got, other.ID)
	}
}
