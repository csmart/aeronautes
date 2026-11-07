package swiftcontrol

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/ncw/swift/v2"
)

// authSnapshot is one consistent (token, storage URL, expiry) triple.
type authSnapshot struct {
	token, storageURL string
	expires           time.Time
}

// credentialSnapshot holds the atomically-published credential triple
// shared by both authenticators (review R-06). Each authenticator embeds
// it and calls publish from its Response, which the wire library runs
// inside its own authLock, so a stored value is consistent; Session reads
// it through snapshot()/storageTarget() with an atomic load instead of
// racing the connection's exported StorageUrl/AuthToken fields during an
// implicit 401 reauth. One shared copy keeps the v1 and v3 paths from
// drifting apart on this concurrency-sensitive logic.
type credentialSnapshot struct {
	snap atomic.Pointer[authSnapshot]
}

// publish stores a new triple ONLY on a genuinely successful auth. A
// non-2xx (a 401 caught during a token-rollover window) or a response
// missing the token or storage URL must never clobber a good snapshot
// with empty credentials, or a concurrent reader would get an empty
// token/URL. Callers validate the storage URL before publishing; the
// empty check here keeps the primitive safe on its own.
func (c *credentialSnapshot) publish(statusCode int, token, storageURL string, expires time.Time) {
	if statusCode/100 == 2 && token != "" && storageURL != "" {
		c.snap.Store(&authSnapshot{token: token, storageURL: storageURL, expires: expires})
	}
}

// snapshot returns the last published credential triple, or the zero
// snapshot before first authentication.
func (c *credentialSnapshot) snapshot() authSnapshot {
	if s := c.snap.Load(); s != nil {
		return *s
	}
	return authSnapshot{}
}

// storageTarget runs under the wire connection's authentication lock, so
// its accepted URL belongs to the token selected in that same critical
// section (the OnReAuth callback the native requests use).
func (c *credentialSnapshot) storageTarget() (string, error) {
	target := c.snapshot().storageURL
	if target == "" {
		return "", errors.New("no accepted Swift storage URL")
	}
	return target, nil
}

// snapshotAuth is the authenticator contract Session holds: a wire
// authenticator that also exposes the published credential snapshot and
// the re-auth storage target. v1Auth and guardedAuth both satisfy it by
// embedding credentialSnapshot.
type snapshotAuth interface {
	swift.Authenticator
	snapshot() authSnapshot
	storageTarget() (string, error)
}
