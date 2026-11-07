package swiftcontrol

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ncw/swift/v2"
)

// guardedAuth wraps the wire library's built-in Keystone v3 authenticator
// (swift.NewV3Authenticator) so two Aeronautes requirements hold for v3
// exactly as the hand-written v1 authenticator holds them for TempAuth:
//
//   - the storage URL the identity catalog advertises is validated before
//     any token travels (ValidateStorageURL) — an HTTPS login must not
//     hand its token to an http:// endpoint, and the endpoint must pass
//     the bookmark's TLS policy (§12.3);
//   - a consistent (token, URL, expiry) snapshot is published from
//     Response, which the wire library runs under its own authLock, so
//     Session reads credentials without racing an implicit 401 reauth
//     (review R-06);
//   - the selected object-store endpoint is bound to the configured
//     region and preserved across re-authentication, so an implicit
//     re-auth cannot move operations or a metadata write to a different
//     region's namespace (Config.Region; ADR 0100).
//
// It WRAPS rather than reimplements: the v3 request, response and catalog
// parsing are the library's well-exercised code, so our surface is only
// these two concerns (see the Keystone v3 auth ADR and the sibling
// v3-authenticator-export.patch, which exports the built-in so Aeronautes
// can hold a handle to it). The storage-URL override that v1 needs does
// not apply here: Keystone's catalog already distinguishes endpoints by
// interface and region, so the operator selects one there rather than
// overriding a single returned URL.
type guardedAuth struct {
	inner   swift.Authenticator
	authURL string
	tlsMode TLSMode
	credentialSnapshot
}

// newGuardedV3Auth builds the v3 authenticator Session presets on the wire
// connection. authURL and tlsMode are the bookmark's; they validate the
// catalog's storage URL under the same policy as the auth endpoint.
func newGuardedV3Auth(authURL string, tlsMode TLSMode) *guardedAuth {
	return &guardedAuth{inner: swift.NewV3Authenticator(), authURL: authURL, tlsMode: tlsMode}
}

func (a *guardedAuth) Request(ctx context.Context, c *swift.Connection) (*http.Request, error) {
	return a.inner.Request(ctx, c)
}

func (a *guardedAuth) Response(ctx context.Context, resp *http.Response) error {
	if err := a.inner.Response(ctx, resp); err != nil {
		return err
	}
	// The built-in selected the object-store endpoint from the catalog;
	// gate that choice before Session hands the token to it.
	storageURL := a.inner.StorageUrl(false)
	if err := ValidateStorageURL(a.authURL, storageURL, a.tlsMode); err != nil {
		return err
	}
	// Bind the namespace: once an endpoint is in use, an implicit re-auth
	// that selects a different one must not silently move operations (and
	// a metadata write) to another region. The configured region pins the
	// selection; this refuses any residual switch as defence in depth,
	// before the new token is published.
	if bound := a.snapshot().storageURL; bound != "" && storageURL != bound {
		return fmt.Errorf("Keystone re-authentication selected a different storage endpoint than the one in use; refusing to switch the account namespace mid-session")
	}
	a.publish(resp.StatusCode, a.inner.Token(), storageURL, a.Expires())
	return nil
}

func (a *guardedAuth) StorageUrl(internal bool) string { return a.inner.StorageUrl(internal) }
func (a *guardedAuth) Token() string                   { return a.inner.Token() }
func (a *guardedAuth) CdnUrl() string                  { return a.inner.CdnUrl() }

// Expires implements swift.Expireser by forwarding the built-in's token
// expiry (from the Keystone token's expires_at), so §26.2 gets the
// server's own statement; the zero time means it did not say.
func (a *guardedAuth) Expires() time.Time {
	if e, ok := a.inner.(swift.Expireser); ok {
		return e.Expires()
	}
	return time.Time{}
}
