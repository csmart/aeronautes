package deletion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// A deletion job sends at most one destructive request per name. The
// issued record is written before the request leaves, a name recorded
// before is never sent again in any later pass, and a record that cannot
// be written stops admission before anything is sent.

func ownedAccounting(t *testing.T) (*deletionAccounting, *Result, *int) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	result := &Result{}
	stops := 0
	accounting, err := newDeletionAccounting(context.Background(), result, Options{}, func() { stops++ })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accounting.close() })
	return accounting, result, &stops
}

func TestIssuedRecordAdmitsEachNameOnce(t *testing.T) {
	accounting, result, stops := ownedAccounting(t)
	ctx := context.Background()
	fresh, ok := accounting.issue(ctx, []string{"a", "b"})
	if !ok || strings.Join(fresh, ",") != "a,b" || result.NotRequestedAgain != 0 {
		t.Fatalf("first issue: fresh=%v ok=%v result=%+v", fresh, ok, result)
	}
	fresh, ok = accounting.issue(ctx, []string{"b", "c", "a"})
	if !ok || strings.Join(fresh, ",") != "c" || result.NotRequestedAgain != 2 || strings.Join(result.NotRequestedAgainNames, ",") != "b,a" {
		t.Fatalf("second issue: fresh=%v ok=%v result=%+v", fresh, ok, result)
	}
	fresh, ok = accounting.issue(ctx, nil)
	if !ok || len(fresh) != 0 {
		t.Fatalf("empty issue: fresh=%v ok=%v", fresh, ok)
	}
	if *stops != 0 || result.AccountingIncomplete {
		t.Fatalf("healthy issue stopped admission: stops=%d result=%+v", *stops, result)
	}
}

func TestIssuedRecordFailureStopsBeforeRequest(t *testing.T) {
	accounting, result, stops := ownedAccounting(t)
	if err := accounting.index.close(); err != nil {
		t.Fatal(err)
	}
	fresh, ok := accounting.issue(context.Background(), []string{"a"})
	if ok || fresh != nil || *stops != 1 || !result.AccountingIncomplete || accounting.problem() == nil {
		t.Fatalf("unwritable issued record did not stop admission: fresh=%v ok=%v stops=%d result=%+v problem=%v", fresh, ok, *stops, result, accounting.problem())
	}
	if !accounting.admit(context.Background()) == false {
		t.Fatal("admission stayed open after the issued record failed")
	}
	if fresh, ok := accounting.issue(context.Background(), []string{"b"}); ok || fresh != nil {
		t.Fatalf("a later issue was admitted after the latch: fresh=%v ok=%v", fresh, ok)
	}
}

// The same reported facet recorded twice counts once: the duplicate-facet
// proof lives at the accounting boundary and no longer depends on the
// sweep repeating a destructive request.
func TestRecordedFacetsCountDistinctIdentities(t *testing.T) {
	accounting, result, _ := ownedAccounting(t)
	var failed, unresolved []string
	accounting.opts = Options{
		OnFailure:    func(name, detail string) { failed = append(failed, name) },
		OnUnresolved: func(name, detail string) { unresolved = append(unresolved, name) },
	}
	pending := []pendingIdentity{
		{identityFacet{reportedFailureIdentity, "doomed/b"}, "500 Internal Server Error"},
		{identityFacet{unresolvedIdentity, "doomed/b"}, "the service reported 500 and no observation establishes whether the object was destroyed"},
	}
	for i := 0; i < 3; i++ {
		if !accounting.record(context.Background(), pending) {
			t.Fatalf("record %d refused: %v", i, accounting.problem())
		}
	}
	if result.Failed != 1 || result.Unresolved != 1 || len(failed) != 1 || len(unresolved) != 1 || len(result.Failures) != 1 || len(result.Unresolveds) != 1 {
		t.Fatalf("repeated facets were not counted once: %+v failed=%v unresolved=%v", result, failed, unresolved)
	}
}

// singleDeleteServer answers each object DELETE as applied without applying
// it, so a later listing offers the same names again; every other request
// reaches the mock.
type singleDeleteServer struct {
	inner   http.Handler
	mu      sync.Mutex
	deletes map[string]int
}

func (s *singleDeleteServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete && strings.Count(r.URL.Path, "/") >= 4 {
		s.mu.Lock()
		s.deletes[r.URL.Path]++
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.inner.ServeHTTP(w, r)
}

func TestParallelSweepIssuesEachNameOnce(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mock := swifttest.New("test:tester", "testing")
	front := &singleDeleteServer{inner: mock, deletes: map[string]int{}}
	srv := httptest.NewServer(front)
	t.Cleanup(srv.Close)
	session, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := session.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	capabilities, err := session.FetchCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mock.AddContainer("media", "standard")
	for _, name := range []string{"doomed/a", "doomed/b", "doomed/c"} {
		mock.PutObject("media", name, swifttest.Object{Bytes: 1, ETag: "aa", Data: []byte("x")})
	}
	result, err := DeletePrefix(ctx, session, capabilities, "media", "doomed/", Options{ForceParallel: true, Workers: 2, Passes: 3})
	if err != nil {
		t.Fatalf("parallel sweep returned an error: %v", err)
	}
	front.mu.Lock()
	defer front.mu.Unlock()
	if len(front.deletes) != 3 {
		t.Fatalf("deleted paths = %v, want three names", front.deletes)
	}
	for path, count := range front.deletes {
		if count != 1 {
			t.Errorf("%s was requested %d times, want exactly once", path, count)
		}
	}
	// The second pass listed all three again and requested none of them;
	// the run cannot claim completion while they are still listed.
	if result.Passes != 2 || result.NotRequestedAgain != 3 || result.Remaining != 1 {
		t.Errorf("sweep outcome: passes=%d notRequestedAgain=%d remaining=%d, want 2, 3 and 1", result.Passes, result.NotRequestedAgain, result.Remaining)
	}
}
