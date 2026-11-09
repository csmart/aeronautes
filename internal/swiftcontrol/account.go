package swiftcontrol

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
)

const accountMetaPrefix = "X-Account-Meta-"

// PolicyUsage is one storage policy's account-level usage (§53).
type PolicyUsage struct {
	BytesUsed      int64
	ObjectCount    int64
	ContainerCount int64
}

// AccountPortrait is the account inspector's model (§53): counts and
// bytes from the account HEAD, categorised metadata, TempURL key slots
// (presence only — values are secrets, §22), per-policy usage, and the
// redacted raw header view.
type AccountPortrait struct {
	Containers int64
	Objects    int64
	BytesUsed  int64
	// QuotaBytes is the account quota, 0 when none is set.
	QuotaBytes int64
	// Meta is the user metadata, minus special keys (quota, TempURL).
	Meta map[string]string
	// TempURLKeySlots lists which key slots exist; values are withheld.
	TempURLKeySlots []string
	// PolicyUsage maps a storage policy name (lower-cased) to its usage.
	PolicyUsage map[string]PolicyUsage
	// RawHeaders is the full header set with secret values redacted.
	RawHeaders map[string]string
	// TransID is the Swift transaction id of the HEAD, for diagnostics.
	TransID string
	// EditableFingerprint covers the editable set (user meta + quota) for
	// §18 stale-write detection.
	EditableFingerprint string
	FetchedAt           time.Time
}

// AccountPortrait HEADs the account and builds its portrait.
func (s *Session) AccountPortrait(ctx context.Context) (*AccountPortrait, error) {
	p, _, err := s.accountHead(ctx)
	return p, err
}

// accountHead is AccountPortrait with the canonical headers the portrait
// was built from, for the one caller that must compare a value the
// portrait withholds (UpdateTempURLKey).
func (s *Session) accountHead(ctx context.Context) (*AccountPortrait, map[string]string, error) {
	info, headers, err := s.conn.Account(ctx)
	if err != nil {
		return nil, nil, WithTransID(Normalize(err), headers)
	}
	canonical := canonicalHeaders(headers)
	p := buildAccountPortrait(canonical, time.Now().UTC())
	p.Containers = info.Containers
	p.Objects = info.Objects
	p.BytesUsed = info.BytesUsed
	return p, canonical, nil
}

const policyHeaderPrefix = "X-Account-Storage-Policy-"

func buildAccountPortrait(headers map[string]string, now time.Time) *AccountPortrait {
	p := &AccountPortrait{
		Meta:        map[string]string{},
		PolicyUsage: map[string]PolicyUsage{},
		RawHeaders:  redactHeaders(headers, accountMetaPrefix),
		FetchedAt:   now,
	}

	editable := map[string]string{}
	for k, v := range headers {
		if name := metaSuffix(k, accountMetaPrefix); name != "" {
			switch {
			case isTempURLKey(name):
				p.TempURLKeySlots = append(p.TempURLKeySlots, name)
			case strings.EqualFold(name, "Quota-Bytes"):
				p.QuotaBytes, _ = strconv.ParseInt(v, 10, 64)
				editable[k] = v
			default:
				p.Meta[name] = v
				editable[k] = v
			}
			continue
		}
		if rest := metaSuffix(k, policyHeaderPrefix); rest != "" {
			addPolicyUsage(p.PolicyUsage, rest, v)
			continue
		}
	}
	p.TransID = transIDOfHeaders(headers)
	sort.Strings(p.TempURLKeySlots)
	p.EditableFingerprint = editableFingerprint(editable)
	return p
}

// addPolicyUsage parses "<Policy-Name>-Bytes-Used" style header rests.
// Policy names may themselves contain hyphens, so match on the known
// suffixes rather than splitting.
func addPolicyUsage(usage map[string]PolicyUsage, rest, value string) {
	n, _ := strconv.ParseInt(value, 10, 64)
	for _, suffix := range []string{"-Bytes-Used", "-Object-Count", "-Container-Count"} {
		name, found := strings.CutSuffix(rest, suffix)
		if !found || name == "" {
			continue
		}
		key := strings.ToLower(name)
		u := usage[key]
		switch suffix {
		case "-Bytes-Used":
			u.BytesUsed = n
		case "-Object-Count":
			u.ObjectCount = n
		case "-Container-Count":
			u.ContainerCount = n
		}
		usage[key] = u
		return
	}
}
