package swiftcontrol

import (
	"net/url"
	"strings"
)

// AccountIdentity extracts the account segment from a storage URL path (the
// ".../v1/AUTH_xxx" tail), lowercased: the part that stays equal across host
// aliases and VIPs for one Swift account and differs between accounts. It is
// "" when it cannot be found, and callers treat an unknown identity as
// possibly any account, so their checks fail closed.
func AccountIdentity(storageURL string) string {
	return strings.ToLower(accountSegment(storageURL))
}

// accountSegment is the account component of a storage URL as the store
// spells it, or "" when there is none to read.
func accountSegment(storageURL string) string {
	u, err := url.Parse(storageURL)
	if err != nil {
		return ""
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, seg := range segments {
		if seg == "v1" && i+1 < len(segments) {
			return segments[i+1]
		}
	}
	if n := len(segments); n > 0 {
		return segments[n-1]
	}
	return ""
}

// SameAccount reports whether two storage URLs name one account exactly, for
// keeping a session on the account it resolved. Swift tells accounts apart by
// their exact bytes, so case counts here, unlike AccountIdentity, which may
// call two accounts one because it is used to refuse an overlap. An account
// that cannot be read is the same only as the identical storage URL.
func SameAccount(a, b string) bool {
	accountA, accountB := accountSegment(a), accountSegment(b)
	if accountA == "" || accountB == "" {
		return a == b
	}
	return accountA == accountB
}
