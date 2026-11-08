package swiftcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/textproto"
	"sort"
	"strings"
)

// Header categorisation (§13.1, §53): Swift responses mix derived state,
// editable system settings, user metadata, and secret-bearing keys in one
// header set. This file is the single place that takes them apart.
//
// TempURL keys are the trap: they live under the ordinary *-Meta-* prefix
// (X-Account-Meta-Temp-URL-Key and friends) but are secrets (§22,
// K-SEC-006) — so categorisation reports their presence as slots and
// withholds the values, including from the raw-header view. Revealing a
// value is a deliberate future action, not a default.

const redactedValue = "[redacted]"

// canonicalHeaders re-keys a header map into canonical MIME form so lookups
// and prefix checks are case-insensitive regardless of what the server or
// a middlebox sent.
func canonicalHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[textproto.CanonicalMIMEHeaderKey(k)] = v
	}
	return out
}

// metaSuffix returns the metadata name after prefix (canonical form), or
// "" when key is not under it.
func metaSuffix(key, prefix string) string {
	if strings.HasPrefix(key, prefix) && len(key) > len(prefix) {
		return key[len(prefix):]
	}
	return ""
}

// isTempURLKey reports whether a metadata name is a TempURL key slot
// (Temp-URL-Key, Temp-URL-Key-2).
func isTempURLKey(metaName string) bool {
	return strings.HasPrefix(strings.ToLower(metaName), "temp-url-key")
}

// redactHeaders returns a copy of headers safe to display and log: every
// secret-bearing key's value replaced with a placeholder. metaPrefix is
// the resource's *-Meta- prefix.
func redactHeaders(headers map[string]string, metaPrefix string) map[string]string {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if name := metaSuffix(k, metaPrefix); name != "" && isTempURLKey(name) {
			out[k] = redactedValue
			continue
		}
		out[k] = v
	}
	return out
}

// editableFingerprint hashes an editable header set, order-independently,
// for §18 best-effort stale-write detection. It is conflict *detection*
// input, never a concurrency guarantee (K-META-003).
func editableFingerprint(editable map[string]string) string {
	// Normalise key case before sorting: header names are
	// case-insensitive, and the sort order must not depend on casing.
	lowered := make(map[string]string, len(editable))
	for k, v := range editable {
		lowered[strings.ToLower(k)] = v
	}
	keys := make([]string, 0, len(lowered))
	for k := range lowered {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(lowered[k]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// transIDOf is a raw response's transaction id as Swift reports it
// (X-Trans-Id) or as Ceph RGW does (X-Openstack-Request-Id).
func transIDOf(h http.Header) string {
	if id := h.Get("X-Trans-Id"); id != "" {
		return id
	}
	return h.Get("X-Openstack-Request-Id")
}
