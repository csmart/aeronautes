package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/secrets"
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

// A saved key verifies under the generation it was acquired with: a key
// replaced or a bookmark deleted while the authentication is still held
// must not have the account HEAD dispatched afterwards (ADR 0015).
func TestBookmarkVerifyRefusesAfterRevocationDuringAuthentication(t *testing.T) {
	for _, revoke := range []string{"key replaced", "deleted"} {
		t.Run(revoke, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			arrived := make(chan struct{}, 1)
			release := make(chan struct{})
			var heads atomic.Int64
			gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/auth/v1.0" {
					select {
					case arrived <- struct{}{}:
					default:
					}
					<-release
				}
				if strings.HasPrefix(r.URL.Path, "/v1/AUTH_test") {
					heads.Add(1)
				}
				h.mock.ServeHTTP(w, r)
			}))
			t.Cleanup(gate.Close)
			saved, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "Held", AuthURL: gate.URL + "/auth/v1.0", Username: "test:tester", TLSMode: "system"}, "testing")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := h.bookmarks.Verify(ctx, saved, "")
				done <- err
			}()
			select {
			case <-arrived:
			case <-time.After(10 * time.Second):
				t.Fatal("the authentication request never arrived")
			}
			switch revoke {
			case "key replaced":
				if _, err := h.bookmarks.Save(ctx, saved, "testing-replacement"); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if err := h.bookmarks.Delete(ctx, saved.ID); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			if err := <-done; err == nil {
				t.Fatal("verify succeeded under a revoked generation")
			}
			if heads.Load() != 0 {
				t.Errorf("the account HEAD was dispatched %d time(s) after revocation", heads.Load())
			}
		})
	}
}

// The binding a saved key is compared against is the configuration acquired
// with it: the same CA bytes pasted again are the same trust, as Save
// treats them, and changed bytes are a changed trust.
func TestBookmarkVerifyComparesTheAcquiredTrust(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, endpoint, ca := tlsMock(t)
	saved, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "custom trust", AuthURL: endpoint + "/auth/v1.0",
		Username: "test:tester", TLSMode: "custom_ca", CAPem: ca}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	same := saved
	same.CAPem = ca
	if result, err := h.bookmarks.Verify(ctx, same, ""); err != nil || result.Account != "AUTH_test" {
		t.Fatalf("identical CA bytes with the saved key: %+v, %v", result, err)
	}
	changed := saved
	changed.CAPem = ca + "\n"
	if _, err := h.bookmarks.Verify(ctx, changed, ""); !errors.Is(err, errVerifyKeyReentry) {
		t.Fatalf("changed CA bytes with the saved key: %v, want a re-entry request", err)
	}
}

// A saved custom-CA bundle serves a typed key even when the saved key is no
// longer readable, as Save keeps the bundle when the PEM is left empty.
func TestBookmarkVerifyUsesTheSavedCAWithATypedKey(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, endpoint, ca := tlsMock(t)
	saved, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "custom trust", AuthURL: endpoint + "/auth/v1.0",
		Username: "test:tester", TLSMode: "custom_ca", CAPem: ca}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	// A fresh session store over the same database is a restart with the
	// session-only key gone.
	restarted := bookmarks.NewStore(h.db, secrets.NewSessionStore())
	service := NewBookmarkService(restarted, NewConnections(restarted), nil)
	if _, err := service.Verify(ctx, saved, ""); err == nil {
		t.Fatal("a lost key verified")
	}
	if result, err := service.Verify(ctx, saved, "testing"); err != nil || result.Account != "AUTH_test" {
		t.Fatalf("typed key with the saved CA bundle: %+v, %v", result, err)
	}
	if _, err := service.Verify(ctx, saved, "wrong"); err == nil {
		t.Fatal("a wrong typed key verified")
	}
}
