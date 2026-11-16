package systemtest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// swiftGate fronts the owned Swift for one bookmark: every request of a
// session connected through it passes the gate, so a case can act as a
// request arrives, or have one DELETE refused.
type swiftGate struct {
	upstream string
	server   *httptest.Server
	client   *http.Client
	mu       sync.Mutex
	// beforeAny runs before a request is forwarded, with the method and
	// path, so a case can act between a listing and the request that follows
	// it.
	beforeAny func(method, path string)
	// refuseDelete is a path suffix and refuseStatus a status: a DELETE of a
	// path ending in the suffix is answered with the status here and never
	// reaches the store, as a store that refuses the request would answer.
	refuseDelete string
	refuseStatus int
}

// refuseObjectDELETE refuses one object's DELETE with status, or lifts the
// refusal when status is 0, in the shape the mock offers.
func (g *swiftGate) refuseObjectDELETE(container, name string, status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refuseDelete, g.refuseStatus = "", 0
	if status != 0 {
		g.refuseDelete, g.refuseStatus = "/"+container+"/"+name, status
	}
}

func newSwiftGate(t *testing.T, authURL string) *swiftGate {
	t.Helper()
	upstream := authURL[:strings.Index(authURL, "/auth/")]
	g := &swiftGate{upstream: upstream, client: &http.Client{}}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

func (g *swiftGate) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	g.mu.Lock()
	beforeAny, refuseDelete, refuseStatus := g.beforeAny, g.refuseDelete, g.refuseStatus
	g.mu.Unlock()
	if beforeAny != nil {
		beforeAny(r.Method, r.URL.Path)
	}
	if refuseDelete != "" && r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, refuseDelete) {
		w.WriteHeader(refuseStatus)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, g.upstream+r.URL.RequestURI(), strings.NewReader(string(body)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	resp, err := g.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	replyBody, _ := io.ReadAll(resp.Body)
	for k, values := range resp.Header {
		if k == "X-Storage-Url" {
			values = []string{strings.Replace(values[0], g.upstream, g.server.URL, 1)}
		}
		w.Header()[k] = append([]string(nil), values...)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(replyBody)
}

// gatedSwift connects a bookmark through the gate and returns its id.
func gatedSwift(t *testing.T, h *Harness, g *swiftGate) string {
	t.Helper()
	ctx := context.Background()
	saved, err := h.Bookmarks.Save(ctx, app.BookmarkDTO{DisplayName: "gated", AuthURL: g.server.URL + "/auth/v1.0",
		Username: h.Primary.user, TLSMode: "system"}, h.Primary.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, rec := h.ConnectBookmark(ctx, saved.ID); rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	return saved.ID
}
