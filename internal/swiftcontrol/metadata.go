package swiftcontrol

import (
	"fmt"
	"sort"
	"strings"
)

// Header normalisation can change a complete working copy even when Swift
// accepts the write. Refuse inputs that cannot keep their literal identity.
func validateMetadata(meta map[string]string, allowEmptyValues bool) error {
	keys := make([]string, 0, len(meta))
	for key := range meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seen := make(map[string]string, len(meta))
	for _, key := range keys {
		if key == "" || !metadataToken(key) {
			return fmt.Errorf("metadata key %q must be a nonempty ASCII HTTP token", key)
		}
		if strings.Contains(key, "_") {
			return fmt.Errorf("metadata key %q contains an underscore, which Swift does not preserve literally", key)
		}
		folded := strings.ToLower(key)
		if original, exists := seen[folded]; exists {
			return fmt.Errorf("metadata keys %q and %q are duplicates; names are case-insensitive", original, key)
		}
		seen[folded] = key

		value := meta[key]
		if !allowEmptyValues && value == "" {
			return fmt.Errorf("metadata key %q has an empty container value; remove the key to delete it", key)
		}
		if strings.Trim(value, " \t") != value {
			return fmt.Errorf("metadata value for %q has leading or trailing ASCII spaces or tabs, which HTTP does not preserve", key)
		}
		for i := 0; i < len(value); i++ {
			if value[i] < ' ' && value[i] != '\t' || value[i] == 0x7f {
				return fmt.Errorf("metadata value for %q contains an invalid HTTP header control byte", key)
			}
		}
	}
	return nil
}

// refuseReservedMetadata refuses a working copy that names a key the typed
// surface owns. The portraits keep these keys out of Meta, so the additive
// delta would SET a caller-supplied value the operator never saw: a TempURL
// slot is a secret (§22, K-SEC-006) and a quota key belongs to its typed
// field. Refusing before any I/O, rather than filtering, is what makes "the
// editors cannot touch a slot" true of a working copy as well as of the
// detail (ADR 0101). Keys are checked in sorted order so the refusal names
// the same key every time.
func refuseReservedMetadata(meta map[string]string, quotaKeys ...string) error {
	keys := make([]string, 0, len(meta))
	for key := range meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if isTempURLKey(key) {
			return fmt.Errorf("metadata key %q is a TempURL secret slot and cannot be set through the metadata editor", key)
		}
		for _, quota := range quotaKeys {
			if strings.EqualFold(key, quota) {
				return fmt.Errorf("metadata key %q is a quota; set it through its own field rather than a metadata entry", key)
			}
		}
	}
	return nil
}

func metadataToken(key string) bool {
	for i := 0; i < len(key); i++ {
		b := key[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' {
			continue
		}
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b)) {
			return false
		}
	}
	return true
}
