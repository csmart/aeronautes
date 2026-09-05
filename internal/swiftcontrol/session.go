// Package swiftcontrol is Aeronautes' Swift semantic layer (DESIGN.md §9):
// typed, safe operations built on github.com/ncw/swift/v2. The wire library
// is wrapped, never exposed — the frontend and the rest of the application
// talk to this package, and Swift semantics live here, not in UI code.
package swiftcontrol

import (
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
}

// Session is an authenticated (or authenticatable) connection to one Swift
// account. It is safe for concurrent use after Authenticate.
type Session struct {
	conn *swift.Connection
	// auth is the v1 authenticator preset on conn — the source of the
	// atomic credential snapshot Session reads (review R-06).
	auth *v1Auth
	// raw serves the requests the wire library has no verb for (bulk
	// delete): the SAME bookmark transport (custom CA / insecure), no
	// redirect following (a redirect would resend X-Auth-Token wherever
	// it pointed), and a ceiling instead of an unbounded hang.
	raw *http.Client

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
	if err := ValidateEndpointURL(cfg.AuthURL, cfg.TLS.Mode); err != nil {
		return nil, fmt.Errorf("auth URL: %w", err)
	}
	if cfg.StorageURLOverride != "" {
		if err := ValidateStorageURL(cfg.AuthURL, cfg.StorageURLOverride, cfg.TLS.Mode); err != nil {
			return nil, err
		}
	}
	transport, err := cfg.TLS.transport()
	if err != nil {
		return nil, err
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
		auth: auth,
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

// Authenticate performs v1 auth: GET AuthURL with X-Auth-User/X-Auth-Key,
// keeping the returned token and storage URL. The wire library also
// re-authenticates once and replays when a request meets 401 (§9.2); the
// authenticator preserves the storage-URL override across those re-auths.
func (s *Session) Authenticate(ctx context.Context) error {
	if err := s.conn.Authenticate(ctx); err != nil {
		return Normalize(err)
	}
	s.refreshMu.Lock()
	s.authedAt = time.Now()
	s.refreshMu.Unlock()
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
	expires := s.auth.snapshot().expires
	stale := false
	if !expires.IsZero() {
		stale = time.Until(expires) < margin
	} else {
		stale = s.authedAt.IsZero() || time.Since(s.authedAt) > 10*time.Minute
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

// AuthToken returns the current auth token — for handing to the rclone
// child's environment (§26.2) ONLY. It must never cross the frontend
// bridge or reach a log or database (K-SEC-002/003).
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
