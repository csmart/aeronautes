package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func heldSessionRequest(t *testing.T, h *harness, session *swiftcontrol.Session, operation string) func() error {
	t.Helper()
	ctx := t.Context()
	switch operation {
	case "authentication":
		return func() error { return session.Authenticate(ctx) }
	case "capabilities":
		return func() error {
			capabilities, err := session.FetchCapabilities(ctx)
			if err != nil {
				return err
			}
			return capabilities.Unavailable
		}
	case "account":
		return func() error { _, err := session.AccountPortrait(ctx); return err }
	case "container":
		return func() error { _, err := session.ContainerDetail(ctx, "media"); return err }
	case "object":
		return func() error { _, err := session.ObjectDetail(ctx, "media", "file"); return err }
	case "open":
		return func() error {
			body, _, err := session.OpenObject(ctx, "media", "file")
			if err != nil {
				return err
			}
			defer body.Close()
			_, err = io.Copy(io.Discard, body)
			return err
		}
	case "put":
		return func() error {
			_, err := session.PutObject(ctx, "media", "new", strings.NewReader("new data"), "text/plain", nil)
			return err
		}
	case "post":
		return func() error { return session.PostObjectHeaders(ctx, "media", "file", nil) }
	case "delete":
		return func() error { return session.DeleteObject(ctx, "media", "file") }
	case "bulk-delete":
		return func() error { _, err := session.BulkDelete(ctx, "media", []string{"file"}); return err }
	case "symlink":
		return func() error { _, err := session.SymlinkGet(ctx, "media", "file"); return err }
	case "containers":
		rows := session.Containers(ctx, swiftcontrol.ListOptions{PageSize: 1})
		return func() error {
			for _, err := range rows {
				return err
			}
			return nil
		}
	case "objects":
		rows := session.Objects(ctx, "media", swiftcontrol.ObjectListOptions{PageSize: 1, Deep: true})
		return func() error {
			for _, err := range rows {
				return err
			}
			return nil
		}
	case "engine-credentials":
		return func() error {
			_, err := h.transfer.connections.EngineRemote(ctx, session, "aeronautes_src")
			return err
		}
	default:
		t.Fatalf("unknown session operation %q", operation)
		return nil
	}
}

func TestHeldSessionRefusesNewRequestsAfterBookmarkChange(t *testing.T) {
	operations := []string{"authentication", "capabilities", "account", "container", "object", "open", "put", "post", "delete", "bulk-delete", "symlink", "containers", "objects", "engine-credentials"}
	for _, change := range []string{"replace", "delete", "unavailable", "cosmetic"} {
		for _, operation := range operations {
			t.Run(change+"/"+operation, func(t *testing.T) {
				h := newHarness(t)
				var requests atomic.Int64
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					h.mock.ServeHTTP(w, r)
				}))
				t.Cleanup(srv.Close)
				h.authURL = srv.URL + "/auth/v1.0"
				h.connect(t)
				h.mock.AddContainer("media", "standard")
				seedObject(h.mock, "media", "file", "contents", nil)
				connections := h.transfer.connections
				id := connections.ActiveBookmarkID()
				session, err := connections.SessionFor(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				request := heldSessionRequest(t, h, session, operation)
				bookmark, err := h.bookmarks.store.Get(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "replace":
					// A still-valid key isolates generation revocation from
					// an authentication refusal by the test server.
					_, err = h.bookmarks.Save(t.Context(), toBookmarkDTO(bookmark), "testing")
				case "delete":
					err = h.bookmarks.Delete(t.Context(), id)
				case "unavailable":
					err = h.db.Close()
				case "cosmetic":
					bookmark.DisplayName = "renamed while session held"
					_, err = h.bookmarks.Save(t.Context(), toBookmarkDTO(bookmark), "")
				}
				if err != nil {
					t.Fatal(err)
				}
				before := requests.Load()
				err = request()
				if change == "cosmetic" {
					if err != nil {
						t.Fatalf("cosmetic edit revoked the held session: %v", err)
					}
					return
				}
				if err == nil {
					t.Error("obsolete or unavailable session admitted new authority")
				}
				if sent := requests.Load() - before; sent != 0 {
					t.Errorf("obsolete or unavailable session sent %d requests", sent)
				}
			})
		}
	}
}

func TestBookmarkChangeRefusesAuthenticatedRequestRetry(t *testing.T) {
	for _, operation := range []string{"account", "put", "bulk-delete", "symlink"} {
		t.Run(operation, func(t *testing.T) {
			h := newHarness(t)
			var armed atomic.Bool
			var requests atomic.Int64
			entered := make(chan struct{}, 1)
			gate, release := connectionGate(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if strings.HasPrefix(r.URL.Path, "/v1/") && armed.CompareAndSwap(true, false) {
					entered <- struct{}{}
					<-gate
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				h.mock.ServeHTTP(w, r)
			}))
			t.Cleanup(func() { release(); srv.Close() })
			h.authURL = srv.URL + "/auth/v1.0"
			h.connect(t)
			h.mock.AddContainer("media", "standard")
			seedObject(h.mock, "media", "file", "contents", nil)
			id := h.transfer.connections.ActiveBookmarkID()
			session, err := h.transfer.connections.SessionFor(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			request := heldSessionRequest(t, h, session, operation)
			armed.Store(true)
			result := make(chan error, 1)
			go func() { result <- request() }()
			awaitConnection(t, entered)
			bookmark, err := h.bookmarks.store.Get(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.bookmarks.Save(t.Context(), toBookmarkDTO(bookmark), "testing"); err != nil {
				t.Fatal(err)
			}
			before := requests.Load()
			release()
			if err := awaitConnection(t, result); err == nil {
				t.Error("obsolete request authenticated and retried")
			}
			if requests.Load() != before {
				t.Error("obsolete retry reached authentication or storage after the change")
			}
			fresh, err := h.transfer.connections.SessionFor(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if err := heldSessionRequest(t, h, fresh, operation)(); err != nil {
				t.Fatalf("replacement generation could not operate: %v", err)
			}
		})
	}
}

func TestBookmarkChangeRefusesLaterListingPages(t *testing.T) {
	for _, resource := range []string{"containers", "objects"} {
		t.Run(resource, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			h.mock.AddContainer("media", "standard")
			seedObject(h.mock, "media", "first", "contents", nil)
			seedObject(h.mock, "media", "second", "contents", nil)
			id := h.transfer.connections.ActiveBookmarkID()
			session, err := h.transfer.connections.SessionFor(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			seen, requests := 0, 0
			refused := false
			consume := func(err error) bool {
				if err != nil {
					refused = true
					return false
				}
				seen++
				if seen != 1 {
					t.Error("obsolete listing admitted another page")
					return false
				}
				if err := h.bookmarks.Delete(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				requests = h.mock.Requests()
				return true
			}
			if resource == "containers" {
				for _, err := range session.Containers(t.Context(), swiftcontrol.ListOptions{PageSize: 1}) {
					if !consume(err) {
						break
					}
				}
			} else {
				for _, err := range session.Objects(t.Context(), "media", swiftcontrol.ObjectListOptions{PageSize: 1, Deep: true}) {
					if !consume(err) {
						break
					}
				}
			}
			if seen != 1 || !refused || h.mock.Requests() != requests {
				t.Fatalf("listing after deletion: rows=%d, refused=%v, extra requests=%d", seen, refused, h.mock.Requests()-requests)
			}
		})
	}
}

func TestBookmarkChangeAllowsAdmittedReadButRefusesReopen(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "file", "contents", nil)
	id := h.transfer.connections.ActiveBookmarkID()
	session, err := h.transfer.connections.SessionFor(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := session.OpenObject(t.Context(), "media", "file")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { body.Close() })
	if err := h.bookmarks.Delete(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	if err != nil || string(data) != "contents" {
		t.Fatalf("already admitted read was lost: %q, %v", data, err)
	}
	seeker, ok := body.(interface {
		Seek(context.Context, int64, int) (int64, error)
	})
	if !ok {
		t.Fatal("native stream no longer exposes its contextual seek")
	}
	before := h.mock.Requests()
	if _, err := seeker.Seek(t.Context(), 0, io.SeekStart); err == nil {
		t.Error("deleted session reopened the object stream")
	}
	if h.mock.Requests() != before {
		t.Error("stream reopen reached storage after deletion")
	}
}
