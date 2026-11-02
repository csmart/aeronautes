package swifttest_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// A container HEAD ranges the container's Meta map while SetContainerMeta
// writes it; both run as net/http handler goroutines of the mock. The
// handler once ranged an aliased copy of Meta after releasing the lock,
// racing a concurrent write. Under -race this trips on that regression
// and is clean once the snapshot clones Meta under the lock.
func TestContainerMetaConcurrentAccessIsRaceFree(t *testing.T) {
	const token = "AUTH_tk_swifttest_0000000000000001"
	server := swifttest.New("tester", "testing")
	server.AddContainer("media", "standard")
	server.SetContainerMeta("media", "Team", "ops")

	head := func() int {
		req := httptest.NewRequest(http.MethodHead, "/v1/AUTH_test/media", nil)
		req.Header.Set("X-Auth-Token", token)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		return rec.Code
	}
	// Guard against a false pass: the read race is inside the container
	// handler, which a rejected token would never reach.
	if code := head(); code == http.StatusUnauthorized {
		t.Fatalf("container HEAD was unauthorised (%d); the test would not exercise the handler", code)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 2000 {
			head()
		}
	}()
	go func() {
		defer wg.Done()
		for range 2000 {
			server.SetContainerMeta("media", "Team", "changed")
		}
	}()
	wg.Wait()
}
