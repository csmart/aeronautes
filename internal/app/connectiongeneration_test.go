package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func connectionGate(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return gate, release
}

func awaitConnection[T any](t *testing.T, result <-chan T) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("connection barrier timed out")
		var zero T
		return zero
	}
}

func TestBookmarkChangeRefusesDelayedSessionPublication(t *testing.T) {
	for _, call := range []string{"connect", "session-for"} {
		for _, phase := range []string{"authentication", "capabilities"} {
			if call == "session-for" && phase == "capabilities" {
				continue
			}
			for _, change := range []string{"replace", "delete", "cosmetic", "unavailable"} {
				t.Run(call+"/"+phase+"/"+change, func(t *testing.T) {
					h := newHarness(t)
					c := h.transfer.connections
					entered := make(chan struct{}, 1)
					gate, release := connectionGate(t)
					var held atomic.Bool
					var capabilities atomic.Int64
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/info" {
							capabilities.Add(1)
						}
						boundary := (phase == "authentication" && r.URL.Path == "/auth/v1.0") ||
							(phase == "capabilities" && r.URL.Path == "/info")
						if boundary && held.CompareAndSwap(false, true) {
							entered <- struct{}{}
							<-gate
						}
						h.mock.ServeHTTP(w, r)
					}))
					t.Cleanup(func() { release(); srv.Close() })
					b, err := h.bookmarks.Save(t.Context(), BookmarkDTO{
						DisplayName: "held", AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester",
					}, "testing")
					if err != nil {
						t.Fatal(err)
					}
					result := make(chan error, 1)
					go func() {
						if call == "connect" {
							_, err := h.browse.Connect(t.Context(), b.ID)
							result <- err
						} else {
							_, err := c.SessionFor(t.Context(), b.ID)
							result <- err
						}
					}()
					awaitConnection(t, entered)
					wantErr := bookmarks.ErrNotFound
					var fresh *swiftcontrol.Session
					switch change {
					case "delete":
						if err := h.bookmarks.Delete(t.Context(), b.ID); err != nil {
							t.Fatal(err)
						}
					case "replace":
						// Reentering the same valid key still publishes a new
						// generation, without making the server reject either key.
						if _, err := h.bookmarks.Save(t.Context(), b, "testing"); err != nil {
							t.Fatal(err)
						}
						if err := c.Connect(t.Context(), b.ID); err != nil {
							t.Fatal(err)
						}
						fresh, err = c.Session()
						if err != nil {
							t.Fatal(err)
						}
						wantErr = bookmarks.ErrChanged
					case "cosmetic":
						b.DisplayName = "renamed during authentication"
						if _, err := h.bookmarks.Save(t.Context(), b, ""); err != nil {
							t.Fatal(err)
						}
						wantErr = nil
					case "unavailable":
						if err := h.db.Close(); err != nil {
							t.Fatal(err)
						}
					}
					beforeRelease := capabilities.Load()
					release()
					err = awaitConnection(t, result)
					if change == "unavailable" {
						if err == nil {
							t.Error("connection succeeded without generation validation")
						}
						c.mu.Lock()
						installed := c.active.session != nil || len(c.sessions) != 0 || c.capabilities != nil
						c.mu.Unlock()
						if installed {
							t.Fatal("unvalidated connection installed in cache")
						}
					} else if !errors.Is(err, wantErr) {
						t.Errorf("obsolete %s result = %v; want %v", call, err, wantErr)
					}
					if phase == "authentication" && change != "cosmetic" && capabilities.Load() != beforeRelease {
						t.Error("obsolete authentication continued to capability discovery")
					}
					if change == "unavailable" {
						return
					}
					active, activeErr := c.Session()
					cached, cachedErr := c.SessionFor(t.Context(), b.ID)
					if change == "delete" {
						if active != nil || !errors.Is(activeErr, ErrNotConnected) || cached != nil || !errors.Is(cachedErr, bookmarks.ErrNotFound) {
							t.Fatalf("deleted authority was restored: active=%v cached=%v", activeErr, cachedErr)
						}
					} else if change == "cosmetic" {
						if cachedErr != nil || cached == nil {
							t.Fatalf("cosmetic edit refused current session: %v", cachedErr)
						}
						if call == "connect" && (activeErr != nil || active != cached) {
							t.Fatalf("cosmetic edit refused active connection: %v", activeErr)
						}
					} else if activeErr != nil || cachedErr != nil || active != fresh || cached != fresh {
						t.Fatalf("obsolete completion replaced the current cache: active=%v cached=%v", activeErr, cachedErr)
					}
				})
			}
		}
	}
}

func TestCachedSessionsCheckPublishedGeneration(t *testing.T) {
	for _, call := range []string{"active", "session-for"} {
		for _, change := range []string{"replace", "delete", "cosmetic"} {
			t.Run(call+"/"+change, func(t *testing.T) {
				h := newHarness(t)
				h.connect(t)
				c := h.transfer.connections
				old, err := c.Session()
				if err != nil {
					t.Fatal(err)
				}
				id := c.ActiveBookmarkID()
				store := h.bookmarks.store
				wantErr := bookmarks.ErrChanged
				if change == "delete" {
					err = store.Delete(t.Context(), id)
					wantErr = bookmarks.ErrNotFound
				} else {
					b, readErr := store.Get(t.Context(), id)
					if readErr != nil {
						t.Fatal(readErr)
					}
					key := []byte("testing")
					if change == "cosmetic" {
						b.DisplayName = "cosmetic change"
						key = nil
						wantErr = nil
					}
					_, err = store.Save(t.Context(), b, key)
				}
				if err != nil {
					t.Fatal(err)
				}
				var got *swiftcontrol.Session
				if call == "active" {
					got, err = c.Session()
				} else {
					got, err = c.SessionFor(t.Context(), id)
				}
				if !errors.Is(err, wantErr) || (wantErr != nil && got != nil) || (wantErr == nil && got != old) {
					t.Fatalf("%s cache lookup after %s = %p, %v; want %v", call, change, got, err, wantErr)
				}
			})
		}
	}
}

func TestCacheReadersReleaseMutexBeforeGenerationRead(t *testing.T) {
	for _, call := range []string{"active", "session-for", "capabilities", "bookmark-id", "account-name"} {
		t.Run(call, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			c := h.transfer.connections
			id := c.ActiveBookmarkID()
			reader := h.db.Reader()
			reader.SetMaxOpenConns(1)
			connection, err := reader.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { connection.Close() })
			before := reader.Stats().WaitCount
			result := make(chan error, 1)
			go func() {
				var err error
				switch call {
				case "active":
					_, err = c.Session()
				case "session-for":
					_, err = c.SessionFor(t.Context(), id)
				case "capabilities":
					c.Capabilities()
				case "bookmark-id":
					c.ActiveBookmarkID()
				case "account-name":
					c.AccountName()
				}
				result <- err
			}()
			// The pool counter acknowledges that the real generation read
			// is waiting; elapsed time is not evidence of reaching the lock.
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for reader.Stats().WaitCount == before {
				select {
				case err := <-result:
					t.Fatalf("cache reader skipped generation validation: %v", err)
				case <-deadline.C:
					t.Fatal("cache reader did not reach the database barrier")
				case <-ticker.C:
				}
			}
			if c.mu.TryLock() {
				c.mu.Unlock()
			} else {
				t.Error("cache mutex held while waiting for bookmark generation")
			}
			if err := connection.Close(); err != nil {
				t.Fatal(err)
			}
			if err := awaitConnection(t, result); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCachedSessionsRefuseUnavailableGeneration(t *testing.T) {
	for _, call := range []string{"active", "session-for", "capabilities", "bookmark-id", "account-name"} {
		t.Run(call, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			c := h.transfer.connections
			id := c.ActiveBookmarkID()
			if err := h.db.Close(); err != nil {
				t.Fatal(err)
			}
			switch call {
			case "active":
				if session, err := c.Session(); session != nil || err == nil {
					t.Fatalf("unvalidated active session = %p, %v", session, err)
				}
			case "session-for":
				if session, err := c.SessionFor(t.Context(), id); session != nil || err == nil {
					t.Fatalf("unvalidated cached session = %p, %v", session, err)
				}
			case "capabilities":
				if c.Capabilities() != nil {
					t.Fatal("unvalidated capabilities returned")
				}
			case "bookmark-id":
				if c.ActiveBookmarkID() != "" {
					t.Fatal("unvalidated bookmark identity returned")
				}
			case "account-name":
				if c.AccountName() != "" {
					t.Fatal("unvalidated account name returned")
				}
			}
		})
	}
}
