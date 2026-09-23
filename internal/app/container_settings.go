package app

import (
	"strings"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// ContainerSettingsDTO is the set of access and feature facts one
// container HEAD reported, derived once here and shown wherever the
// container appears: as row badges, as the banner over a world-readable
// open container and beside the inspector's read ACL (ADR 0071). A fact is
// a positive observation made under one listing scope; a row without one
// is unchecked, never private.
type ContainerSettingsDTO struct {
	// PublicRead is the acknowledgement gate's own predicate: an exact
	// `.r:*` grant in any comma position of the read ACL.
	PublicRead bool `json:"publicRead"`
	// PublicListing is `.rlistings` beside a world-read grant: object names
	// are listable by anyone, not only readable by URL.
	PublicListing bool `json:"publicListing"`
	// ReferrerGrants are the `.r:` entries that grant to a referrer host
	// rather than to the world; a `.r:-` entry is a denial and grants
	// nothing, so it is never listed here.
	ReferrerGrants []string `json:"referrerGrants"`
	// SharedWrite is a non-empty write ACL.
	SharedWrite       bool   `json:"sharedWrite"`
	StaticWeb         bool   `json:"staticWeb"`
	VersioningMode    string `json:"versioningMode"`
	VersioningArchive string `json:"versioningArchive"`
	Quota             bool   `json:"quota"`
	CORS              bool   `json:"cors"`
	// ReadACL and WriteACL are the exact header values, for the tooltip.
	ReadACL  string `json:"readAcl"`
	WriteACL string `json:"writeAcl"`
	// ObservedAt is when the HEAD that produced these facts was answered
	// (RFC 3339); Generation is the listing scope it was answered under.
	ObservedAt string `json:"observedAt"`
	Generation int64  `json:"generation"`
}

// containerSettings is the one derivation of settings facts from a
// container HEAD. Nothing else parses an ACL for display.
func containerSettings(d *swiftcontrol.ContainerDetail, generation int64, observed time.Time) ContainerSettingsDTO {
	s := ContainerSettingsDTO{
		PublicRead:        grantsPublicRead(d.ReadACL),
		ReferrerGrants:    referrerGrants(d.ReadACL),
		SharedWrite:       strings.TrimSpace(d.WriteACL) != "",
		StaticWeb:         d.StaticWeb,
		VersioningMode:    string(d.Versioning.Mode),
		VersioningArchive: d.Versioning.Archive,
		Quota:             d.QuotaBytes > 0 || d.QuotaCount > 0,
		CORS:              d.CORS,
		ReadACL:           d.ReadACL,
		WriteACL:          d.WriteACL,
		ObservedAt:        observed.UTC().Format(time.RFC3339),
		Generation:        generation,
	}
	s.PublicListing = s.PublicRead && aclContains(d.ReadACL, ".rlistings")
	if s.ReferrerGrants == nil {
		s.ReferrerGrants = []string{}
	}
	return s
}

func aclContains(acl, entry string) bool {
	for _, part := range strings.Split(acl, ",") {
		if strings.TrimSpace(part) == entry {
			return true
		}
	}
	return false
}

// referrerGrants lists the referrer hosts a read ACL grants to: every
// `.r:` entry naming a host or a `*.` wildcard domain, in ACL order. The
// world grant is not a referrer, a `.r:-` entry is a denial, and a near
// miss of the world grant such as `.r:*x` or a bare `.r:` is nothing at
// all (ADR 0071 decision 1).
func referrerGrants(acl string) []string {
	var grants []string
	for _, part := range strings.Split(acl, ",") {
		entry := strings.TrimSpace(part)
		if !strings.HasPrefix(entry, ".r:") || strings.HasPrefix(entry, ".r:-") {
			continue
		}
		host := entry[len(".r:"):]
		if host == "" || host == "*" || (strings.HasPrefix(host, "*") && !strings.HasPrefix(host, "*.")) {
			continue
		}
		grants = append(grants, host)
	}
	return grants
}

// settingsFacts retains the facts observed under the current connection
// generation of each bookmark. A fact learned under one connection is
// never shown under another: remembering a fact for a newer generation
// discards the bookmark's older ones, and a read for any other generation
// finds nothing (ADR 0066 scope, ADR 0071 decision 3).
type settingsFacts struct {
	mu         sync.Mutex
	generation map[string]int64
	facts      map[string]map[string]ContainerSettingsDTO
}

func (f *settingsFacts) remember(bookmarkID string, generation int64, name string, s ContainerSettingsDTO) {
	if bookmarkID == "" || generation <= 0 || name == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.generation == nil {
		f.generation = map[string]int64{}
		f.facts = map[string]map[string]ContainerSettingsDTO{}
	}
	if f.generation[bookmarkID] != generation {
		f.generation[bookmarkID] = generation
		f.facts[bookmarkID] = map[string]ContainerSettingsDTO{}
	}
	f.facts[bookmarkID][name] = s
}

// known returns the facts held for the names under exactly this
// bookmark and generation; names without one are absent from the result.
func (f *settingsFacts) known(bookmarkID string, generation int64, names []string) map[string]ContainerSettingsDTO {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]ContainerSettingsDTO{}
	if f.generation == nil || f.generation[bookmarkID] != generation {
		return out
	}
	for _, name := range names {
		if s, ok := f.facts[bookmarkID][name]; ok {
			out[name] = s
		}
	}
	return out
}
