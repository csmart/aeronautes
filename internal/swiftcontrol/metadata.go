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
