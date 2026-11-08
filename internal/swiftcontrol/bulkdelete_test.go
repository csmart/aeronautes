package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBulkDeleteClientBoundaries(t *testing.T) {
	// The review-wave G5/H-08 client contract: redirects refuse (the
	// token never travels), a 401 earns one re-auth and retry, and an
	// undercounted 200 cannot pass as success.
	ctx := context.Background()

	// Redirect refusal.
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			w.Header().Set("X-Storage-Url", "http://"+r.Host+"/v1/AUTH_test")
			w.Header().Set("X-Auth-Token", "tok")
			return
		}
		http.Redirect(w, r, "http://evil.invalid/steal", http.StatusFound)
	}))
	defer redirecting.Close()
	session := mustSession(t, redirecting.URL)
	if _, err := session.BulkDelete(ctx, "c", []string{"a"}); err == nil ||
		!(strings.Contains(err.Error(), "redirect") || strings.Contains(err.Error(), "302")) {
		t.Fatalf("redirected bulk-delete = %v, want refusal (302 surfaced, never followed)", err)
	}

	// One re-auth on 401, then success; and cardinality reconciliation.
	var authCalls, attempts int
	var undercount bool
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			authCalls++
			w.Header().Set("X-Storage-Url", "http://"+r.Host+"/v1/AUTH_test")
			w.Header().Set("X-Auth-Token", fmt.Sprintf("tok-%d", authCalls))
			return
		}
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if undercount {
			fmt.Fprint(w, `{"Number Deleted": 1, "Number Not Found": 0, "Response Status": "200 OK", "Errors": []}`)
			return
		}
		fmt.Fprint(w, `{"Number Deleted": 2, "Number Not Found": 1, "Response Status": "200 OK", "Errors": []}`)
	}))
	defer flaky.Close()
	session = mustSession(t, flaky.URL)
	result, err := session.BulkDelete(ctx, "c", []string{"a", "b", "d"})
	if err != nil {
		t.Fatalf("bulk after re-auth: %v", err)
	}
	if result.Deleted != 2 || result.NotFound != 1 || authCalls < 2 {
		t.Fatalf("result = %+v authCalls=%d (one 401 re-auth expected)", result, authCalls)
	}

	attempts, undercount = 1, true // skip the 401 leg, force undercount
	if _, err := session.BulkDelete(ctx, "c", []string{"a", "b", "d"}); err == nil || !strings.Contains(err.Error(), "accounts for") {
		t.Fatalf("undercounted response = %v, want cardinality error", err)
	}
}

func mustSession(t *testing.T, base string) *Session {
	t.Helper()
	session, err := New(Config{AuthURL: base + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestAuthenticateRefusesRedirect(t *testing.T) {
	// G22: the wire library's client would follow a 302 and resend
	// X-Auth-Key to wherever it pointed. The transport now strips the
	// Location, so auth fails WITHOUT a second hop.
	var evilHits int
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHits++
	}))
	defer evil.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal-the-key", http.StatusFound)
	}))
	defer redirecting.Close()

	session, err := New(Config{AuthURL: redirecting.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Authenticate(context.Background()); err == nil {
		t.Fatal("redirected auth succeeded")
	}
	if evilHits != 0 {
		t.Fatalf("the durable key travelled: redirect target was contacted %d time(s)", evilHits)
	}
}

func TestForceReauthChangesTheToken(t *testing.T) {
	for _, expires := range []string{"", "3600", fmt.Sprint(time.Now().Add(time.Hour).Unix())} {
		t.Run("expiry="+expires, func(t *testing.T) {
			var authCalls int
			var fail atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authCalls++
				if fail.Load() {
					http.Error(w, "synthetic auth failure", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("X-Storage-Url", "http://"+r.Host+"/v1/AUTH_test")
				w.Header().Set("X-Auth-Token", fmt.Sprintf("tok-%d", authCalls))
				w.Header().Set("X-Auth-Token-Expires", expires)
			}))
			defer srv.Close()
			session := mustSession(t, srv.URL)
			first := session.AuthToken()
			ctx := context.Background()
			if err := session.RefreshIfStale(ctx, time.Minute); err != nil {
				t.Fatal(err)
			}
			if session.AuthToken() != first {
				t.Fatal("young token refreshed without cause")
			}
			session.ForceReauth()
			fail.Store(true)
			if err := session.RefreshIfStale(ctx, time.Minute); err == nil {
				t.Fatal("rejected token survived forced refresh")
			}
			fail.Store(false)
			if err := session.RefreshIfStale(ctx, time.Minute); err != nil {
				t.Fatal(err)
			}
			if session.AuthToken() == first || session.AuthToken() == "" {
				t.Fatal("refresh after auth failure reused the rejected token")
			}
		})
	}
}

func TestStorageURLDowngradeRefused(t *testing.T) {
	// F-07: an https auth endpoint answering with an http storage URL
	// must refuse — the token would travel plaintext to wherever the
	// response pointed.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Storage-Url", "http://plaintext.invalid/v1/AUTH_test")
		w.Header().Set("X-Auth-Token", "tok")
	}))
	defer srv.Close()
	session, err := New(Config{
		AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing",
		TLS: TLSConfig{Mode: TLSModeInsecure}, // trust the test server's cert
	})
	if err != nil {
		t.Fatal(err)
	}
	err = session.Authenticate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "plaintext") {
		t.Fatalf("downgraded storage URL = %v, want refusal", err)
	}
}

func TestCredentialSnapshotRaceFree(t *testing.T) {
	// R-06: concurrent reads of token/storage-URL/expiry must not race
	// the library's implicit reauth. Under -race, hammer the accessors
	// while forcing reauths; the atomic snapshot keeps each read
	// consistent (and never mismatched empty).
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			n++
			w.Header().Set("X-Storage-Url", "http://"+r.Host+"/v1/AUTH_test")
			w.Header().Set("X-Auth-Token", fmt.Sprintf("tok-%d", n))
		}
	}))
	defer srv.Close()
	session := mustSession(t, srv.URL)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = session.AuthToken()
					_ = session.StorageURL()
					_ = session.TokenExpires()
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		session.ForceReauth()
		_ = session.RefreshIfStale(context.Background(), time.Hour)
	}
	close(stop)
	wg.Wait()
	if session.AuthToken() == "" {
		t.Fatal("token empty after reauth storm")
	}
}

func TestBulkDeleteClassifiesTheReply(t *testing.T) {
	// A store answer is classified as every other verb classifies it: a token
	// refused again after the one re-auth and retry is unauthorized, a client
	// refusal is not a server error, and only a 5xx is.
	ctx := context.Background()
	serve := func(status int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/auth/") {
				w.Header().Set("X-Storage-Url", "http://"+r.Host+"/v1/AUTH_test")
				w.Header().Set("X-Auth-Token", "tok")
				return
			}
			w.WriteHeader(status)
		}))
	}
	for _, c := range []struct {
		status   int
		wantKind ErrorKind
	}{
		{http.StatusUnauthorized, KindUnauthorized},
		{http.StatusBadRequest, KindUnknown},
		{http.StatusServiceUnavailable, KindServer},
	} {
		srv := serve(c.status)
		_, err := mustSession(t, srv.URL).BulkDelete(ctx, "c", []string{"a"})
		srv.Close()
		var se *SwiftError
		if !errors.As(err, &se) || se.Kind != c.wantKind || se.HTTPStatus != c.status {
			t.Fatalf("HTTP %d: err = %v, want kind %s carrying that status", c.status, err, c.wantKind)
		}
	}
}
