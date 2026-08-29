package swiftcontrol

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/ncw/swift/v2"
)

// etagValue strips the RFC quotes some paths add (a large object's HEAD
// ETag arrives quoted).
func etagValue(e string) string { return strings.Trim(e, `"`) }

// The transfer primitives: the small native surface the copy executor
// drives (§28.6). All of them are manifest-only by construction — a
// native DELETE or PUT touches exactly the named object and never
// follows segments, which is what makes the native path immune to the
// §28.6 shared-segment corruption rclone's Update/Remove can cause.

// OpenObject GETs one object as the server serves it: for a DLO/SLO
// manifest that is the assembled logical stream. The returned detail is
// built from the response headers (Class still identifies a manifest).
func (s *Session) OpenObject(ctx context.Context, container, name string) (io.ReadCloser, *ObjectDetail, error) {
	file, headers, err := s.conn.ObjectOpen(ctx, container, name, false, nil)
	if err != nil {
		return nil, nil, Normalize(err)
	}
	detail := buildObjectDetail(canonicalHeaders(headers), time.Now().UTC())
	detail.Name = name
	if length, err := file.Length(ctx); err == nil {
		detail.ContentLength = length
	}
	detail.ContentType = headers["Content-Type"]
	detail.ETag = etagValue(headers["Etag"])
	return file, detail, nil
}

// PutObject writes one object in a single PUT (create or atomic
// replace), carrying the given headers verbatim — the §27.4 metadata
// set, a DLO X-Object-Manifest for a manifest-swap commit, or both.
// Returns the stored ETag when the server reports one.
func (s *Session) PutObject(ctx context.Context, container, name string, body io.Reader, contentType string, headers map[string]string) (string, error) {
	respHeaders, err := s.conn.ObjectPut(ctx, container, name, body, false, "", contentType, swift.Headers(headers))
	if err != nil {
		return "", Normalize(err)
	}
	return etagValue(respHeaders["Etag"]), nil
}

// CopyObject is the server-side COPY commit for plain objects
// (§28.6(a)): same account, no client hairpin, metadata carried and the
// given headers applied on top.
func (s *Session) CopyObject(ctx context.Context, srcContainer, srcName, dstContainer, dstName string, headers map[string]string) error {
	_, err := s.conn.ObjectCopy(ctx, srcContainer, srcName, dstContainer, dstName, swift.Headers(headers))
	return Normalize(err)
}

// PostObjectHeaders sends a raw object POST with exactly the given
// headers. Swift object POST is REPLACE-ALL for user metadata, the DLO
// manifest, and expiry — the caller must send the complete intended set
// (the §17.1 trap the executor's finalize handles by re-sending the
// destination's own X-Object-Manifest).
func (s *Session) PostObjectHeaders(ctx context.Context, container, name string, headers map[string]string) error {
	return Normalize(s.conn.ObjectUpdate(ctx, container, name, swift.Headers(headers)))
}

// DeleteObject removes exactly the named object — for a DLO/SLO
// manifest, the manifest alone. Used for Aeronautes' own staging litter
// and captured-orphan sweeps; job flows never call it on names they did
// not create (§28.6 zero-destruction invariant for Phase 5).
func (s *Session) DeleteObject(ctx context.Context, container, name string) error {
	return Normalize(s.conn.ObjectDelete(ctx, container, name))
}
