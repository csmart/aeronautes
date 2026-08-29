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
	conn *swift.Connection
}

// New builds a Session for cfg. It does not touch the network; call
// Authenticate (or any operation, which authenticates lazily). The
// connection is preset with Aeronautes' own v1 authenticator so token
// expiry is captured and the storage-URL override holds across re-auths.
func New(cfg Config) (*Session, error) {
	transport, err := cfg.TLS.transport()
	if err != nil {
		return nil, err
	}
	const userAgent = "aeronautes"
	return &Session{
		conn: &swift.Connection{
			AuthUrl:     cfg.AuthURL,
			UserName:    cfg.Username,
			ApiKey:      cfg.APIKey,
			AuthVersion: 1,
			UserAgent:   userAgent,
			Transport:   transport,
			Auth: &v1Auth{
				authURL:   cfg.AuthURL,
				username:  cfg.Username,
				apiKey:    cfg.APIKey,
				userAgent: userAgent,
				override:  cfg.StorageURLOverride,
			},
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
	return nil
}

// StorageURL returns the storage URL in effect (override included), or ""
// before authentication.
func (s *Session) StorageURL() string {
	return s.conn.StorageUrl
}

// TokenExpires returns when the current token expires, or the zero time
// when the auth backend does not say (§26.2 falls through its priority
// order: configured per-bookmark lifetime, then failure-driven reauth).
func (s *Session) TokenExpires() time.Time {
	return s.conn.Expires
}

// AuthToken returns the current auth token — for handing to the rclone
// child's environment (§26.2) ONLY. It must never cross the frontend
// bridge or reach a log or database (K-SEC-002/003).
func (s *Session) AuthToken() string {
	return s.conn.AuthToken
}
