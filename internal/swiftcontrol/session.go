// Package swiftcontrol is Aeronautes' Swift semantic layer (DESIGN.md §9):
// typed, safe operations built on github.com/ncw/swift/v2. The wire library
// is wrapped, never exposed — the frontend and the rest of the application
// talk to this package, and Swift semantics live here, not in UI code.
package swiftcontrol

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ncw/swift/v2"
)

// Config carries one bookmark's connection parameters. The API key comes
// from the SecretStore at session construction; it is never persisted by
// this package.
type Config struct {
	// AuthURL is the v1 auth endpoint, e.g. https://swift.example/auth/v1.0.
	AuthURL string
	// Username is the TempAuth-style account:user identity (§9.2).
	Username string
	// APIKey is the durable auth key (X-Auth-Key).
	APIKey string
	// StorageURLOverride, when set, replaces the storage URL returned by
	// auth — a classic SwiftStack deployment quirk (§9.2). It must survive
	// re-authentication, not just the first Authenticate.
	StorageURLOverride string
	// TLS is this bookmark's TLS policy (§12.3).
	TLS TLSConfig
	// AdmitRequest binds held sessions to the captured bookmark generation.
	// Standalone sessions may omit it; bookmark sessions must supply it.
	AdmitRequest func(context.Context) error `json:"-"`
	// MaxConnsPerHost caps the connections this session opens to one
	// host, so a burst of reads and scans cannot open them without bound
	// (ADR 0074 decision 4); zero means DefaultMaxConnsPerHost.
	MaxConnsPerHost int
}

// DefaultMaxConnsPerHost is the per-host connection cap a session gets
// when its config names none (ADR 0074 decision 4).
const DefaultMaxConnsPerHost = 16

// Session is an authenticated (or authenticatable) connection to one Swift
// account. It is safe for concurrent use after Authenticate.
type Session struct {
	conn *swift.Connection
	tls  TLSConfig
	// auth is the v1 authenticator preset on conn — the source of the
	// atomic credential snapshot Session reads (review R-06).
	auth *v1Auth
	// raw serves the requests the wire library has no verb for (bulk
	// delete): the SAME bookmark transport (custom CA / insecure), no
	// redirect following (a redirect would resend X-Auth-Token wherever
	// it pointed), and a ceiling instead of an unbounded hang.
	raw          *http.Client
	admitRequest func(context.Context) error
	// connectionCap is the per-host cap the session was opened with.
	connectionCap int

	// refreshMu single-flights explicit refreshes; authedAt is the last
	// EXPLICIT authentication (the wire library's own 401-driven re-auth
	// does not update it, which only makes RefreshIfStale conservative).
	refreshMu sync.Mutex
	authedAt  time.Time
}

// New builds a Session for cfg. It does not touch the network; call
// Authenticate (or any operation, which authenticates lazily). The
// connection is preset with Aeronautes' own v1 authenticator so token
// expiry is captured and the storage-URL override holds across re-auths.
func New(cfg Config) (*Session, error) {
	cfg.TLS.CACertPEM = bytes.Clone(cfg.TLS.CACertPEM)
	if err := ValidateEndpointURL(cfg.AuthURL, cfg.TLS.Mode); err != nil {
		return nil, fmt.Errorf("auth URL: %w", err)
	}
	if cfg.StorageURLOverride != "" {
		if err := ValidateStorageURL(cfg.AuthURL, cfg.StorageURLOverride, cfg.TLS.Mode); err != nil {
			return nil, err
		}
	}
	if cfg.MaxConnsPerHost <= 0 {
		cfg.MaxConnsPerHost = DefaultMaxConnsPerHost
	}
	transport, err := cfg.TLS.transport(cfg.MaxConnsPerHost)
	if err != nil {
		return nil, err
	}
	if cfg.AdmitRequest != nil {
		transport = requestAdmissionTransport{inner: transport, admit: cfg.AdmitRequest}
	}
	const userAgent = "aeronautes"
	auth := &v1Auth{
		authURL:   cfg.AuthURL,
		username:  cfg.Username,
		apiKey:    cfg.APIKey,
		userAgent: userAgent,
		tlsMode:   cfg.TLS.Mode,
		override:  cfg.StorageURLOverride,
	}
	return &Session{
		auth:          auth,
		tls:           cfg.TLS,
		admitRequest:  cfg.AdmitRequest,
		connectionCap: cfg.MaxConnsPerHost,
		raw: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Minute,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errors.New("redirect refused: native Swift calls never follow one (the token must not travel)")
			},
		},
		conn: &swift.Connection{
			AuthUrl:     cfg.AuthURL,
			UserName:    cfg.Username,
			ApiKey:      cfg.APIKey,
			AuthVersion: 1,
			UserAgent:   userAgent,
			Transport:   transport,
			Auth:        auth,
		},
	}, nil
}

// Close releases the session's idle connections. A one-shot session, such
// as a bookmark verification, owns its transport and must not leave
// keep-alive connections behind when it returns; cached bookmark sessions
// live as long as their registry and never call this.
func (s *Session) Close() {
	closeIdleConnections(s.conn.Transport)
}

// closeIdleConnections closes what a transport, or the transport inside a
// wrapper, keeps idle.
func closeIdleConnections(rt http.RoundTripper) {
	if closer, ok := rt.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// Authenticate performs v1 auth: GET AuthURL with X-Auth-User/X-Auth-Key,
// keeping the returned token and storage URL. The wire library also
// re-authenticates once and replays when a request meets 401 (§9.2); the
// authenticator preserves the storage-URL override across those re-auths.
func (s *Session) Authenticate(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if err := s.conn.Authenticate(ctx); err != nil {
		return Normalize(err)
	}
	s.authedAt = time.Now()
	return nil
}

// RefreshIfStale re-authenticates when the token is close to expiry
// (§26.2/ADR 0009): within margin of a KNOWN expiry, or — when the
// backend does not advertise one — past a conservative age. Long jobs
// call this before every engine spawn so no child starts on a token
// about to die; the child itself cannot re-authenticate (its
// environment carries a token, never credentials).
func (s *Session) RefreshIfStale(ctx context.Context, margin time.Duration) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	return s.refreshIfStale(ctx, margin)
}

func (s *Session) refreshIfStale(ctx context.Context, margin time.Duration) error {
	expires := s.auth.snapshot().expires
	// ForceReauth clears authedAt even when the rejected token claims
	// a future expiry. Only successful authentication clears that state.
	stale := s.authedAt.IsZero()
	if !expires.IsZero() {
		stale = stale || time.Until(expires) < margin
	} else {
		stale = stale || time.Since(s.authedAt) > 10*time.Minute
	}
	if !stale {
		return nil
	}
	if err := s.conn.Authenticate(ctx); err != nil {
		return Normalize(err)
	}
	s.authedAt = time.Now()
	return nil
}

// StorageCredentials stays inside the Go core: neither it nor its TLS
// material belongs in the renderer, a log or persisted job settings.
type StorageCredentials struct {
	StorageURL string
	Token      string `json:"-"`
	Expires    time.Time
	TLS        TLSConfig
}

// Credentials refreshes and captures one accepted auth response under
// the explicit-refresh lock. A failed or forced refresh cannot expose
// the last rejected token through this API, even if a caller ignores err.
// Trust comes from the immutable session config, never a later bookmark
// edit; callers own the returned CA bytes and cannot change the session.
func (s *Session) Credentials(ctx context.Context, margin time.Duration) (StorageCredentials, error) {
	// A fresh token can bypass HTTP entirely, so transport admission alone
	// would still let an obsolete session hand credentials to a child gateway.
	if err := s.AdmitRequest(ctx); err != nil {
		return StorageCredentials{}, err
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if err := s.refreshIfStale(ctx, margin); err != nil {
		return StorageCredentials{}, err
	}
	snapshot := s.auth.snapshot()
	if snapshot.token == "" || snapshot.storageURL == "" {
		return StorageCredentials{}, errors.New("no accepted Swift credentials")
	}
	tls := s.tls
	tls.CACertPEM = bytes.Clone(tls.CACertPEM)
	return StorageCredentials{
		StorageURL: snapshot.storageURL, Token: snapshot.token,
		Expires: snapshot.expires, TLS: tls,
	}, nil
}

// StorageURL returns the storage URL in effect (override included), or ""
// before authentication.
func (s *Session) StorageURL() string {
	return s.auth.snapshot().storageURL
}

// TokenExpires returns when the current token expires, or the zero time
// when the auth backend does not say (§26.2 falls through its priority
// order: configured per-bookmark lifetime, then failure-driven reauth).
func (s *Session) TokenExpires() time.Time {
	return s.auth.snapshot().expires
}

// AuthToken returns the last accepted token for diagnostics inside tests.
// Callers sending credentials must use Credentials for a refreshed tuple.
// Neither API may cross the frontend or reach a log/database (K-SEC-002/003).
func (s *Session) AuthToken() string {
	return s.auth.snapshot().token
}

// ForceReauth invalidates the current token so the NEXT RefreshIfStale
// (every engine spawn does one) performs a real authentication — the
// retry after an observed 401 must never replay the rejected token
// (review F-06).
func (s *Session) ForceReauth() {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.conn.UnAuthenticate()
	s.authedAt = time.Time{}
}

// ConnectionCap reports the per-host connection cap the session was
// opened with (ADR 0074 decision 4).
func (s *Session) ConnectionCap() int { return s.connectionCap }
