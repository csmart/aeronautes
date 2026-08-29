package swiftcontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenExpiresFromDeltaHeader(t *testing.T) {
	auth := &fakeAuth{
		user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct",
		expiresHeader: "86399", // TempAuth style: seconds remaining
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	auth.serverURL = srv.URL

	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if err := s.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	expires := s.TokenExpires()
	if expires.IsZero() {
		t.Fatal("TokenExpires must be captured from X-Auth-Token-Expires")
	}
	remaining := expires.Sub(before)
	if remaining < 86300*time.Second || remaining > 86500*time.Second {
		t.Errorf("expiry %v from now, want ~86399s", remaining)
	}
}

func TestTokenExpiresUnknownWithoutHeader(t *testing.T) {
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
		t.Fatal(err)
	}
	if !s.TokenExpires().IsZero() {
		t.Errorf("TokenExpires = %v, want zero when the server does not say", s.TokenExpires())
	}
}

func TestParseV1Expiry(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		value string
		want  time.Time
	}{
		{"", time.Time{}},
		{"junk", time.Time{}},
		{"-5", time.Time{}},
		{"3600", now.Add(time.Hour)},
		// Absolute unix epoch values pass through as-is.
		{"1787000000", time.Unix(1787000000, 0)},
	}
	for _, c := range cases {
		if got := parseV1Expiry(c.value, now); !got.Equal(c.want) {
			t.Errorf("parseV1Expiry(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}
