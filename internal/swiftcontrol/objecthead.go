package swiftcontrol

import (
	"context"
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
	target := s.conn.StorageUrl + "/" + escapeObjectPath(container) + "/" + escapeObjectPath(name) + "?symlink=get"
	send := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", s.conn.AuthToken)
		return s.raw.Do(req)
	}
	resp, err := send()
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if authErr := s.Authenticate(ctx); authErr != nil {
			return nil, authErr
		}
		resp, err = send()
	}
	if err != nil {
		return nil, Normalize(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, &SwiftError{Kind: KindNotFound, HTTPStatus: resp.StatusCode, Message: "symlink HEAD: not found"}
	}
	if resp.StatusCode >= 300 {
		return nil, &SwiftError{Kind: KindServer, HTTPStatus: resp.StatusCode, Message: "symlink HEAD failed"}
	}
	flat := map[string]string{}
	for k, values := range resp.Header {
		if len(values) > 0 {
			flat[k] = values[0]
		}
	}
	d := buildObjectDetail(flat, time.Now().UTC())
	d.Name = name
	return d, nil
}
