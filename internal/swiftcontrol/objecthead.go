package swiftcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const objectMetaPrefix = "X-Object-Meta-"

// ObjectClass is an object's large-object classification from an
// authoritative HEAD (§20). Listings only ever hint; this is the truth.
type ObjectClass string

const (
	ClassPlain ObjectClass = "plain"
	ClassDLO   ObjectClass = "dlo"
	ClassSLO   ObjectClass = "slo"
)

// ObjectDetail is the object inspector's model (§53): the HEAD response
// taken apart — identity, sizes, DLO/SLO classification, categorised
// user metadata, expiry, and the redacted raw header view.
type ObjectDetail struct {
	Name  string
	Class ObjectClass
	// ContentLength is the HEAD Content-Length: for a DLO/SLO manifest
	// this is the assembled logical size, not the stored manifest bytes.
	ContentLength int64
	ContentType   string
	ETag          string
	LastModified  time.Time
	// Manifest is the DLO X-Object-Manifest value ("container/prefix") as
	// the store sent it, percent-encoded, empty for anything else; read the
	// segments it names through DynamicSegments. SLO manifests are fetched
	// separately when the segment table is opened (?multipart-manifest=get,
	// Phase 4).
	Manifest string
	// DeleteAt is the scheduled expiry (X-Delete-At), zero when none.
	DeleteAt time.Time
	// ContentEncoding/ContentDisposition are part of the §27.4 preserved
	// set on copies.
	ContentEncoding    string
	ContentDisposition string
	// Meta is the user metadata (X-Object-Meta-*).
	Meta map[string]string
	// RawHeaders is the full header set, secret-shaped values redacted.
	RawHeaders map[string]string
	// FollowedSymlink reports the HEAD chased a Swift symlink
	// (Content-Location present): the fields describe the TARGET; the
	// link object itself is addressed with ?symlink=get.
	FollowedSymlink bool
	// SymlinkTarget/-Account are set when the response exposes the link
	// headers (a ?symlink=get HEAD, or a cross-account link Swift will
	// not follow).
	SymlinkTarget        string
	SymlinkTargetAccount string
	// TransID is the Swift transaction id of the HEAD, for diagnostics.
	TransID string
	// EditableFingerprint covers what the metadata editor can change
	// (user meta, Content-Type, X-Delete-At) for §18 stale-write checks.
	EditableFingerprint string
	FetchedAt           time.Time
}

// ContentFingerprint identifies WHAT this name holds — its class, its logical
// length and its entity tag — so a caller that was shown an object can hand it
// back and have a destructive request refused when the name no longer holds it
// (ADR 0085).
//
// It is a method rather than a stored field, and that is the whole point. As a
// field it was computed inside buildObjectDetail, which runs BEFORE the callers
// that assign ContentLength and ETag from the HEAD's own object record — so it
// hashed a zero length and an empty tag, was identical for every object of a
// class, and let a same-class replacement of different length or content pass
// the comparison it exists to fail. Deriving it on demand makes that ordering
// impossible rather than something a test has to keep catching.
//
// Like EditableFingerprint it is detection input and never a concurrency
// guarantee: this store honours no conditional delete, so a match says the name
// had not changed as of the HEAD it came from, and nothing about the instant a
// DELETE arrives.
func (d *ObjectDetail) ContentFingerprint() string {
	h := sha256.New()
	// Length-prefixed so no combination of values can be spelled two ways.
	for _, field := range []string{string(d.Class), strconv.FormatInt(d.ContentLength, 10), etagValue(d.ETag)} {
		fmt.Fprintf(h, "%d:%s", len(field), field)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ObjectDetail HEADs one object and builds its inspector model.
func (s *Session) ObjectDetail(ctx context.Context, container, name string) (*ObjectDetail, error) {
	obj, headers, err := s.conn.Object(ctx, container, name)
	if err != nil {
		return nil, WithTransID(Normalize(err), headers)
	}
	d := buildObjectDetail(canonicalHeaders(headers), time.Now().UTC())
	d.Name = name
	d.ContentLength = obj.Bytes
	d.ContentType = obj.ContentType
	d.ETag = obj.Hash
	d.LastModified = obj.LastModified
	return d, nil
}

func buildObjectDetail(headers map[string]string, now time.Time) *ObjectDetail {
	d := &ObjectDetail{
		Class:      ClassPlain,
		Meta:       map[string]string{},
		RawHeaders: redactHeaders(headers, objectMetaPrefix),
		FetchedAt:  now,
	}

	editable := map[string]string{}
	for k, v := range headers {
		if name := metaSuffix(k, objectMetaPrefix); name != "" {
			d.Meta[name] = v
			editable[k] = v
			continue
		}
		switch k {
		case "X-Object-Manifest":
			d.Manifest = v
		case "Content-Location":
			d.FollowedSymlink = true
		case "X-Symlink-Target":
			d.SymlinkTarget = strings.TrimPrefix(v, "/")
		case "X-Symlink-Target-Account":
			d.SymlinkTargetAccount = v
		case "X-Delete-At":
			if epoch, err := strconv.ParseInt(v, 10, 64); err == nil {
				d.DeleteAt = time.Unix(epoch, 0).UTC()
			}
			editable[k] = v
		case "Content-Type":
			editable[k] = v
		case "Content-Encoding":
			d.ContentEncoding = v
		case "Content-Disposition":
			d.ContentDisposition = v
		}
	}
	// The class is decided after the walk, in a fixed order, so map order
	// never is. No store sends both manifest markers; if a reply somehow
	// does, SLO wins: the store stamps X-Static-Large-Object itself, and an
	// SLO route the store disagrees with is refused, whereas a DLO route
	// taken wrongly would sweep a segment prefix.
	switch {
	case isTrue(headers["X-Static-Large-Object"]):
		d.Class = ClassSLO
	case d.Manifest != "":
		d.Class = ClassDLO
	}
	d.TransID = transIDOfHeaders(headers)
	d.EditableFingerprint = editableFingerprint(editable)
	return d
}

// isTrue matches Swift's boolean header convention ("True"/"true"/"1").
func isTrue(v string) bool {
	switch v {
	case "True", "true", "TRUE", "1":
		return true
	}
	return false
}

// SymlinkGet HEADs the link OBJECT itself (?symlink=get) via the
// session's raw client: the returned detail carries X-Symlink-Target
// and the link's own metadata, never the followed target's.
func (s *Session) SymlinkGet(ctx context.Context, container, name string) (*ObjectDetail, error) {
	return s.literalHead(ctx, container, name, "symlink=get", "symlink HEAD")
}

// LiteralObjectHead HEADs exactly what is stored at a name: a symlink is
// not followed and a large-object manifest is not expanded, because both
// controls ride the request (?symlink=get&multipart-manifest=get). A read
// that follows or expands reports a dangling link, or a manifest whose
// segments are missing, as not found while an object still stands at the
// name, which is why a deletion verifies with this read (ADR 0092).
func (s *Session) LiteralObjectHead(ctx context.Context, container, name string) (*ObjectDetail, error) {
	return s.literalHead(ctx, container, name, "symlink=get&multipart-manifest=get", "literal HEAD")
}

// literalHead is a read-only raw HEAD that asks once more with fresh
// credentials after a refused token, which is harmless for a read. A path whose requests must
// share one token and one storage URL takes credentials itself and calls
// rawHead directly.
func (s *Session) literalHead(ctx context.Context, container, name, rawQuery, what string) (*ObjectDetail, error) {
	for attempt := 0; ; attempt++ {
		credentials, err := s.Credentials(ctx, 0)
		if err != nil {
			return nil, Normalize(err)
		}
		d, _, status, err := s.rawHead(ctx, credentials, container, name, rawQuery, what)
		if status == http.StatusUnauthorized && attempt == 0 {
			// rawHead has already invalidated the refused token; this only
			// decides to ask once more with fresh credentials.
			continue
		}
		return d, err
	}
}

// rawHead HEADs a name through the raw client under the credentials given,
// with rawQuery deciding what the store follows or expands. It returns the
// response headers beside the detail so a caller can judge whether the
// identity it carries is complete, and the status so a caller can decide
// what a refused token means on its own path. A 404 and any other
// non-success status are returned as a SwiftError carrying that status.
func (s *Session) rawHead(ctx context.Context, credentials StorageCredentials, container, name, rawQuery, what string) (*ObjectDetail, http.Header, int, error) {
	if container == "" || name == "" {
		// An empty object name addresses the container; an object read must
		// never be answered by a container's headers.
		return nil, nil, 0, &SwiftError{Kind: KindUnknown, Message: what + ": the container and object name must both be present"}
	}
	target := credentials.StorageURL + "/" + escapeObjectPath(container) + "/" + escapeObjectPath(name)
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return nil, nil, 0, Normalize(err)
	}
	req.Header.Set("X-Auth-Token", credentials.Token)
	resp, err := s.raw.Do(req)
	if err != nil {
		return nil, nil, 0, Normalize(err)
	}
	defer resp.Body.Close()
	transID := transIDOf(resp.Header)
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, resp.Header, resp.StatusCode, &SwiftError{Kind: KindNotFound, HTTPStatus: resp.StatusCode, Message: what + ": not found", TransID: transID}
	case resp.StatusCode == http.StatusUnauthorized:
		// The store refused this token, so it is invalidated here, whatever the
		// caller decides next: Credentials must never hand a rejected token to
		// anyone, the engine remote included. Retrying is the caller's choice.
		s.ForceReauth()
		return nil, resp.Header, resp.StatusCode, &SwiftError{Kind: KindUnauthorized, HTTPStatus: resp.StatusCode, Message: what + ": the token was refused", TransID: transID}
	case resp.StatusCode >= 300:
		kind, retryable := classifyStatus(resp.StatusCode)
		return nil, resp.Header, resp.StatusCode, &SwiftError{Kind: kind, HTTPStatus: resp.StatusCode, Message: what + " failed", TransID: transID, Retryable: retryable}
	}
	flat := map[string]string{}
	for k, values := range resp.Header {
		if len(values) > 0 {
			flat[k] = values[0]
		}
	}
	d := buildObjectDetail(flat, time.Now().UTC())
	d.Name = name
	// ObjectDetail takes the length, entity tag and content type from the
	// wire library's object record; this read has only its own headers, so
	// they are taken from there, or an observation could never be compared
	// with the inspector's (ADR 0092 decision 3). Whether they were present
	// is the caller's question, answered from the headers returned beside.
	if n, err := strconv.ParseInt(flat["Content-Length"], 10, 64); err == nil {
		d.ContentLength = n
	}
	d.ETag = flat["Etag"]
	d.ContentType = flat["Content-Type"]
	if modified, err := http.ParseTime(flat["Last-Modified"]); err == nil {
		d.LastModified = modified
	}
	return d, resp.Header, resp.StatusCode, nil
}

// DynamicSegments is the segment container and prefix a dynamic manifest
// names. The X-Object-Manifest value is percent-encoded, as the engine and
// other clients write it, and Swift's dynamic large object middleware
// decodes it before it lists the prefix, so a reader that lists, names or
// compares the segments has to decode it the same way (F37). An escape
// that is not valid is kept as written, as Swift's decoding keeps it. ok is
// false for a value that names no container.
func DynamicSegments(manifest string) (container, prefix string, ok bool) {
	decoded := unquoteManifest(manifest)
	container, prefix, ok = strings.Cut(decoded, "/")
	return container, prefix, ok && container != ""
}

// unquoteManifest decodes each valid %XX escape to its byte and leaves
// everything else, an invalid escape and a plus sign among them, as it is.
func unquoteManifest(value string) string {
	if !strings.Contains(value, "%") {
		return value
	}
	out := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		if value[i] == '%' && i+2 < len(value) && isHex(value[i+1]) && isHex(value[i+2]) {
			out = append(out, unhex(value[i+1])<<4|unhex(value[i+2]))
			i += 2
			continue
		}
		out = append(out, value[i])
	}
	return string(out)
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c <= 'F':
		return c - 'A' + 10
	default:
		return c - 'a' + 10
	}
}
