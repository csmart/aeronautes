package swiftcontrol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
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

// A sign-in the store never answered is asked again, up to three more
// times (finding F75); one it answered, even with a refusal, is left to the
// wire library's own handling.
func TestSignInAsksAgainOnlyWhenUnanswered(t *testing.T) {
	pause := signInRetryPause
	signInRetryPause = time.Millisecond
	t.Cleanup(func() { signInRetryPause = pause })
	cases := []struct {
		name      string
		drops     int // how many sign-ins the store closes without answering
		key       string
		wantErr   ErrorKind
		wantCalls int
	}{
		{name: "one dropped sign-in", drops: 1, key: "sekrit", wantCalls: 2},
		{name: "three dropped sign-ins", drops: 3, key: "sekrit", wantCalls: 4},
		{name: "every sign-in dropped", drops: 9, key: "sekrit", wantErr: KindNetwork, wantCalls: 4},
		// The wire library itself repeats a 401 once, with an alternate form.
		{name: "wrong key", key: "wrong", wantErr: KindUnauthorized, wantCalls: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
			var mu sync.Mutex
			calls := 0
			mux := http.NewServeMux()
			mux.HandleFunc("/auth/v1.0", func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls++
				drop := calls <= tc.drops
				mu.Unlock()
				if drop {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						conn.Close()
					}
					return
				}
				auth.handle(w, r)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			auth.serverURL = srv.URL

			s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: tc.key})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			err = s.Authenticate(context.Background())
			var se *SwiftError
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Authenticate: %v", err)
			case tc.wantErr != "" && (!errors.As(err, &se) || se.Kind != tc.wantErr):
				t.Fatalf("Authenticate error = %v, want kind %s", err, tc.wantErr)
			}
			mu.Lock()
			defer mu.Unlock()
			if calls != tc.wantCalls {
				t.Errorf("sign-in requests = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}
