package swiftcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
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
	return s.openObject(ctx, container, name, false)
}

// OpenObjectChecked is OpenObject with the store's entity tag checked
// against the bytes read: when the whole body of a plain object has been
// read, closing it reports bytes whose MD5 is not the tag. A large object's
// tag is made from its segments' tags, so the library does not check it.
func (s *Session) OpenObjectChecked(ctx context.Context, container, name string) (io.ReadCloser, *ObjectDetail, error) {
	return s.openObject(ctx, container, name, true)
}

func (s *Session) openObject(ctx context.Context, container, name string, checkHash bool) (io.ReadCloser, *ObjectDetail, error) {
	file, headers, err := s.conn.ObjectOpen(ctx, container, name, checkHash, nil)
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

// DynamicManifestHeader escapes raw names once: Swift URL-decodes this
// header before looking up the segments, including any literal %XX names.
func DynamicManifestHeader(container, prefix string) string {
	return (&url.URL{Path: container + "/" + prefix}).EscapedPath()
}

// StaticSegment is one whole object a static manifest names, with the
// size and entity tag the store must find it at.
type StaticSegment struct {
	Container, Name string
	ETag            string
	Bytes           int64
}

// EngineModTimeHeader is the metadata header under which the transfer engine
// records a modification time on every object it writes.
const EngineModTimeHeader = "X-Object-Meta-Mtime"

// EngineModTime is that header and its value for t.
func EngineModTime(t time.Time) (header, value string) {
	return EngineModTimeHeader, swift.TimeToFloatString(t)
}

// PostObject replaces an object's user metadata, content headers and expiry
// with the given headers in one POST, and reads nothing before or after it.
// A dynamic manifest's X-Object-Manifest has to be among them: the store
// drops the one it had with the rest (measured on the owned Swift).
func (s *Session) PostObject(ctx context.Context, container, name string, headers map[string]string) error {
	_, err := s.post(ctx, container, name, swift.Headers(headers))
	return err
}

// PutStaticManifest writes a static large object (?multipart-manifest=put)
// naming each segment by path, entity tag and size, in order. The store
// checks every named segment against its entry and refuses the whole
// manifest, storing nothing, when one does not match. The headers ride
// the PUT verbatim, as PutObject's do; the returned value is the stored
// object's entity tag when the server reports one.
func (s *Session) PutStaticManifest(ctx context.Context, container, name string, segments []StaticSegment, contentType string, headers map[string]string) (string, error) {
	type entry struct {
		Path      string `json:"path"`
		ETag      string `json:"etag"`
		SizeBytes int64  `json:"size_bytes"`
	}
	entries := make([]entry, len(segments))
	for i, segment := range segments {
		entries[i] = entry{Path: "/" + segment.Container + "/" + segment.Name, ETag: segment.ETag, SizeBytes: segment.Bytes}
	}
	body, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	credentials, err := s.Credentials(ctx, 0)
	if err != nil {
		return "", err
	}
	requestHeaders := swift.Headers{}
	for k, v := range headers {
		requestHeaders[k] = v
	}
	if contentType != "" {
		requestHeaders["Content-Type"] = contentType
	}
	_, respHeaders, err := s.conn.Call(ctx, credentials.StorageURL, swift.RequestOpts{
		Container:  container,
		ObjectName: name,
		Operation:  "PUT",
		Parameters: url.Values{"multipart-manifest": []string{"put"}},
		Headers:    requestHeaders,
		Body:       bytes.NewReader(body),
		NoResponse: true,
		OnReAuth:   s.auth.storageTarget,
	})
	if err != nil {
		return "", WithTransID(Normalize(err), respHeaders)
	}
	return etagValue(respHeaders["Etag"]), nil
}

// DeleteObject removes exactly the named object — for a DLO/SLO
// manifest, the manifest alone. Used for Aeronautes' own staging litter
// and captured-orphan sweeps; job flows never call it on names they did
// not create (§28.6 zero-destruction invariant for Phase 5).
func (s *Session) DeleteObject(ctx context.Context, container, name string) error {
	return Normalize(s.conn.ObjectDelete(ctx, container, name))
}
