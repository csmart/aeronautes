package swiftcontrol

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestReturnedStorageEndpointPolicy(t *testing.T) {
	cases := []struct {
		name, storage string
		mode          TLSMode
		allowed       bool
	}{
		{"https", "https://swift.example/v1/AUTH_test", TLSModeSystem, true},
		{"loopback v4", "http://127.0.0.1/v1/AUTH_test", TLSModeSystem, true},
		{"loopback v6", "http://[::1]/v1/AUTH_test", TLSModeSystem, true},
		{"localhost", "http://localhost/v1/AUTH_test", TLSModeSystem, true},
		{"private without opt in", "http://10.0.0.1/v1/AUTH_test", TLSModeSystem, false},
		{"private with opt in", "http://10.0.0.1/v1/AUTH_test", TLSModeInsecure, true},
		{"private v6 without opt in", "http://[fd00::1]/v1/AUTH_test", TLSModeSystem, false},
		{"private v6 with opt in", "http://[fd00::1]/v1/AUTH_test", TLSModeInsecure, true},
		{"public address", "http://192.0.2.10/v1/AUTH_test", TLSModeSystem, false},
		{"public insecure", "http://192.0.2.10/v1/AUTH_test", TLSModeInsecure, false},
		{"public hostname", "http://swift.example/v1/AUTH_test", TLSModeInsecure, false},
		{"credentials", "https://user:password@swift.example/v1/AUTH_test", TLSModeSystem, false},
		{"query", "https://swift.example/v1/AUTH_test?x=1", TLSModeSystem, false},
		{"fragment", "https://swift.example/v1/AUTH_test#x", TLSModeSystem, false},
		{"missing hostname", "https://:443/v1/AUTH_test", TLSModeSystem, false},
		{"missing endpoint", "", TLSModeSystem, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Storage-Url", tc.storage)
				w.Header().Set("X-Auth-Token", "synthetic-token")
			}))
			t.Cleanup(auth.Close)
			s, err := New(Config{AuthURL: auth.URL, Username: "test:tester", APIKey: "testing", TLS: TLSConfig{Mode: tc.mode}})
			if err != nil {
				t.Fatal(err)
			}
			err = s.Authenticate(context.Background())
			if tc.allowed {
				if err != nil {
					t.Fatal(err)
				}
				if s.StorageURL() != tc.storage || s.AuthToken() != "synthetic-token" {
					t.Fatal("accepted endpoint was not published with its token")
				}
			} else {
				if err == nil {
					t.Fatal("authentication accepted a forbidden storage endpoint")
				}
				if s.StorageURL() != "" || s.AuthToken() != "" {
					t.Fatal("rejected endpoint published usable credentials")
				}
			}
		})
	}
}

func TestStorageEndpointPolicyOnReauth(t *testing.T) {
	var calls atomic.Int32
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		storage := "https://swift.example/v1/AUTH_test"
		if n > 1 {
			storage = "http://192.0.2.10/v1/AUTH_test"
		}
		w.Header().Set("X-Storage-Url", storage)
		w.Header().Set("X-Auth-Token", fmt.Sprintf("synthetic-token-%d", n))
	}))
	t.Cleanup(auth.Close)
	s, err := New(Config{AuthURL: auth.URL, Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Authenticate(context.Background()); err == nil {
		t.Fatal("reauthentication accepted a forbidden storage endpoint")
	}
	if s.StorageURL() != "https://swift.example/v1/AUTH_test" || s.AuthToken() != "synthetic-token-1" {
		t.Fatal("failed reauthentication replaced the accepted endpoint/token")
	}
}

func TestStorageOverrideWinsOverAdvertisedEndpoint(t *testing.T) {
	for _, advertised := range []string{"http://192.0.2.10/v1/AUTH_test", "http://swift.lab/v1/AUTH_test", ""} {
		t.Run(advertised, func(t *testing.T) {
			var calls atomic.Int32
			auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Storage-Url", advertised)
				w.Header().Set("X-Auth-Token", fmt.Sprintf("synthetic-token-%d", calls.Add(1)))
			}))
			t.Cleanup(auth.Close)
			override := auth.URL + "/v1/AUTH_override"
			s, err := New(Config{AuthURL: auth.URL, StorageURLOverride: override, Username: "test:tester", APIKey: "testing"})
			if err != nil {
				t.Fatal(err)
			}
			for n := 1; n <= 2; n++ {
				if err := s.Authenticate(context.Background()); err != nil {
					t.Fatal(err)
				}
				if s.StorageURL() != override || s.AuthToken() != fmt.Sprintf("synthetic-token-%d", n) {
					t.Fatalf("authentication %d did not preserve the override with the new token", n)
				}
			}
			if calls.Load() != 2 {
				t.Fatalf("auth calls = %d, want 2", calls.Load())
			}
		})
	}
}

func TestStorageEndpointCannotDowngradeHTTPS(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("override=%t", override), func(t *testing.T) {
			auth := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Storage-Url", "http://127.0.0.1/v1/AUTH_test")
				w.Header().Set("X-Auth-Token", "synthetic-token")
			}))
			t.Cleanup(auth.Close)
			cfg := Config{AuthURL: auth.URL, Username: "test:tester", APIKey: "testing", TLS: TLSConfig{
				Mode: TLSModeCustomCA, CACertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: auth.Certificate().Raw}),
			}}
			if override {
				cfg.StorageURLOverride = "http://127.0.0.1/v1/AUTH_override"
			}
			s, err := New(cfg)
			if err == nil {
				err = s.Authenticate(context.Background())
			}
			if err == nil {
				t.Fatal("HTTPS authentication allowed a plaintext storage endpoint")
			}
		})
	}
}
