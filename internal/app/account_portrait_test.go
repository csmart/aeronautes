package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarks"
)

// Independent HTTP accounts make a late global account lookup observable:
// alpha's held HEAD must not inherit beta's identity after a Connect.
type portraitAccountFixture struct {
	url     string
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
	heads   atomic.Int64
}

func newPortraitAccountFixture(t *testing.T, name string, containers, maxFileSize int64) *portraitAccountFixture {
	t.Helper()
	f := &portraitAccountFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v1.0":
			if r.Header.Get("X-Auth-User") != name+":reader" || r.Header.Get("X-Auth-Key") != "private-fixture-key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("X-Storage-Url", f.url+"/v1/AUTH_"+name)
			w.Header().Set("X-Auth-Token", name+"-fixture-token")
			w.WriteHeader(http.StatusOK)
		case "/info":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"swift":{"max_file_size":%d,"policies":[{"name":%q,"default":true}]}}`, maxFileSize, name)
		case "/v1/AUTH_" + name:
			f.heads.Add(1)
			if r.Method != http.MethodHead || r.Header.Get("X-Auth-Token") != name+"-fixture-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.mu.Lock()
			entered, release := f.entered, f.release
			f.entered = nil
			f.mu.Unlock()
			if entered != nil {
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			w.Header().Set("X-Account-Container-Count", fmt.Sprint(containers))
			w.Header().Set("X-Account-Object-Count", fmt.Sprint(containers*10))
			w.Header().Set("X-Account-Bytes-Used", fmt.Sprint(containers*100))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	f.url = server.URL
	t.Cleanup(server.Close)
	return f
}

func savePortraitBookmark(t *testing.T, h *harness, account *portraitAccountFixture, name string) string {
	t.Helper()
	bookmark, err := h.bookmarks.Save(t.Context(), BookmarkDTO{DisplayName: name,
		AuthURL: account.url + "/auth/v1.0", Username: name + ":reader"}, "private-fixture-key")
	if err != nil {
		t.Fatal(err)
	}
	return bookmark.ID
}

func (f *portraitAccountFixture) holdNextHEAD(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.entered, f.release = entered, release
	f.mu.Unlock()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	return entered, unblock
}

func TestAccountPortraitKeepsCapturedAccount(t *testing.T) {
	for _, switchAccount := range []bool{false, true} {
		t.Run(fmt.Sprintf("switch=%t", switchAccount), func(t *testing.T) {
			h := newHarness(t)
			alpha := newPortraitAccountFixture(t, "alpha", 11, 111)
			beta := newPortraitAccountFixture(t, "beta", 22, 222)
			alphaID := savePortraitBookmark(t, h, alpha, "alpha")
			betaID := savePortraitBookmark(t, h, beta, "beta")
			entered, unblock := alpha.holdNextHEAD(t)
			defer unblock()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			type result struct {
				portrait AccountDTO
				err      error
			}
			done := make(chan result, 1)
			go func() {
				portrait, err := h.browse.Connect(ctx, alphaID)
				done <- result{portrait, err}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("alpha account HEAD was not reached")
			}
			if switchAccount {
				if _, err := h.browse.Connect(ctx, betaID); err != nil {
					t.Fatal(err)
				}
			}
			unblock()
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			if got.portrait.Name != "AUTH_alpha" || got.portrait.Containers != 11 {
				t.Fatalf("Connect(alpha) mixed account identity: name=%q containers=%d; want AUTH_alpha and11", got.portrait.Name, got.portrait.Containers)
			}
			if got.portrait.Scope != (ListingScopeDTO{BookmarkID: alphaID, Generation: 1}) {
				t.Fatalf("held alpha reply lost its captured scope: %+v", got.portrait.Scope)
			}
		})
	}
}

func TestAccountPortraitKeepsGenerationAfterResave(t *testing.T) {
	h := newHarness(t)
	alpha := newPortraitAccountFixture(t, "alpha", 11, 111)
	beta := newPortraitAccountFixture(t, "beta", 22, 222)
	id := savePortraitBookmark(t, h, alpha, "alpha")
	if _, err := h.browse.Connect(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	entered, unblock := alpha.holdNextHEAD(t)
	defer unblock()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type result struct {
		portrait AccountDTO
		err      error
	}
	done := make(chan result, 1)
	go func() {
		portrait, err := h.browse.Account(ctx, id)
		done <- result{portrait, err}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("alpha account HEAD was not reached")
	}
	if _, err := h.bookmarks.Save(ctx, BookmarkDTO{ID: id, DisplayName: "replacement", AuthURL: beta.url + "/auth/v1.0", Username: "beta:reader"}, "private-fixture-key"); err != nil {
		t.Fatal(err)
	}
	replacement, err := h.browse.Connect(ctx, id)
	if err != nil || replacement.Name != "AUTH_beta" || replacement.Scope.Generation != 2 {
		t.Fatalf("replacement portrait = %+v, %v", replacement, err)
	}
	unblock()
	got := <-done
	if got.err != nil || got.portrait.Name != "AUTH_alpha" || got.portrait.Containers != 11 || got.portrait.Scope != (ListingScopeDTO{BookmarkID: id, Generation: 1}) {
		t.Fatalf("held portrait after re-save = %+v, %v", got.portrait, got.err)
	}
}

func TestAccountReadsRequireCurrentBookmark(t *testing.T) {
	reads := []struct {
		name  string
		read  func(*BrowseService, context.Context, string) (any, error)
		empty any
	}{
		{"portrait", func(s *BrowseService, ctx context.Context, id string) (any, error) { return s.Account(ctx, id) }, AccountDTO{}},
		{"capabilities", func(s *BrowseService, ctx context.Context, id string) (any, error) { return s.Capabilities(ctx, id) }, CapabilitiesDTO{}},
	}
	cases := []struct {
		name string
		want error
	}{
		{"disconnected", ErrNotConnected},
		{"empty bookmark", ErrNotConnected},
		{"another active bookmark", ErrAccountScopeChanged},
		{"deleted bookmark", ErrNotConnected},
		{"saved bookmark", ErrNotConnected},
		{"stored generation changed", bookmarks.ErrChanged},
		{"stored bookmark deleted", bookmarks.ErrNotFound},
	}
	for _, read := range reads {
		for _, tc := range cases {
			t.Run(read.name+"/"+tc.name, func(t *testing.T) {
				h := newHarness(t)
				alpha := newPortraitAccountFixture(t, "alpha", 11, 111)
				beta := newPortraitAccountFixture(t, "beta", 22, 222)
				id := savePortraitBookmark(t, h, alpha, "alpha")
				ctx := t.Context()
				if tc.name != "disconnected" {
					if _, err := h.browse.Connect(ctx, id); err != nil {
						t.Fatal(err)
					}
				}
				requested := id
				switch tc.name {
				case "empty bookmark":
					requested = ""
				case "another active bookmark":
					other := savePortraitBookmark(t, h, beta, "beta")
					if _, err := h.browse.Connect(ctx, other); err != nil {
						t.Fatal(err)
					}
				case "deleted bookmark":
					if err := h.bookmarks.Delete(ctx, id); err != nil {
						t.Fatal(err)
					}
				case "saved bookmark", "stored generation changed":
					stored, err := h.bookmarks.store.Get(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					if tc.name == "saved bookmark" {
						_, err = h.bookmarks.Save(ctx, toBookmarkDTO(stored), "private-fixture-key")
					} else {
						// Bypass service invalidation to exercise the stored-generation
						// guard, not merely the absence of an active connection.
						_, err = h.bookmarks.store.Save(ctx, stored, []byte("private-fixture-key"))
					}
					if err != nil {
						t.Fatal(err)
					}
				case "stored bookmark deleted":
					if err := h.bookmarks.store.Delete(ctx, id); err != nil {
						t.Fatal(err)
					}
				}
				beforeAlpha, beforeBeta := alpha.heads.Load(), beta.heads.Load()
				got, err := read.read(h.browse, ctx, requested)
				if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, read.empty) {
					t.Fatalf("refused %s = %+v, %v; want zero reply and %v", read.name, got, err, tc.want)
				}
				if alpha.heads.Load() != beforeAlpha || beta.heads.Load() != beforeBeta {
					t.Fatal("refused read reached an account HEAD")
				}
			})
		}
	}
}

func TestAccountCapabilitiesKeepTheirCapturedScope(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		name string
		max  int64
	}{{"alpha", 111}, {"beta", 222}} {
		account := newPortraitAccountFixture(t, tc.name, 11, tc.max)
		id := savePortraitBookmark(t, h, account, tc.name)
		portrait, err := h.browse.Connect(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		caps, err := h.browse.Capabilities(t.Context(), id)
		if err != nil || caps.MaxFileSize != tc.max || len(caps.StoragePolicies) != 1 || caps.StoragePolicies[0].Name != tc.name || caps.Scope != portrait.Scope || caps.Scope.BookmarkID != id || caps.Scope.Generation <= 0 {
			t.Fatalf("%s capability snapshot = %+v, %v; portrait scope %+v", tc.name, caps, err, portrait.Scope)
		}
	}
}

func TestAccountCapabilitiesUnknownRemainsScoped(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) {
			h := newHarnessWithInfo(t, "not a readable info document")
			if disabled {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/info" {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					h.mock.ServeHTTP(w, r)
				}))
				t.Cleanup(server.Close)
				h.authURL = server.URL + "/auth/v1.0"
			}
			h.connect(t)
			id := h.bookmarkID()
			caps, err := h.browse.Capabilities(t.Context(), id)
			if err != nil || caps.Source != "unknown" || caps.Scope != (ListingScopeDTO{BookmarkID: id, Generation: 1}) || caps.MaxFileSize != 0 || len(caps.StoragePolicies) != 0 {
				t.Fatalf("unknown capability snapshot = %+v, %v", caps, err)
			}
		})
	}
}
