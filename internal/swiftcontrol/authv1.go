package swiftcontrol

import (
	"context"
	"net/http"
	"strconv"
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
	// override, when set, replaces the storage URL the server returns.
	override string

	headers http.Header
	expires time.Time
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
	a.headers = resp.Header
	a.expires = parseV1Expiry(resp.Header.Get("X-Auth-Token-Expires"), time.Now())
	return nil
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
