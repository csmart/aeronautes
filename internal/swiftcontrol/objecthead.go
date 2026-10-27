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
	// Manifest is the DLO X-Object-Manifest value ("container/prefix"),
	// empty for anything else. SLO manifests are fetched separately when
	// the segment table is opened (?multipart-manifest=get, Phase 4).
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
		return nil, Normalize(err)
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
			d.Class = ClassDLO
			d.Manifest = v
		case "Content-Location":
			d.FollowedSymlink = true
		case "X-Symlink-Target":
			d.SymlinkTarget = strings.TrimPrefix(v, "/")
		case "X-Symlink-Target-Account":
			d.SymlinkTargetAccount = v
		case "X-Static-Large-Object":
			if isTrue(v) {
				d.Class = ClassSLO
			}
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
		case "X-Trans-Id":
			d.TransID = v
		case "X-Openstack-Request-Id":
			if d.TransID == "" {
				d.TransID = v
			}
		}
	}
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

func (s *Session) literalHead(ctx context.Context, container, name, rawQuery, what string) (*ObjectDetail, error) {
	send := func() (*http.Response, error) {
		credentials, err := s.Credentials(ctx, 0)
		if err != nil {
			return nil, err
		}
		target := credentials.StorageURL + "/" + escapeObjectPath(container) + "/" + escapeObjectPath(name) + "?" + rawQuery
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", credentials.Token)
		return s.raw.Do(req)
	}
	resp, err := send()
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		s.ForceReauth()
		resp, err = send()
	}
	if err != nil {
		return nil, Normalize(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		s.ForceReauth()
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, &SwiftError{Kind: KindNotFound, HTTPStatus: resp.StatusCode, Message: what + ": not found", TransID: resp.Header.Get("X-Trans-Id")}
	}
	if resp.StatusCode >= 300 {
		return nil, &SwiftError{Kind: KindServer, HTTPStatus: resp.StatusCode, Message: what + " failed", TransID: resp.Header.Get("X-Trans-Id")}
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
	// they are taken from there, or a literal observation could never be
	// compared with the inspector's (ADR 0092 decision 3).
	if n, err := strconv.ParseInt(flat["Content-Length"], 10, 64); err == nil {
		d.ContentLength = n
	}
	d.ETag = flat["Etag"]
	d.ContentType = flat["Content-Type"]
	if modified, err := http.ParseTime(flat["Last-Modified"]); err == nil {
		d.LastModified = modified
	}
	return d, nil
}
