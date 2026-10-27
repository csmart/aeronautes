package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// The one-shot session closes its connections on every return, so verifying
// does not leave keep-alive connections behind.
func TestBookmarkVerifyClosesItsConnections(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var mu sync.Mutex
	open := map[net.Conn]bool{}
	srv := httptest.NewUnstartedServer(h.mock)
	srv.Config.ConnState = func(c net.Conn, state http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		switch state {
		case http.StateNew:
			open[c] = true
		case http.StateClosed, http.StateHijacked:
			delete(open, c)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(open)
	}
	typed := BookmarkDTO{DisplayName: "Typed", AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", TLSMode: "system"}
	for _, key := range []string{"testing", "wrong"} {
		if _, err := h.bookmarks.Verify(ctx, typed, key); (err == nil) != (key == "testing") {
			t.Fatalf("verify with %q: %v", key, err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for count() != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if n := count(); n != 0 {
			t.Fatalf("%d connection(s) left open after verify with %q", n, key)
		}
	}
}
