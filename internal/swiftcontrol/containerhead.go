package swiftcontrol

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
)

const containerMetaPrefix = "X-Container-Meta-"

// VersioningMode is the container-versioning *display* profile (§21).
// The behavioural model for the v1 target is still an owner decision;
// this only says what the headers say.
type VersioningMode string

const (
	VersioningOff VersioningMode = "off"
	// VersioningEnabled is modern object versioning
	// (X-Versions-Enabled: true) — versions live in the SAME container.
	VersioningEnabled          VersioningMode = "enabled"
	VersioningVersionsLocation VersioningMode = "versions_location"
	VersioningHistoryLocation  VersioningMode = "history_location"
)

// ContainerVersioning is the displayed versioning state.
type ContainerVersioning struct {
	Mode VersioningMode
	// Archive is the referenced versions/history container, "" when off.
	Archive string
}

// ContainerDetail is the container inspector's model (§53): counts,
// policy, ACLs, versioning display, quotas, feature presence, categorised
// metadata, and the redacted raw view.
type ContainerDetail struct {
	Name        string
	ObjectCount int64
	BytesUsed   int64
	// StoragePolicy is read-only after creation (§20).
	StoragePolicy string
	// ReadACL/WriteACL are the raw ACL strings, displayed verbatim (§19);
	// parsing/editing is a later phase.
	ReadACL    string
	WriteACL   string
	Versioning ContainerVersioning
	// QuotaBytes/QuotaCount are 0 when unset.
	QuotaBytes int64
	QuotaCount int64
	// CORS / StaticWeb report configured-feature presence (§60 Phase 2).
	CORS      bool
	StaticWeb bool
	// TempURLKeySlots lists which key slots exist; values are withheld
	// (§22, K-SEC-006).
	TempURLKeySlots []string
	// Meta is the user metadata minus special keys (quota, TempURL, CORS,
	// staticweb).
	Meta map[string]string
	// RawHeaders is the full header set with secret values redacted.
	RawHeaders map[string]string
	TransID    string
	// EditableFingerprint covers what a container editor may change (user
	// meta, quotas, ACLs, versioning headers) for §18 stale-write checks.
	EditableFingerprint string
	FetchedAt           time.Time
}

// ContainerDetail HEADs one container and builds its inspector model.
func (s *Session) ContainerDetail(ctx context.Context, name string) (*ContainerDetail, error) {
	info, headers, err := s.conn.Container(ctx, name)
	if err != nil {
		return nil, Normalize(err)
	}
	d := buildContainerDetail(canonicalHeaders(headers), time.Now().UTC())
	d.Name = name
	d.ObjectCount = info.Count
	d.BytesUsed = info.Bytes
	return d, nil
}

func buildContainerDetail(headers map[string]string, now time.Time) *ContainerDetail {
	d := &ContainerDetail{
		Versioning: ContainerVersioning{Mode: VersioningOff},
		Meta:       map[string]string{},
		RawHeaders: redactHeaders(headers, containerMetaPrefix),
		FetchedAt:  now,
	}

	editable := map[string]string{}
	for k, v := range headers {
		if name := metaSuffix(k, containerMetaPrefix); name != "" {
			switch {
			case isTempURLKey(name):
				d.TempURLKeySlots = append(d.TempURLKeySlots, name)
			case strings.EqualFold(name, "Quota-Bytes"):
				d.QuotaBytes, _ = strconv.ParseInt(v, 10, 64)
				editable[k] = v
			case strings.EqualFold(name, "Quota-Count"):
				d.QuotaCount, _ = strconv.ParseInt(v, 10, 64)
				editable[k] = v
			case hasFoldedPrefix(name, "Access-Control-"):
				// CORS configuration lives in container meta; presence is
				// what Phase 2 shows, the values stay in the raw view.
				d.CORS = true
				editable[k] = v
			case hasFoldedPrefix(name, "Web-"):
				d.StaticWeb = true
				editable[k] = v
			default:
				d.Meta[name] = v
				editable[k] = v
			}
			continue
		}
		switch k {
		case "X-Storage-Policy":
			d.StoragePolicy = v
		case "X-Container-Read":
			d.ReadACL = v
			editable[k] = v
		case "X-Container-Write":
			d.WriteACL = v
			editable[k] = v
		case "X-Versions-Location":
			// Swift rejects setting both headers; if a response ever carries
			// both anyway, prefer History deterministically (map order must
			// not decide).
			if d.Versioning.Mode == VersioningOff {
				d.Versioning = ContainerVersioning{Mode: VersioningVersionsLocation, Archive: v}
			}
			editable[k] = v
		case "X-History-Location":
			d.Versioning = ContainerVersioning{Mode: VersioningHistoryLocation, Archive: v}
			editable[k] = v
		case "X-Versions-Enabled":
			// Modern object versioning (§21 display-only): deletes and
			// overwrites here accumulate versions in-container.
			editable[k] = v
			if isTrue(v) {
				d.Versioning = ContainerVersioning{Mode: VersioningEnabled}
			}
		case "X-Trans-Id":
			d.TransID = v
		case "X-Openstack-Request-Id":
			if d.TransID == "" {
				d.TransID = v
			}
		}
	}
	sort.Strings(d.TempURLKeySlots)
	d.EditableFingerprint = editableFingerprint(editable)
	return d
}

func hasFoldedPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
