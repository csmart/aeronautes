package copy

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
)

// runDigest parses an entity tag as a complete MD5. Swift quotes entity
// tags on some responses and not others, and reports them in either case,
// so trim and fold before deciding whether the value is a complete MD5.
func runDigest(etag string) ([md5.Size]byte, bool) {
	trimmed := strings.Trim(etag, `"`)
	if len(trimmed) != md5.Size*2 {
		return [md5.Size]byte{}, false
	}
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return [md5.Size]byte{}, false
	}
	return [md5.Size]byte(raw), true
}
