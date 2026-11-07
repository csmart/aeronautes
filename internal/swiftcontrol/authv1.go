package swiftcontrol

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/ncw/swift/v2"
)

// v1Auth is Aeronautes' own TempAuth/v1 authenticator, preset on the wire
// connection so it replaces the library's builtin. It exists because the
// builtin v1 authenticator offers none of four things Aeronautes needs,
// two of which can only be enforced from inside the auth exchange
// (§9.2, §26.2, §12.3, review R-06):
//
//   - §26.2 token lifetime: the builtin v1 implements no Expires(), but
//     the lifetime priority puts the server's own statement first, so we
//     read X-Auth-Token-Expires (TempAuth sends the remaining seconds,
//     verified against upstream SAIO);
//   - the storage-URL override must hold across every re-auth, which is
//     naturally an authenticator property, not a one-shot field write;
//   - ValidateStorageURL gates the storage URL before any token travels,
//     so an HTTPS login cannot hand its token to an http:// endpoint; and
//   - the (token, URL, expiry) snapshot is published here, under the wire
//     library's authLock, so Session reads credentials without racing an
//     implicit 401 reauth.
//
// The v1 protocol is a GET with two headers, so reimplementing the wire
// authenticator was cheaper than exporting and wrapping the builtin. The
// Keystone v3 authenticator makes the opposite trade: its protocol is
// substantial, so guardedAuth WRAPS the builtin (see authv3.go) and adds
// only the gate and snapshot. The shared credentialSnapshot keeps both
// paths identical on that concurrency-sensitive step (ADR 0100).
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
	// credentialSnapshot publishes the (token, URL, expiry) triple from
	// Response under the wire library's authLock; Session reads it without
	// racing an implicit 401 reauth (review R-06). Shared with the v3
	// authenticator so both paths capture credentials identically.
	credentialSnapshot
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
	a.publish(resp.StatusCode, token, storageURL, a.expires)
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
