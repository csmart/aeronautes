package swiftcontrol

import (
	"bytes"
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCredentialsRetainSessionTrust(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Storage-Url", "https://"+r.Host+"/v1/AUTH_test")
		w.Header().Set("X-Auth-Token", "synthetic-token")
	}))
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	original := bytes.Clone(ca)
	session, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", TLS: TLSConfig{Mode: TLSModeCustomCA, CACertPEM: ca}})
	if err != nil {
		t.Fatal(err)
	}
	ca[0] ^= 1
	credentials, err := session.Credentials(context.Background(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.TLS.Mode != TLSModeCustomCA || !bytes.Equal(credentials.TLS.CACertPEM, original) {
		t.Fatal("caller changed the session trust after construction")
	}
	credentials.TLS.CACertPEM[0] ^= 1
	credentials, err = session.Credentials(context.Background(), time.Minute)
	if err != nil || !bytes.Equal(credentials.TLS.CACertPEM, original) {
		t.Fatalf("returned credentials could mutate session trust: %v", err)
	}
}

func TestCredentialsHideRejectedTokenAfterRefreshFailure(t *testing.T) {
	var fail atomic.Bool
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if fail.Load() {
			http.Error(w, "synthetic auth failure", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("X-Storage-Url", "http://"+r.Host+"/v1/AUTH_test")
		w.Header().Set("X-Auth-Token", fmt.Sprintf("synthetic-%d", n))
		w.Header().Set("X-Auth-Token-Expires", "3600")
	}))
	defer srv.Close()
	session := mustSession(t, srv.URL)
	first := session.AuthToken()
	session.ForceReauth()
	fail.Store(true)
	credentials, err := session.Credentials(context.Background(), time.Minute)
	if err == nil || credentials.Token != "" || credentials.StorageURL != "" || !credentials.Expires.IsZero() {
		t.Fatal("failed refresh exposed previously accepted credentials")
	}
	fail.Store(false)
	credentials, err = session.Credentials(context.Background(), time.Minute)
	if err != nil || credentials.Token == "" || credentials.Token == first {
		t.Fatalf("retry failed to publish new credentials: %v", err)
	}
}

func TestBulkDeleteKeepsRejectedCredentialsInvalidated(t *testing.T) {
	var failAuth atomic.Bool
	var deletes atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			if failAuth.Load() {
				http.Error(w, "synthetic auth failure", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("X-Storage-Url", "http://"+r.Host+"/v1/AUTH_test")
			w.Header().Set("X-Auth-Token", "synthetic-token")
			w.Header().Set("X-Auth-Token-Expires", "3600")
			return
		}
		deletes.Add(1)
		http.Error(w, "synthetic rejected token", http.StatusUnauthorized)
	}))
	defer srv.Close()
	session := mustSession(t, srv.URL)
	failAuth.Store(true)
	for range 2 {
		if _, err := session.BulkDelete(context.Background(), "owned", []string{"one"}); err == nil {
			t.Fatal("bulk deletion ignored failed reauthentication")
		}
	}
	if deletes.Load() != 1 {
		t.Fatal("a later bulk request reused the rejected token after failed authentication")
	}
}

func TestCredentialsAndBulkDeleteRemainCoherentDuringWireReauth(t *testing.T) {
	var generation atomic.Int64
	var deletes atomic.Int64
	baseExpiry := time.Now().Add(time.Hour).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			n := generation.Add(1)
			w.Header().Set("X-Storage-Url", fmt.Sprintf("http://%s/v1/AUTH_%d", r.Host, n))
			w.Header().Set("X-Auth-Token", fmt.Sprint(n))
			w.Header().Set("X-Auth-Token-Expires", fmt.Sprint(baseExpiry+n))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/AUTH_"+r.Header.Get("X-Auth-Token") {
			t.Error("bulk request mixed a storage URL and token from different auth responses")
			http.Error(w, "credential mismatch", http.StatusForbidden)
			return
		}
		deletes.Add(1)
		// Both counts are always present in a real reply, and the
		// decoder now requires them rather than reading a missing
		// field as an observed zero.
		fmt.Fprint(w, `{"Number Deleted":1,"Number Not Found":0,"Response Status":"200 OK"}`)
	}))
	defer srv.Close()
	session := mustSession(t, srv.URL)
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			// The wire library can reauthenticate without refreshMu during
			// a native operation, so explicit-session locking is not enough.
			if err := session.conn.Authenticate(ctx); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for range 100 {
		credentials, err := session.Credentials(ctx, time.Minute)
		if err != nil {
			t.Error(err)
			break
		}
		n, err := strconv.ParseInt(credentials.Token, 10, 64)
		if err != nil || credentials.StorageURL != fmt.Sprintf("%s/v1/AUTH_%d", srv.URL, n) || credentials.Expires.Unix() != baseExpiry+n {
			t.Error("credential fields came from different auth responses")
			break
		}
		if _, err := session.BulkDelete(ctx, "owned", []string{"one"}); err != nil {
			t.Error(err)
			break
		}
	}
	wg.Wait()
	if deletes.Load() != 100 {
		t.Fatalf("completed bulk requests: %d, want 100", deletes.Load())
	}
}
