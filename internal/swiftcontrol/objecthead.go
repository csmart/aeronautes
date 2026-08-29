package swiftcontrol

import (
	"context"
	"strconv"
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
