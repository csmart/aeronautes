// Package swiftcontrol is Aeronautes' Swift semantic layer (DESIGN.md §9):
// typed, safe operations built on github.com/ncw/swift/v2. The wire library
// is wrapped, never exposed — the frontend and the rest of the application
// talk to this package, and Swift semantics live here, not in UI code.
package swiftcontrol

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ncw/swift/v2"

	"github.com/csmart/aeronautes/internal/applog"
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
	// auth for deployments with a different access endpoint (§9.2).
	// It must survive re-authentication, not just the first Authenticate.
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
	// AuthVersion selects the auth protocol: 0 or 1 is TempAuth v1 (the
	// default), 3 is Keystone v3. New rejects any other value.
	AuthVersion int
	// Keystone v3 (AuthVersion 3) identity for the password method with
	// project scope — the minimal first cut. Username/APIKey carry the
	// user and password; these name the user's domain and the project the
	// token is scoped to. The identity catalog, not an override, selects
	// the storage endpoint, so StorageURLOverride is rejected for v3.
	UserDomain    string
	ProjectName   string
	ProjectDomain string
	// UserID and ProjectID name the user and the project by ID instead
	// (ADR 0113). An ID replaces the matching name and its domain on the
	// wire, because Keystone resolves an ID on its own and a name sent
	// beside it could disagree.
	UserID    string
	ProjectID string
	// Region names the catalog region whose object-store endpoint the
	// token binds to. It is required for v3: a Keystone catalog can
	// advertise object-store endpoints in several regions, and without an
	// explicit choice the selection is ambiguous and can change across an
	// implicit re-auth, silently moving operations — and a metadata
	// write — to another region (ADR 0100).
	Region string
}

// DefaultMaxConnsPerHost is the per-host connection cap a session gets
// when its config names none (ADR 0074 decision 4).
const DefaultMaxConnsPerHost = 16

// Session is an authenticated (or authenticatable) connection to one Swift
// account. It is safe for concurrent use after Authenticate.
type Session struct {
	conn *swift.Connection
	tls  TLSConfig
	// auth is the authenticator preset on conn (v1Auth for TempAuth,
	// guardedAuth for Keystone v3) — the source of the atomic credential
	// snapshot Session reads (review R-06).
	auth snapshotAuth
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
	// does not update it, which only makes refreshIfStale conservative).
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
	conn := &swift.Connection{
		AuthUrl:   cfg.AuthURL,
		UserName:  cfg.Username,
		ApiKey:    cfg.APIKey,
		UserAgent: userAgent,
		Transport: transport,
	}
	var auth snapshotAuth
	switch cfg.AuthVersion {
	case 0, 1:
		conn.AuthVersion = 1
		auth = &v1Auth{
			authURL:   cfg.AuthURL,
			username:  cfg.Username,
			apiKey:    cfg.APIKey,
			userAgent: userAgent,
			tlsMode:   cfg.TLS.Mode,
			override:  cfg.StorageURLOverride,
		}
	case 3:
		if cfg.StorageURLOverride != "" {
			return nil, errors.New("storage URL override is not supported for Keystone v3; the identity catalog selects the endpoint by interface and region")
		}
		if cfg.Region == "" {
			return nil, errors.New("Keystone v3 requires an explicit region: the catalog can advertise object-store endpoints in several regions and the token must bind to the one you reviewed")
		}
		conn.AuthVersion = 3
		conn.Region = cfg.Region
		// The library sends a user's name and domain whenever they are
		// set, even beside an ID, so a side named by ID leaves them empty.
		if cfg.UserID != "" {
			conn.UserId = cfg.UserID
			conn.UserName = ""
		} else {
			conn.Domain = cfg.UserDomain
		}
		if cfg.ProjectID != "" {
			conn.TenantId = cfg.ProjectID
		} else {
			conn.Tenant = cfg.ProjectName
			conn.TenantDomain = cfg.ProjectDomain
		}
		auth = newGuardedV3Auth(cfg.AuthURL, cfg.TLS.Mode, transport)
	default:
		return nil, fmt.Errorf("unsupported auth version %d", cfg.AuthVersion)
	}
	conn.Auth = auth
	return &Session{
		auth:          auth,
		tls:           cfg.TLS,
		admitRequest:  cfg.AdmitRequest,
		connectionCap: cfg.MaxConnsPerHost,
		raw: &http.Client{
			Transport: repeatedReadTransport{inner: transport},
			Timeout:   10 * time.Minute,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errors.New("redirect refused: native Swift calls never follow one (the token must not travel)")
			},
		},
		conn: conn,
	}, nil
}

// Close releases the session's idle connections. A one-shot session, such
// as a bookmark verification, owns its transport and must not leave
// keep-alive connections behind when it returns; cached bookmark sessions
// live as long as their registry and never call this.
func (s *Session) Close() {
	closeIdleConnections(s.conn.Transport)
}

// idleCloser is how the transport wrappers forward a close to the transport
// inside them. It is deliberately not the exported CloseIdleConnections:
// the wire library calls that on any transport advertising it whenever it
// re-authenticates, which would break the requests in flight on the
// connections it closes.
type idleCloser interface {
	closeIdle()
}

// closeIdleConnections closes what a transport, or the transport inside a
// wrapper, keeps idle.
func closeIdleConnections(rt http.RoundTripper) {
	switch closer := rt.(type) {
	case idleCloser:
		closer.closeIdle()
	case interface{ CloseIdleConnections() }:
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
	if err := s.signIn(ctx); err != nil {
		return err
	}
	s.authedAt = time.Now()
	return nil
}

// signIn asks the identity service again, up to signInRetries more times
// a short pause apart, while a sign-in gets no answer at all, such as a
// connection reset or a TLS alert in place of a reply. One store has now
// and then answered a sign-in with a TLS alert and accepted the same
// sign-in a moment later (finding F75). A refusal the store did answer, a
// wrong password among them, is never repeated, and a timeout is not
// either, so a slow store does not keep the operator waiting four times.
func (s *Session) signIn(ctx context.Context) error {
	err := Normalize(s.conn.Authenticate(ctx))
	for retry := 1; retry <= signInRetries && unanswered(err); retry++ {
		applog.Warnf("sign-in got no answer (%s), trying again in %s, %d of %d", signInCause(err), signInRetryPause, retry, signInRetries)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(signInRetryPause):
		}
		err = Normalize(s.conn.Authenticate(ctx))
	}
	return err
}

// The operator's choice of 18 November 2026: three more tries, a few
// seconds apart, before the failure is shown. A variable so tests need not
// wait.
const signInRetries = 3

var signInRetryPause = 2 * time.Second

func unanswered(err error) bool {
	var se *SwiftError
	return errors.As(err, &se) && se.Kind == KindNetwork
}

// signInCause names a failed sign-in's cause without the error's own text,
// which carries the identity URL.
func signInCause(err error) string {
	var alert tls.AlertError
	if errors.As(err, &alert) {
		return alert.Error()
	}
	return "connection failed"
}

// refreshIfStale re-authenticates, under refreshMu, when the token is
// close to expiry (§26.2/ADR 0009): within margin of a KNOWN expiry, or —
// when the backend does not advertise one — past a conservative age.
// Credentials runs it before every engine spawn so no child starts on a
// token about to die; the child itself cannot re-authenticate (its
// environment carries a token, never credentials).
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
	if err := s.signIn(ctx); err != nil {
		return err
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

// ForceReauth invalidates the current token so the next Credentials call
// (every engine spawn takes one) performs a real authentication — the
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
