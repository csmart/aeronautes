package swiftcontrol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// droppingStore is the mock store behind a server that, once told to, drops
// the connection of the requests a case names before any response, and
// counts the attempts it receives by method from then on. Keep-alives are
// off, so each attempt arrives on a connection of its own: the standard
// transport repeats a read only on a connection it was reusing, and these
// cases are about the others.
type droppingStore struct {
	mu       sync.Mutex
	drop     func(method string, attempt int) bool
	attempts map[string]int
}

// newDroppingStore returns the store and a session on it, with one object,
// "box/thing", already there.
func newDroppingStore(t *testing.T) (*droppingStore, *Session) {
	t.Helper()
	store := &droppingStore{attempts: map[string]int{}}
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		store.mu.Lock()
		dropped := false
		if store.drop != nil {
			store.attempts[r.Method]++
			dropped = store.drop(r.Method, store.attempts[r.Method])
		}
		store.mu.Unlock()
		if dropped {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		mock.ServeHTTP(w, r)
	}))
	srv.Config.SetKeepAlivesEnabled(false)
	srv.Start()
	t.Cleanup(srv.Close)
	session, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	ctx := context.Background()
	if err := session.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateContainer(ctx, ContainerCreateRequest{Name: "box"}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.PutObject(ctx, "box", "thing", strings.NewReader("x"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	return store, session
}

// dropping starts the drops and the count.
func (s *droppingStore) dropping(drop func(method string, attempt int) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drop, s.attempts = drop, map[string]int{}
}

func (s *droppingStore) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts[method]
}

// A literal HEAD is one of the session's own requests, made outside the
// wire library, which repeats its own reads. One whose connection is
// dropped before any response is sent once more and answers; one dropped
// twice reports the failure and is not sent a third time.
func TestALiteralHeadDroppedInTransitIsSentOnceMore(t *testing.T) {
	ctx := context.Background()
	store, session := newDroppingStore(t)
	store.dropping(func(method string, attempt int) bool { return method == http.MethodHead && attempt == 1 })
	if detail, err := session.LiteralObjectHead(ctx, "box", "thing"); err != nil || detail.ContentLength != 1 {
		t.Fatalf("the HEAD was not repeated: %+v, %v", detail, err)
	}
	if got := store.count(http.MethodHead); got != 2 {
		t.Fatalf("the store received %d HEAD attempts, want 2", got)
	}

	store.dropping(func(method string, _ int) bool { return method == http.MethodHead })
	if _, err := session.LiteralObjectHead(ctx, "box", "thing"); err == nil {
		t.Fatal("a HEAD dropped on both attempts reported no failure")
	}
	if got := store.count(http.MethodHead); got != 2 {
		t.Fatalf("the store received %d HEAD attempts, want 2", got)
	}
}

// A request that changes the store is never sent twice: whether the store
// applied it cannot be known from a dropped connection (§49). An object
// DELETE is the case that matters, since like a read it has no body.
func TestADeleteDroppedInTransitIsNotSentAgain(t *testing.T) {
	ctx := context.Background()
	store, session := newDroppingStore(t)
	store.dropping(func(method string, _ int) bool { return method == http.MethodDelete })
	if _, err := session.DeleteName(ctx, "box", "thing"); !errors.Is(err, ErrUnverifiedDelete) {
		t.Fatal("a dropped delete reported no failure")
	}
	if got := store.count(http.MethodDelete); got != 1 {
		t.Fatalf("the store received %d DELETE attempts, want 1", got)
	}
}
