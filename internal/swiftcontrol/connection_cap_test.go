package swiftcontrol

import (
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ADR 0074 decision 4: every trust mode opens its session with the
// per-host connection cap, the package default when the config names
// none, and the session reports the cap it was opened with.
func TestSessionTransportCarriesTheConnectionCap(t *testing.T) {
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.StartTLS()
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	for _, tc := range []struct {
		name   string
		policy TLSConfig
		cap    int
		want   int
	}{
		{name: "system default", policy: TLSConfig{Mode: TLSModeSystem}, cap: 0, want: DefaultMaxConnsPerHost},
		{name: "system", policy: TLSConfig{Mode: TLSModeSystem}, cap: 3, want: 3},
		{name: "insecure", policy: TLSConfig{Mode: TLSModeInsecure}, cap: 5, want: 5},
		{name: "custom ca", policy: TLSConfig{Mode: TLSModeCustomCA, CACertPEM: ca}, cap: 7, want: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := New(Config{AuthURL: server.URL + "/auth/v1.0", Username: "fixture", APIKey: "fixture", TLS: tc.policy, MaxConnsPerHost: tc.cap})
			if err != nil {
				t.Fatal(err)
			}
			transport := session.conn.Transport.(redirectRefusingTransport).inner.(*http.Transport)
			if transport.MaxConnsPerHost != tc.want || session.ConnectionCap() != tc.want {
				t.Fatalf("cap: transport %d, session %d, want %d", transport.MaxConnsPerHost, session.ConnectionCap(), tc.want)
			}
			if transport == http.DefaultTransport {
				t.Fatal("the session shares the process default transport")
			}
			// A copy of the default transport carries its protocol defaults
			// in a client TLS config; system trust means no roots of its own
			// and verification kept.
			if c := transport.TLSClientConfig; tc.policy.Mode == TLSModeSystem && c != nil && (c.RootCAs != nil || c.InsecureSkipVerify) {
				t.Fatalf("system trust changed: %+v", c)
			}
			if tc.policy.Mode != TLSModeSystem && (transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify != (tc.policy.Mode == TLSModeInsecure)) {
				t.Fatalf("trust policy lost: %+v", transport.TLSClientConfig)
			}
		})
	}
}

// The cap bounds what one session opens to a host: with twelve reads
// held at the server, a session capped at four holds four connections
// and the other reads wait for one to free; a session capped at twelve
// holds twelve. Every read completes once the server answers.
func TestSessionCapsConnectionsPerHost(t *testing.T) {
	for _, tc := range []struct {
		name          string
		cap, requests int
	}{
		{name: "capped", cap: 4, requests: 12},
		{name: "wide", cap: 12, requests: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			var active, peak, held atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/auth/v1.0" {
					w.Header().Set("X-Storage-Url", "http://"+r.Host+"/v1/AUTH_fixture")
					w.Header().Set("X-Auth-Token", "token")
					w.WriteHeader(http.StatusOK)
					return
				}
				held.Add(1)
				<-release
				w.Header().Set("Content-Length", "0")
				w.WriteHeader(http.StatusOK)
			}))
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				switch state {
				case http.StateNew:
					n := active.Add(1)
					for {
						p := peak.Load()
						if n <= p || peak.CompareAndSwap(p, n) {
							break
						}
					}
				case http.StateClosed, http.StateHijacked:
					active.Add(-1)
				}
			}
			server.Start()
			t.Cleanup(server.Close)
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			session, err := New(Config{AuthURL: server.URL + "/auth/v1.0", Username: "fixture", APIKey: "fixture", MaxConnsPerHost: tc.cap})
			if err != nil {
				t.Fatal(err)
			}
			transport := session.conn.Transport.(redirectRefusingTransport).inner.(*http.Transport)
			t.Cleanup(transport.CloseIdleConnections)
			if err := session.Authenticate(t.Context()); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			errs := make(chan error, tc.requests)
			for range tc.requests {
				wg.Add(1)
				go func() {
					defer wg.Done()
					reader, err := session.OpenObjectContent(t.Context(), "owned", "object")
					if err == nil {
						_, err = io.Copy(io.Discard, reader)
						reader.Close()
					}
					errs <- err
				}()
			}
			deadline := time.Now().Add(10 * time.Second)
			for held.Load() < int32(tc.cap) {
				if time.Now().After(deadline) {
					t.Fatalf("the server holds %d reads, want %d", held.Load(), tc.cap)
				}
				time.Sleep(5 * time.Millisecond)
			}
			// The remaining reads have had their chance to dial past the cap.
			time.Sleep(200 * time.Millisecond)
			if h, p := held.Load(), peak.Load(); h != int32(tc.cap) || p > int32(tc.cap) {
				t.Fatalf("held %d reads over a peak of %d connections, cap %d", h, p, tc.cap)
			}
			close(release)
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// substituteRoundTripper stands in for the default transport the way a
// request-observing test does.
type substituteRoundTripper struct{}

func (substituteRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("substitute transport")
}

// A process whose default transport was replaced by something other
// than the standard transport keeps it as it is: system trust cannot
// copy or cap it, and must not fail to open a session over it.
func TestSessionKeepsASubstitutedDefaultTransport(t *testing.T) {
	standard := http.DefaultTransport
	http.DefaultTransport = substituteRoundTripper{}
	t.Cleanup(func() { http.DefaultTransport = standard })
	session, err := New(Config{AuthURL: "http://127.0.0.1:1/auth/v1.0", Username: "fixture", APIKey: "fixture", MaxConnsPerHost: 3})
	if err != nil {
		t.Fatal(err)
	}
	inner := session.conn.Transport.(redirectRefusingTransport).inner
	if _, ok := inner.(substituteRoundTripper); !ok {
		t.Fatalf("system trust replaced the substituted transport with %T", inner)
	}
	if session.ConnectionCap() != 3 {
		t.Fatalf("session cap = %d, want the configured 3", session.ConnectionCap())
	}
}
