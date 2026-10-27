package app

import (
	"context"
	"strings"
	"testing"
)

// Verify answers for the details as typed and persists nothing: the right
// key reports the account, a wrong one is refused, and the bookmark list is
// untouched either way. A saved bookmark's stored key serves a verify only
// while its binding is unchanged, the rule Save applies to an edit.
func TestBookmarkVerify(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	typed := BookmarkDTO{DisplayName: "Typed", AuthURL: h.authURL, Username: "test:tester", TLSMode: "system"}

	result, err := h.bookmarks.Verify(ctx, typed, "testing")
	if err != nil {
		t.Fatalf("verify with the right key: %v", err)
	}
	if result.Account != "AUTH_test" {
		t.Errorf("account = %q, want AUTH_test", result.Account)
	}
	if result.Containers <= 0 {
		t.Errorf("containers = %d, want the fixture's count", result.Containers)
	}

	if _, err := h.bookmarks.Verify(ctx, typed, "wrong"); err == nil {
		t.Error("a wrong key was not refused")
	}
	if _, err := h.bookmarks.Verify(ctx, typed, ""); err == nil || !strings.Contains(err.Error(), "enter the API key") {
		t.Errorf("a new bookmark without a key: %v, want a request for the key", err)
	}

	list, err := h.bookmarks.List(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("verify saved something: %d bookmarks (%v)", len(list), err)
	}

	// A public endpoint over plain HTTP is refused by the endpoint policy
	// before any request is made, exactly as a save would be.
	if _, err := h.bookmarks.Verify(ctx, BookmarkDTO{AuthURL: "http://203.0.113.5/auth/v1.0", Username: "a:b", TLSMode: "system"}, "k"); err == nil {
		t.Error("public HTTP auth URL was not refused")
	}
}

func TestBookmarkVerifyUsesTheStoredKeyOnlyForAnUnchangedBinding(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	saved, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "Saved", AuthURL: h.authURL, Username: "test:tester", TLSMode: "system"}, "testing")
	if err != nil {
		t.Fatal(err)
	}

	// The same binding, no key typed: the stored key answers.
	if result, err := h.bookmarks.Verify(ctx, saved, ""); err != nil || result.Account != "AUTH_test" {
		t.Fatalf("stored key with an unchanged binding: %+v, %v", result, err)
	}
	// A renamed bookmark is the same binding.
	renamed := saved
	renamed.DisplayName = "Renamed"
	if _, err := h.bookmarks.Verify(ctx, renamed, ""); err != nil {
		t.Errorf("a display-name change must not demand the key: %v", err)
	}
	// A changed user is not: the stored key may not reach it.
	changed := saved
	changed.Username = "other:user"
	if _, err := h.bookmarks.Verify(ctx, changed, ""); err == nil || !strings.Contains(err.Error(), "re-enter the API key") {
		t.Errorf("a changed binding without a key: %v, want a re-entry request", err)
	}
	// With the key typed, the typed details are what is verified.
	if _, err := h.bookmarks.Verify(ctx, changed, "testing"); err == nil {
		t.Error("the mock knows no other:user; the typed details were not what was verified")
	}

	list, err := h.bookmarks.List(ctx)
	if err != nil || len(list) != 1 || list[0].DisplayName != "Saved" {
		t.Fatalf("verify changed the saved bookmark: %+v (%v)", list, err)
	}
}
