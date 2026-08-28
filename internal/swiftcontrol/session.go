// Package swiftcontrol is Aeronautes' Swift semantic layer (DESIGN.md §9):
// typed, safe operations built on github.com/ncw/swift/v2. The wire library
// is wrapped, never exposed — the frontend and the rest of the application
// talk to this package, and Swift semantics live here, not in UI code.
package swiftcontrol

import (
	"context"
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
	conn     *swift.Connection
	override string
}

// New builds a Session for cfg. It does not touch the network; call
// Authenticate (or any operation, which authenticates lazily).
func New(cfg Config) (*Session, error) {
	transport, err := cfg.TLS.transport()
	if err != nil {
		return nil, err
	}
	return &Session{
		conn: &swift.Connection{
			AuthUrl:     cfg.AuthURL,
			UserName:    cfg.Username,
			ApiKey:      cfg.APIKey,
			AuthVersion: 1,
			UserAgent:   "aeronautes",
			Transport:   transport,
		},
		override: cfg.StorageURLOverride,
	}, nil
}

// Authenticate performs v1 auth: GET AuthURL with X-Auth-User/X-Auth-Key,
// keeping the returned token and storage URL. The wire library also
// re-authenticates once and replays when a request meets 401 (§9.2), and
// the storage-URL override is preserved across those re-auths.
func (s *Session) Authenticate(ctx context.Context) error {
	if err := s.conn.Authenticate(ctx); err != nil {
		return Normalize(err)
	}
	s.applyStorageURLOverride()
	return nil
}

// applyStorageURLOverride pins the storage URL to the configured override.
// Authenticate (initial and internal re-auth) sets Connection.StorageUrl
// from the authenticator, so the override lives in a wrapping
// Authenticator rather than a one-shot field write. Called after the
// first successful Authenticate, before the connection is used
// concurrently.
func (s *Session) applyStorageURLOverride() {
	if s.override == "" {
		return
	}
	if _, wrapped := s.conn.Auth.(storageURLOverrideAuth); !wrapped {
		s.conn.Auth = storageURLOverrideAuth{Authenticator: s.conn.Auth, storageURL: s.override}
	}
	s.conn.StorageUrl = s.override
}

// StorageURL returns the storage URL in effect (override included), or ""
// before authentication.
func (s *Session) StorageURL() string {
	return s.conn.StorageUrl
}

// TokenExpires returns when the current token expires, or the zero time
// when the auth backend does not say — common for v1 auth, and exactly the
// uncertainty DESIGN.md §26.2's lifetime priority order exists for.
func (s *Session) TokenExpires() time.Time {
	return s.conn.Expires
}

// storageURLOverrideAuth wraps the real authenticator and pins the storage
// URL, so a mid-call re-authentication cannot silently undo the override.
type storageURLOverrideAuth struct {
	swift.Authenticator
	storageURL string
}

func (a storageURLOverrideAuth) StorageUrl(bool) string {
	return a.storageURL
}

// Expires passes through the inner authenticator's expiry, since embedding
// only promotes Authenticator methods.
func (a storageURLOverrideAuth) Expires() time.Time {
	if e, ok := a.Authenticator.(swift.Expireser); ok {
		return e.Expires()
	}
	return time.Time{}
}
