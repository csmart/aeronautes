// Package bookmarkimport reads the Swift accounts another tool's
// configuration describes, rclone.conf or an OpenStack clouds.yaml, as
// bookmarks to import (ADR 0114). It only reads: saving, verifying and
// the secret store belong to the caller.
//
// Nothing here quotes the file. A skip reason is a fixed phrase and every
// error is one of the sentinels below, because a configuration file holds
// passwords and a parser's message can echo the line it failed on.
package bookmarkimport

import (
	"bytes"
	"errors"
	"strings"

	"github.com/csmart/aeronautes/internal/bookmarks"
)

// MaxFileSize bounds what the caller reads before handing a file over.
const MaxFileSize = 1 << 20

var (
	ErrTooLarge  = errors.New("the file is larger than 1 MiB, more than any configuration Aeronautes imports")
	ErrEncrypted = errors.New("this rclone config is encrypted, and Aeronautes does not decrypt it")
	ErrNotConfig = errors.New("this is not an rclone config or a clouds.yaml Aeronautes can import")
)

// Source is one account the file describes. Bookmark holds what the save
// will store, with the source's name as its display name; Secret is the
// key or password, empty when the file has none; Skip, when not empty, is
// why the account is not offered for import.
type Source struct {
	Name     string
	Bookmark bookmarks.Bookmark
	Secret   string
	Skip     string
}

// Fixed skip reasons (ADR 0114 decision 5). None quotes the file.
const (
	SkipEnvironment     = "takes its credentials from the environment"
	SkipKeystoneV2      = "uses Keystone v2, which Aeronautes does not offer"
	SkipMethod          = "signs in with an application credential, a token or another method Aeronautes does not offer"
	SkipInterface       = "uses an endpoint interface other than public"
	SkipDomainID        = "names a domain by ID; give the domain's name instead"
	SkipUserDomain      = "names its user without a domain, which Keystone refuses"
	SkipProjectDomain   = "names its project without a domain, which Keystone refuses"
	SkipNoRegion        = "has no region, which a Keystone v3 bookmark needs"
	SkipV3StorageURL    = "sets a storage URL, which a Keystone v3 bookmark does not use"
	SkipUnknownVersion  = "states no auth version and its auth URL does not show one"
	SkipTrust           = "sets its own TLS trust; add this bookmark by hand to choose its trust"
	SkipNoAuthURL       = "has no auth URL"
	SkipNoUser          = "has no user"
	SkipNoProject       = "has no project, which a Keystone v3 bookmark needs"
	SkipUnsupportedAuth = "states an auth version Aeronautes does not offer"
)

// Read returns the accounts file describes. secure is the clouds.yaml
// secure.yaml from the same folder, or nil; an rclone config ignores it.
func Read(file, secure []byte) ([]Source, error) {
	if len(file) > MaxFileSize || len(secure) > MaxFileSize {
		return nil, ErrTooLarge
	}
	if clouds, ok := parseClouds(file); ok {
		return readClouds(clouds, secure)
	}
	first := firstContentLine(file)
	if first == "RCLONE_ENCRYPT_V0:" {
		return nil, ErrEncrypted
	}
	if strings.HasPrefix(first, "[") && strings.HasSuffix(first, "]") {
		return readRclone(file), nil
	}
	return nil, ErrNotConfig
}

// firstContentLine is the first line that is neither blank nor a comment,
// trimmed.
func firstContentLine(file []byte) string {
	for _, raw := range bytes.Split(file, []byte("\n")) {
		line := strings.TrimSpace(string(raw))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		return line
	}
	return ""
}
