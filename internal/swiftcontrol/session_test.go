package swiftcontrol

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeAuth is a minimal TempAuth-style v1 endpoint: it checks
// X-Auth-User/X-Auth-Key, hands out tok1, tok2, ... and points the client
// at storagePath on the same server.
type fakeAuth struct {
	mu          sync.Mutex
	user, key   string
	storagePath string
	authCalls   int
	serverURL   string
}

func (f *fakeAuth) currentToken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fmt.Sprintf("tok%d", f.authCalls)
}

func (f *fakeAuth) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Auth-User") != f.user || r.Header.Get("X-Auth-Key") != f.key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	f.authCalls++
	token := fmt.Sprintf("tok%d", f.authCalls)
	f.mu.Unlock()
	w.Header().Set("X-Storage-Url", f.serverURL+f.storagePath)
	w.Header().Set("X-Auth-Token", token)
	w.WriteHeader(http.StatusOK)
}

func writeAccountHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Account-Container-Count", "3")
	w.Header().Set("X-Account-Object-Count", "42")
	w.Header().Set("X-Account-Bytes-Used", "1024")
	w.WriteHeader(http.StatusNoContent)
}

func TestAuthenticateV1(t *testing.T) {
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	auth.serverURL = srv.URL

	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got, want := s.StorageURL(), srv.URL+"/v1/AUTH_acct"; got != want {
		t.Fatalf("StorageURL = %q, want %q", got, want)
	}
	if auth.authCalls != 1 {
		t.Fatalf("auth endpoint called %d times, want 1", auth.authCalls)
	}
}

func TestAuthenticateWrongKey(t *testing.T) {
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	auth.serverURL = srv.URL

	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Authenticate(context.Background())
	var se *SwiftError
	if !errors.As(err, &se) {
		t.Fatalf("Authenticate error = %v (%T), want *SwiftError", err, err)
	}
	if se.Kind != KindUnauthorized || se.HTTPStatus != 401 || se.Retryable {
		t.Fatalf("got kind=%s status=%d retryable=%v, want unauthorized/401/false", se.Kind, se.HTTPStatus, se.Retryable)
	}
}

// TestReauthReplayOn401 is the §9.2 contract: on a 401 mid-session the
// client invalidates the token, authenticates once, and replays the
// request — observable as exactly two auth calls and a successful
// operation.
func TestReauthReplayOn401(t *testing.T) {
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	mux.HandleFunc("/v1/AUTH_acct", func(w http.ResponseWriter, r *http.Request) {
		// tok1 is "expired": only the freshest token is accepted once a
		// second auth has happened, and tok1 is always rejected.
		if r.Header.Get("X-Auth-Token") != auth.currentToken() || auth.currentToken() == "tok1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeAccountHeaders(w)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	auth.serverURL = srv.URL

	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.conn.Account(ctx); err != nil {
		t.Fatalf("Account after 401 should replay and succeed, got: %v", err)
	}
	if auth.authCalls != 2 {
		t.Fatalf("auth endpoint called %d times, want 2 (initial + one reauth)", auth.authCalls)
	}
}

// TestStorageURLOverrideSurvivesReauth covers the §9.2 SwiftStack quirk:
// the override must direct every request, including ones replayed after a
// mid-session re-authentication that hands back a different storage URL.
func TestStorageURLOverrideSurvivesReauth(t *testing.T) {
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_advertised"}
	var overrideHits, advertisedHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	mux.HandleFunc("/v1/AUTH_advertised", func(w http.ResponseWriter, r *http.Request) {
		advertisedHits++
		writeAccountHeaders(w)
	})
	mux.HandleFunc("/v1/AUTH_override", func(w http.ResponseWriter, r *http.Request) {
		overrideHits++
		// Force one reauth on the very first storage request.
		if r.Header.Get("X-Auth-Token") == "tok1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeAccountHeaders(w)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	auth.serverURL = srv.URL

	s, err := New(Config{
		AuthURL:            srv.URL + "/auth/v1.0",
		Username:           "acct:user",
		APIKey:             "sekrit",
		StorageURLOverride: srv.URL + "/v1/AUTH_override",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := s.StorageURL(), srv.URL+"/v1/AUTH_override"; got != want {
		t.Fatalf("StorageURL = %q, want override %q", got, want)
	}

	if _, _, err := s.conn.Account(ctx); err != nil {
		t.Fatalf("Account via override: %v", err)
	}
	if advertisedHits != 0 {
		t.Fatalf("advertised storage URL was hit %d times; override must win even across reauth", advertisedHits)
	}
	if overrideHits < 2 {
		t.Fatalf("override URL hit %d times, want the 401'd request plus its replay", overrideHits)
	}
	if auth.authCalls != 2 {
		t.Fatalf("auth endpoint called %d times, want 2", auth.authCalls)
	}
}

func TestTLSModes(t *testing.T) {
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	auth.serverURL = srv.URL

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	cases := []struct {
		name    string
		tls     TLSConfig
		wantErr bool
	}{
		{"system rejects self-signed", TLSConfig{Mode: TLSModeSystem}, true},
		{"insecure accepts self-signed", TLSConfig{Mode: TLSModeInsecure}, false},
		{"custom CA verifies", TLSConfig{Mode: TLSModeCustomCA, CACertPEM: caPEM}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(Config{
				AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit", TLS: tc.tls,
			})
			if err != nil {
				t.Fatal(err)
			}
			err = s.Authenticate(context.Background())
			if tc.wantErr && err == nil {
				t.Fatal("Authenticate succeeded, want TLS failure")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Authenticate: %v", err)
			}
		})
	}

	t.Run("custom CA with garbage PEM is rejected", func(t *testing.T) {
		_, err := New(Config{
			AuthURL: srv.URL + "/auth/v1.0", Username: "u", APIKey: "k",
			TLS: TLSConfig{Mode: TLSModeCustomCA, CACertPEM: []byte("not pem")},
		})
		if err == nil {
			t.Fatal("New accepted an empty/invalid CA bundle")
		}
	})
}
