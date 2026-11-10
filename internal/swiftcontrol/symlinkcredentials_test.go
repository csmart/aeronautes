package swiftcontrol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSymlinkRetryUsesNewStorageURL(t *testing.T) {
	var generation atomic.Int64
	var heads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			n := generation.Add(1)
			w.Header().Set("X-Storage-Url", fmt.Sprintf("http://%s/gen%d/v1/AUTH_test", r.Host, n))
			w.Header().Set("X-Auth-Token", fmt.Sprint(n))
			return
		}
		if heads.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/gen"+r.Header.Get("X-Auth-Token")+"/v1/AUTH_test/owned/link" {
			t.Error("symlink retry combined an old URL with the new token")
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	session := mustSession(t, srv.URL)
	if _, err := session.SymlinkGet(context.Background(), "owned", "link"); err != nil {
		t.Fatal(err)
	}
	if generation.Load() != 2 || heads.Load() != 2 {
		t.Fatal("symlink read did not make exactly one authenticated retry")
	}
}

func TestSymlinkKeepsRejectedCredentialsInvalidated(t *testing.T) {
	var failAuth atomic.Bool
	var heads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			if failAuth.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("X-Storage-Url", "http://"+r.Host+"/v1/AUTH_test")
			w.Header().Set("X-Auth-Token", "synthetic-token")
			w.Header().Set("X-Auth-Token-Expires", "3600")
			return
		}
		heads.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	session := mustSession(t, srv.URL)
	failAuth.Store(true)
	for range 2 {
		if _, err := session.SymlinkGet(context.Background(), "owned", "link"); err == nil {
			t.Fatal("symlink read ignored failed authentication")
		}
	}
	if heads.Load() != 1 {
		t.Fatal("symlink read reused a rejected token after failed authentication")
	}
}

func TestSymlinkCredentialsRemainCoherentDuringWireReauth(t *testing.T) {
	var generation atomic.Int64
	var heads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			n := generation.Add(1)
			w.Header().Set("X-Storage-Url", fmt.Sprintf("http://%s/gen%d/v1/AUTH_test", r.Host, n))
			w.Header().Set("X-Auth-Token", fmt.Sprint(n))
			return
		}
		if r.URL.Path != "/gen"+r.Header.Get("X-Auth-Token")+"/v1/AUTH_test/owned/link" {
			t.Error("symlink request mixed credentials from different auth responses")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		heads.Add(1)
	}))
	defer srv.Close()
	session := mustSession(t, srv.URL)
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			if err := session.conn.Authenticate(ctx); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for range 100 {
		if _, err := session.SymlinkGet(ctx, "owned", "link"); err != nil {
			t.Error(err)
			break
		}
	}
	wg.Wait()
	if heads.Load() != 100 {
		t.Fatalf("completed symlink reads: %d, want 100", heads.Load())
	}
}
