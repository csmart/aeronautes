package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// The derivation is the only place an ACL is read for display, so its
// cases are the badge contract: world read is the acknowledgement gate's
// predicate, listing needs world read, a `.r:-` denial is never a grant,
// and near misses of `.r:*` are nothing (ADR 0071 decision 1).
func TestContainerSettingsDerivation(t *testing.T) {
	observed := time.Date(2026, 9, 23, 7, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		detail      swiftcontrol.ContainerDetail
		publicRead  bool
		listing     bool
		referrers   []string
		sharedWrite bool
		staticWeb   bool
		versioning  string
		quota       bool
		cors        bool
	}{
		{name: "empty", detail: swiftcontrol.ContainerDetail{}, referrers: []string{}},
		{name: "world read", detail: swiftcontrol.ContainerDetail{ReadACL: ".r:*"}, publicRead: true, referrers: []string{}},
		{name: "world read and listing", detail: swiftcontrol.ContainerDetail{ReadACL: ".r:*,.rlistings"}, publicRead: true, listing: true, referrers: []string{}},
		{name: "spaces", detail: swiftcontrol.ContainerDetail{ReadACL: " .r:* , .rlistings "}, publicRead: true, listing: true, referrers: []string{}},
		{name: "listing without world read", detail: swiftcontrol.ContainerDetail{ReadACL: ".rlistings"}, referrers: []string{}},
		{name: "referrer host", detail: swiftcontrol.ContainerDetail{ReadACL: ".r:.example.com"}, referrers: []string{".example.com"}},
		{name: "referrer wildcard host", detail: swiftcontrol.ContainerDetail{ReadACL: ".r:*.example.com"}, referrers: []string{"*.example.com"}},
		{name: "denial only", detail: swiftcontrol.ContainerDetail{ReadACL: ".r:-.example.com"}, referrers: []string{}},
		{name: "denial beside world read", detail: swiftcontrol.ContainerDetail{ReadACL: ".r:-.example.com,.r:*"}, publicRead: true, referrers: []string{}},
		{name: "near miss", detail: swiftcontrol.ContainerDetail{ReadACL: ".r:*x"}, referrers: []string{}},
		{name: "near miss beside a grant", detail: swiftcontrol.ContainerDetail{ReadACL: ".r:*x,.r:.example.com"}, referrers: []string{".example.com"}},
		{name: "bare prefix", detail: swiftcontrol.ContainerDetail{ReadACL: ".r:"}, referrers: []string{}},
		{name: "account principal", detail: swiftcontrol.ContainerDetail{ReadACL: "account:user"}, referrers: []string{}},
		{name: "shared write", detail: swiftcontrol.ContainerDetail{WriteACL: "account:user"}, sharedWrite: true, referrers: []string{}},
		{name: "blank write", detail: swiftcontrol.ContainerDetail{WriteACL: "  "}, referrers: []string{}},
		{name: "static web", detail: swiftcontrol.ContainerDetail{StaticWeb: true}, staticWeb: true, referrers: []string{}},
		{name: "versioning", detail: swiftcontrol.ContainerDetail{Versioning: swiftcontrol.ContainerVersioning{Mode: swiftcontrol.VersioningMode("versions_location"), Archive: "old"}}, versioning: "versions_location", referrers: []string{}},
		{name: "byte quota", detail: swiftcontrol.ContainerDetail{QuotaBytes: 1}, quota: true, referrers: []string{}},
		{name: "object quota", detail: swiftcontrol.ContainerDetail{QuotaCount: 1}, quota: true, referrers: []string{}},
		{name: "cors", detail: swiftcontrol.ContainerDetail{CORS: true}, cors: true, referrers: []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := containerSettings(&c.detail, 7, observed)
			if got.PublicRead != c.publicRead || got.PublicListing != c.listing || !reflect.DeepEqual(got.ReferrerGrants, c.referrers) ||
				got.SharedWrite != c.sharedWrite || got.StaticWeb != c.staticWeb || got.VersioningMode != c.versioning ||
				got.Quota != c.quota || got.CORS != c.cors {
				t.Fatalf("derived %+v", got)
			}
			if got.ReadACL != c.detail.ReadACL || got.WriteACL != c.detail.WriteACL || got.Generation != 7 || got.ObservedAt != "2026-09-23T07:00:00Z" {
				t.Fatalf("derived facts lost their header value, scope or time: %+v", got)
			}
			// The badge predicate and the acknowledgement gate must agree.
			if got.PublicRead != grantsPublicRead(c.detail.ReadACL) {
				t.Fatalf("badge and acknowledgement disagree on %q", c.detail.ReadACL)
			}
		})
	}
}

// A fact is held only under the generation it was observed in: a newer
// generation for the bookmark discards the older facts, a read for any
// other generation finds nothing, and bookmarks never share facts
// (ADR 0071 decision 3).
func TestContainerSettingsFactsFollowTheGeneration(t *testing.T) {
	var facts settingsFacts
	public := ContainerSettingsDTO{PublicRead: true, Generation: 1}
	facts.remember("a", 1, "one", public)
	facts.remember("a", 1, "two", ContainerSettingsDTO{Generation: 1})
	if got := facts.known("a", 1, []string{"one", "two", "three"}); len(got) != 2 || !got["one"].PublicRead {
		t.Fatalf("facts under their own generation: %+v", got)
	}
	if got := facts.known("a", 2, []string{"one"}); len(got) != 0 {
		t.Fatalf("a later generation saw an older fact: %+v", got)
	}
	if got := facts.known("b", 1, []string{"one"}); len(got) != 0 {
		t.Fatalf("another bookmark saw the fact: %+v", got)
	}
	facts.remember("a", 2, "two", ContainerSettingsDTO{Generation: 2})
	if got := facts.known("a", 2, []string{"one", "two"}); len(got) != 1 || got["two"].Generation != 2 {
		t.Fatalf("remembering under a newer generation did not discard the older facts: %+v", got)
	}
	if got := facts.known("a", 1, []string{"one"}); len(got) != 0 {
		t.Fatalf("the older generation is still answered: %+v", got)
	}
	// Nothing is retained without a scope to hold it under.
	facts.remember("", 2, "two", public)
	facts.remember("a", 0, "two", public)
	facts.remember("a", 2, "", public)
	if got := facts.known("a", 2, []string{"two", ""}); len(got) != 1 || got["two"].PublicRead {
		t.Fatalf("an unscoped fact was retained: %+v", got)
	}
}
