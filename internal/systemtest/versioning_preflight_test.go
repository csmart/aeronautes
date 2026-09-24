package systemtest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// headFailProxy fails the first container HEAD it sees and passes
// everything else through. After ADR 0054 that first HEAD is the write
// preflight, so a refusal must happen before any later request.
type headFailProxy struct {
	failHeads int32
	failed    int32
	passed    int32
}

func installHeadFailProxy(t *testing.T, h *Harness, account *Account, failHeads int32) *headFailProxy {
	t.Helper()
	storage, err := url.Parse(account.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	t.Cleanup(transport.CloseIdleConnections)

	p := &headFailProxy{failHeads: failHeads}
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		container := r.Method == http.MethodHead && len(segments) == 3
		if container && atomic.LoadInt32(&p.failed) < atomic.LoadInt32(&p.failHeads) {
			atomic.AddInt32(&p.failed, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if container {
			atomic.AddInt32(&p.passed, 1)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)

	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range bookmarks {
		if bookmark.ID != account.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, account.key); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Browse.Connect(t.Context(), account.BookmarkID); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Fatal("bookmark for the proxied account is missing")
	return nil
}

func modernVersionedContainer(t *testing.T, h *Harness, label string) string {
	t.Helper()
	name := h.Container(h.Primary, label)
	h.rawPut(h.Primary, "/"+name, nil, map[string]string{"X-Versions-Enabled": "true"})
	detail, err := h.Inspector.Container(t.Context(), h.Primary.BookmarkID, name)
	if err != nil {
		t.Fatalf("read back %s: %v", name, err)
	}
	if detail.VersioningMode != "enabled" {
		t.Fatalf("fixture did not enable modern versioning on %s: mode=%q", name, detail.VersioningMode)
	}
	return name
}

func TestVersioningPreflight(t *testing.T) {
	t.Run("modern_container_refuses", func(t *testing.T) {
		h := New(t)
		name := modernVersionedContainer(t, h, "vp-control")
		_, err := h.Mutate.UpdateContainer(t.Context(), h.ActiveScope(t.Context()), name, "", app.ContainerEditDTO{
			Meta: map[string]string{"Note": "edited"}, VersioningMode: "off",
		})
		if !errors.Is(err, swiftcontrol.ErrModernVersioning) {
			t.Fatalf("modern-versioned edit = %v", err)
		}
	})

	t.Run("plain_container_applies", func(t *testing.T) {
		h := New(t)
		name := h.Container(h.Primary, "vp-plain")
		result, err := h.Mutate.UpdateContainer(t.Context(), h.ActiveScope(t.Context()), name, "", app.ContainerEditDTO{
			Meta: map[string]string{"Note": "edited"}, VersioningMode: "off",
		})
		if err != nil || result.Outcome != "applied" {
			t.Fatalf("plain edit = %+v, %v", result, err)
		}
	})

	t.Run("unestablished_mode_refuses", func(t *testing.T) {
		h := New(t)
		name := modernVersionedContainer(t, h, "vp-unknown")
		p := installHeadFailProxy(t, h, h.Primary, 1)
		_, err := h.Mutate.UpdateContainer(t.Context(), h.ActiveScope(t.Context()), name, "", app.ContainerEditDTO{
			Meta: map[string]string{"Note": "edited"}, VersioningMode: "off",
		})
		if !errors.Is(err, swiftcontrol.ErrVersioningUnestablished) {
			t.Fatalf("unestablished edit = %v", err)
		}
		if atomic.LoadInt32(&p.failed) != 1 || atomic.LoadInt32(&p.passed) != 0 {
			t.Fatalf("proxy failed %d passed %d, want failed=1 passed=0 (no second HEAD)",
				atomic.LoadInt32(&p.failed), atomic.LoadInt32(&p.passed))
		}
		after, readErr := h.Inspector.Container(t.Context(), h.Primary.BookmarkID, name)
		if readErr != nil {
			t.Fatalf("read back: %v", readErr)
		}
		if after.VersioningMode != "enabled" {
			t.Fatalf("refused edit still changed versioning: %q", after.VersioningMode)
		}
	})

	t.Run("unestablished_legacy_rewrite_refuses", func(t *testing.T) {
		h := New(t)
		archive := h.Container(h.Primary, "vp-archive")
		name := modernVersionedContainer(t, h, "vp-rewrite")
		p := installHeadFailProxy(t, h, h.Primary, 1)
		_, err := h.Mutate.UpdateContainer(t.Context(), h.ActiveScope(t.Context()), name, "", app.ContainerEditDTO{
			VersioningMode: "history_location", VersioningArchive: archive,
		})
		if !errors.Is(err, swiftcontrol.ErrVersioningUnestablished) {
			t.Fatalf("unestablished rewrite = %v", err)
		}
		if atomic.LoadInt32(&p.failed) != 1 || atomic.LoadInt32(&p.passed) != 0 {
			t.Fatalf("proxy failed %d passed %d, want failed=1 passed=0",
				atomic.LoadInt32(&p.failed), atomic.LoadInt32(&p.passed))
		}
		after, readErr := h.Inspector.Container(t.Context(), h.Primary.BookmarkID, name)
		if readErr != nil {
			t.Fatalf("read back: %v", readErr)
		}
		if after.VersioningMode != "enabled" {
			t.Fatalf("refused rewrite changed versioning to %q", after.VersioningMode)
		}
	})
}
