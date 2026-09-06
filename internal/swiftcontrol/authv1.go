package swiftcontrol

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ncw/swift/v2"
)

// v1Auth is Aeronautes' own TempAuth/v1 authenticator, preset on the
// wire connection so it replaces the library's builtin. Two reasons it
// exists (§9.2, §26.2):
//
//   - the builtin ignores X-Auth-Token-Expires, but the §26.2 lifetime
//     priority puts the server's own statement first — TempAuth sends the
//     remaining lifetime in seconds (verified against upstream SAIO);
//   - the storage-URL override must hold across every re-auth, which is
//     naturally an authenticator property, not a one-shot field write.
//
// The wire library serialises Request/Response under its auth lock;
// accessor methods are only called on an authenticated connection.
type v1Auth struct {
	authURL   string
	username  string
	apiKey    string
	userAgent string
	tlsMode   TLSMode
	// override, when set, replaces the storage URL the server returns.
	override string

	headers http.Header
	expires time.Time
	// snap is an atomically-published credential snapshot captured on
	// every (re)authentication — Response runs inside ncw/swift's own
	// authLock, so a value stored here is consistent, and Session reads
	// it via atomic load instead of racing the connection's exported
	// fields during an implicit 401 reauth (review R-06).
	snap atomic.Pointer[authSnapshot]
}

// authSnapshot is one consistent (token, storage URL, expiry) triple.
type authSnapshot struct {
	token, storageURL string
	expires           time.Time
}

func (a *v1Auth) Request(ctx context.Context, _ *swift.Connection) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.authURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", a.userAgent)
	req.Header.Set("X-Auth-User", a.username)
	req.Header.Set("X-Auth-Key", a.apiKey)
	return req, nil
}

func (a *v1Auth) Response(_ context.Context, resp *http.Response) error {
	storageURL := a.override
	if storageURL == "" {
		storageURL = resp.Header.Get("X-Storage-Url")
	}
	if err := ValidateStorageURL(a.authURL, storageURL, a.tlsMode); err != nil {
		return err
	}
	a.headers = resp.Header
	a.expires = parseV1Expiry(resp.Header.Get("X-Auth-Token-Expires"), time.Now())
	token := resp.Header.Get("X-Auth-Token")
	// Publish the snapshot ONLY on a genuinely successful auth: a
	// non-2xx (a 401 caught during a token-rollover window) or a
	// tokenless response must NEVER clobber a good snapshot with empty
	// credentials, or a concurrent reader would get an empty storage
	// URL/token (the R-06 snapshot's own corner).
	if resp.StatusCode/100 == 2 && token != "" {
		a.snap.Store(&authSnapshot{token: token, storageURL: storageURL, expires: a.expires})
	}
	return nil
}

// snapshot returns the last published credential triple, or the zero
// snapshot before first authentication.
func (a *v1Auth) snapshot() authSnapshot {
	if s := a.snap.Load(); s != nil {
		return *s
	}
	return authSnapshot{}
}

// storageTarget runs under the wire connection's authentication lock, so
// its accepted URL belongs to the token selected in that same critical section.
func (a *v1Auth) storageTarget() (string, error) {
	target := a.snapshot().storageURL
	if target == "" {
		return "", errors.New("no accepted Swift storage URL")
	}
	return target, nil
}

// StorageUrl ignores the library's internal/service-net flag: Aeronautes
// targets the advertised endpoint (or the operator's explicit override),
// never a derived snet- hostname.
func (a *v1Auth) StorageUrl(_ bool) string {
	if a.override != "" {
		return a.override
	}
	return a.headers.Get("X-Storage-Url")
}

func (a *v1Auth) Token() string {
	return a.headers.Get("X-Auth-Token")
}

func (a *v1Auth) CdnUrl() string {
	return a.headers.Get("X-CDN-Management-Url")
}

// Expires implements swift.Expireser; the zero time means the server did
// not say (§26.2 falls through its priority order).
func (a *v1Auth) Expires() time.Time {
	return a.expires
}

// parseV1Expiry interprets X-Auth-Token-Expires. TempAuth sends delta
// seconds remaining; some deployments send an absolute unix timestamp —
// values large enough to be an epoch (>= 1e9, i.e. beyond ~31 years as a
// delta) are treated as one. Unparseable or absent values mean unknown.
func parseV1Expiry(value string, now time.Time) time.Time {
	if value == "" {
		return time.Time{}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return time.Time{}
	}
	if n >= 1_000_000_000 {
		return time.Unix(n, 0)
	}
	return now.Add(time.Duration(n) * time.Second)
}
