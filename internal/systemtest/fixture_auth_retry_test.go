package systemtest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Maintained regressions for ADR 0051. Fixture authentication retries a
// transient refusal to the readiness gate's own deadline, refuses a
// credential rejection at once so the one-account skip stays fast, and
// reports an attempt count when it needed more than one try.
//
// These drive authenticateWithin against a local auth endpoint rather
// than the real fixture: the property under test is the retry policy,
// and a real fixture cannot be made to refuse on demand.

// authFixture is an auth endpoint whose answer the test chooses per
// attempt, so a refusal is scripted rather than hoped for.
type authFixture struct {
	server *httptest.Server
	// requests counts HTTP requests, NOT authenticateWithin attempts: one
	// attempt makes two requests, the raw auth and then the oracle. The
	// first version of this file conflated the two and asserted the wrong
	// numbers, so the names are kept apart deliberately.
	requests atomic.Int64
	// answer reports the status for request n, 1-based. A status of 0
	// means hang up without answering, which is how a transport error is
	// produced deterministically rather than by racing a close.
	answer func(n int64) int
}

func newAuthFixture(t *testing.T, answer func(n int64) int) *authFixture {
	t.Helper()
	f := &authFixture{answer: answer}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.requests.Add(1)
		status := f.answer(n)
		if status == 0 {
			// Hijack and close without a response: the client sees a
			// broken connection, which is the transient shape this
			// retry exists for.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			conn.Close()
			return
		}
		if status/100 == 2 {
			w.Header().Set("X-Storage-Url", f.server.URL+"/v1/AUTH_test")
			w.Header().Set("X-Auth-Token", "AUTH_tkfixture")
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(f.server.Close)
	return f
}

// harnessFor builds the minimum Harness authenticateWithin needs: the
// testing handle for Helper, Logf and Cleanup. Nothing else on the
// harness is touched by the retry path.
func harnessFor(t *testing.T) *Harness { return &Harness{T: t} }

func accountFor(t *testing.T, f *authFixture) *Account {
	t.Helper()
	// Its own transport, for the reason Harness.account gives: a client left
	// on http.DefaultTransport shares its pool with the whole test binary.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	return &Account{authURL: f.server.URL + "/auth/v1.0", user: "test:tester", key: "testing", rawClient: &http.Client{
		Transport:     transport,
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// A transient refusal is retried and then succeeds, which is the whole
// point: a single broken connection must cost a retry interval, not a run.
func TestFixtureAuthRetriesATransientRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAs int
	}{
		{"broken connection", 0},
		{"service unavailable", http.StatusServiceUnavailable},
		{"not found", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthFixture(t, func(n int64) int {
				if n < 3 {
					return tc.failAs
				}
				return http.StatusOK
			})
			a := accountFor(t, f)
			attempts, err := harnessFor(t).authenticateWithin("primary", a, 5*time.Second)
			if err != nil {
				t.Fatalf("transient %s was not retried to success: %v", tc.name, err)
			}
			// Requests 1 and 2 are refused raw auths; request 3 is the raw
			// auth that succeeds and request 4 the oracle behind it.
			if attempts != 3 {
				t.Errorf("attempts = %d, want 3: the retry must not give up early or spin past success", attempts)
			}
			if got := f.requests.Load(); got != 4 {
				t.Errorf("server saw %d requests, want 4 for three attempts", got)
			}
			if a.token == "" || a.storageURL == "" {
				t.Errorf("succeeded without recording token and storage URL: token=%q storageURL=%q", a.token, a.storageURL)
			}
			if a.Oracle == nil {
				t.Error("succeeded without connecting the oracle")
			}
		})
	}
}

// A credential refusal is definitive: retrying it would cost the
// one-account workstation the whole deadline at every harness
// construction, so it must return on the first answer.
func TestFixtureAuthRefusesCredentialsAtOnce(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprintf("http %d", status), func(t *testing.T) {
			f := newAuthFixture(t, func(int64) int { return status })
			started := time.Now()
			_, err := harnessFor(t).authenticateWithin("secondary", accountFor(t, f), 30*time.Second)
			if err == nil {
				t.Fatal("a credential refusal was accepted")
			}
			if got := f.requests.Load(); got != 1 {
				t.Errorf("server saw %d requests, want 1: HTTP %d is an answer, not a transient failure", got, status)
			}
			if elapsed := time.Since(started); elapsed > 5*time.Second {
				t.Errorf("took %s: a definitive refusal must not consume the deadline", elapsed)
			}
			if !strings.Contains(err.Error(), fmt.Sprint(status)) {
				t.Errorf("error does not name the status that decided it: %v", err)
			}
		})
	}
}

// The deadline bounds a fixture that never comes up, and the error says
// what was tried rather than only that something failed.
func TestFixtureAuthReportsWhatItTriedWhenTheDeadlinePasses(t *testing.T) {
	f := newAuthFixture(t, func(int64) int { return http.StatusServiceUnavailable })
	_, err := harnessFor(t).authenticateWithin("primary", accountFor(t, f), 700*time.Millisecond)
	if err == nil {
		t.Fatal("an endpoint that never authenticates was accepted")
	}
	if f.requests.Load() < 2 {
		t.Errorf("server saw %d requests, want at least 2 before the deadline", f.requests.Load())
	}
	for _, want := range []string{"primary", "700ms", "attempts", "503"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits %q, so the next reader cannot tell what was tried: %v", want, err)
		}
	}
}

// A retry hides an unhealthy fixture from the result, so the count it
// took has to reach the caller; that count is what Harness.account turns
// into a log line. Without it a fourth-attempt success and a
// first-attempt success are indistinguishable.
func TestFixtureAuthReportsHowManyAttemptsItTook(t *testing.T) {
	f := newAuthFixture(t, func(n int64) int {
		if n < 4 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	})
	attempts, err := harnessFor(t).authenticateWithin("secondary", accountFor(t, f), 5*time.Second)
	if err != nil {
		t.Fatalf("retry to success failed: %v", err)
	}
	if attempts != 4 {
		t.Errorf("attempts = %d, want 4: the caller logs this, so an unhealthy fixture stays visible only if it is accurate", attempts)
	}
}

// A fixture that answers first time reports one attempt, so the caller
// stays quiet and the signal above marks the exception rather than every
// run.
func TestFixtureAuthReportsOneAttemptWhenItAuthenticatesFirstTime(t *testing.T) {
	f := newAuthFixture(t, func(int64) int { return http.StatusOK })
	attempts, err := harnessFor(t).authenticateWithin("primary", accountFor(t, f), 5*time.Second)
	if err != nil {
		t.Fatalf("a healthy fixture was refused: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	// One attempt is two requests: the raw auth and the oracle behind it.
	if got := f.requests.Load(); got != 2 {
		t.Errorf("server saw %d requests, want 2 for a single attempt", got)
	}
}

// A seed client must own its transport. Left on http.DefaultTransport its
// connection pool is shared with everything else in this binary, and any
// CloseIdleConnections anywhere in the process can then break a seed request
// that is in flight on a pooled connection — which is how one download case's
// teardown failed an unrelated container PUT with "transport connection
// broken: http: CloseIdleConnections called" on a loaded runner. The property
// is asserted structurally because the failure it prevents is a race, and a
// race is a poor guard against a regression that is a missing field.
func TestFixtureSeedClientsOwnTheirTransport(t *testing.T) {
	f := newAuthFixture(t, func(int64) int { return http.StatusOK })
	one, two := accountFor(t, f), accountFor(t, f)
	for _, a := range []*Account{one, two} {
		if a.rawClient.Transport == nil {
			t.Fatal("a seed client has no transport of its own, so it rides the shared default pool")
		}
		if a.rawClient.Transport == http.DefaultTransport {
			t.Fatal("a seed client uses the shared default transport itself")
		}
	}
	if one.rawClient.Transport == two.rawClient.Transport {
		t.Error("two accounts share one transport, so one account's teardown reaches the other's pool")
	}
}
