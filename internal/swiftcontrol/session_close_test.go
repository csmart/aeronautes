package swiftcontrol

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// Close must reach the transport at the bottom of the wrappers without the
// wrappers advertising the exported method the wire library looks for: the
// library closes idle connections on such a transport whenever it
// re-authenticates, which breaks the requests in flight on them.
func TestCloseReleasesConnectionsWithoutExposingThemToTheLibrary(t *testing.T) {
	var mu sync.Mutex
	open := map[net.Conn]bool{}
	srv := httptest.NewUnstartedServer(swifttest.New("test:tester", "testing"))
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

	for _, admitted := range []bool{false, true} {
		cfg := Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"}
		if admitted {
			cfg.AdmitRequest = func(context.Context) error { return nil }
		}
		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, exposed := s.conn.Transport.(interface{ CloseIdleConnections() }); exposed {
			t.Fatalf("admitted=%v: the transport advertises CloseIdleConnections to the wire library", admitted)
		}
		if err := s.Authenticate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AccountPortrait(context.Background()); err != nil {
			t.Fatal(err)
		}
		if count() == 0 {
			t.Fatalf("admitted=%v: expected a keep-alive connection to be open before Close", admitted)
		}
		s.Close()
		deadline := time.Now().Add(5 * time.Second)
		for count() != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if n := count(); n != 0 {
			t.Fatalf("admitted=%v: %d connection(s) still open after Close", admitted, n)
		}
	}
}
