package bookmarkimport

import (
	"bytes"
	"strings"

	"github.com/csmart/aeronautes/internal/bookmarks"
)

// section is one rclone remote: its name and its options in file order.
type section struct {
	name    string
	options map[string]string
}

// readRclone turns every remote of type swift into a source. rclone writes
// the key in plain text (it is a sensitive option, not a password one), so
// it is used as it stands and never decoded (ADR 0114).
func readRclone(file []byte) []Source {
	var sources []Source
	for _, s := range parseINI(file) {
		if s.options["type"] != "swift" {
			continue
		}
		source := rcloneSource(s.options)
		source.Name = s.name
		source.Bookmark.DisplayName = s.name
		sources = append(sources, source)
	}
	return sources
}

// rcloneSource maps one swift remote the way rclone's library would
// connect with it (ADR 0114 decisions 2, 4 and 5).
func rcloneSource(o map[string]string) Source {
	var source Source
	b := &source.Bookmark
	source.Secret = o["key"]
	b.AuthURL = o["auth"]
	switch {
	case isTrue(o["env_auth"]):
		source.Skip = SkipEnvironment
	case o["auth_token"] != "" || o["application_credential_id"] != "" ||
		o["application_credential_name"] != "" || o["application_credential_secret"] != "":
		source.Skip = SkipMethod
	case o["endpoint_type"] != "" && o["endpoint_type"] != "public":
		source.Skip = SkipInterface
	case b.AuthURL == "":
		source.Skip = SkipNoAuthURL
	}
	if source.Skip != "" {
		return source
	}
	version, skip := rcloneVersion(o["auth_version"], b.AuthURL)
	if skip != "" {
		source.Skip = skip
		return source
	}
	b.AuthVersion = version
	if version == 1 {
		b.Username = o["user"]
		b.StorageURLOverride = o["storage_url"]
		if b.Username == "" {
			source.Skip = SkipNoUser
		}
		return source
	}
	source.Skip = rcloneKeystone(o, b)
	return source
}

// rcloneVersion is auth_version when it is set, otherwise the library's own
// reading of the URL: the text v3, then v2, then v1, anywhere in it. A
// remote whose URL shows none could not authenticate in rclone either.
func rcloneVersion(stated, authURL string) (int, string) {
	if stated == "" || stated == "0" {
		switch {
		case strings.Contains(authURL, "v3"):
			stated = "3"
		case strings.Contains(authURL, "v2"):
			stated = "2"
		case strings.Contains(authURL, "v1"):
			stated = "1"
		default:
			return 0, SkipUnknownVersion
		}
	}
	switch stated {
	case "1":
		return 1, ""
	case "2":
		return 0, SkipKeystoneV2
	case "3":
		return 3, ""
	}
	return 0, SkipUnsupportedAuth
}

// rcloneKeystone fills a v3 bookmark and returns why it is skipped, if it
// is. The library sends a user's name, domain and ID together, which
// Keystone resolves by the name, but only a project's ID when it has one;
// the bookmark sends what Keystone would resolve.
func rcloneKeystone(o map[string]string, b *bookmarks.Bookmark) string {
	b.Region = o["region"]
	switch {
	case o["storage_url"] != "":
		return SkipV3StorageURL
	case b.Region == "":
		return SkipNoRegion
	}
	switch {
	case o["user"] != "":
		if o["domain"] == "" {
			return SkipUserDomain
		}
		b.Username, b.UserDomain = o["user"], o["domain"]
	case o["user_id"] != "":
		b.UserID = o["user_id"]
	default:
		return SkipNoUser
	}
	switch {
	case o["tenant_id"] != "":
		b.ProjectID = o["tenant_id"]
	case o["tenant"] != "":
		// The library's fallback when no project domain is named.
		b.ProjectName = o["tenant"]
		b.ProjectDomain = firstNonEmpty(o["tenant_domain"], o["domain"], "Default")
	default:
		return SkipNoProject
	}
	return ""
}

// parseINI reads rclone's config format: [name] headers, key = value
// lines, and lines starting with # or ; as comments.
func parseINI(file []byte) []section {
	var sections []section
	for _, raw := range bytes.Split(file, []byte("\n")) {
		line := strings.TrimSpace(string(raw))
		switch {
		case line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			sections = append(sections, section{name: strings.TrimSpace(line[1 : len(line)-1]), options: map[string]string{}})
		case len(sections) > 0:
			if key, value, ok := strings.Cut(line, "="); ok {
				sections[len(sections)-1].options[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
	}
	return sections
}

func isTrue(value string) bool {
	switch strings.ToLower(value) {
	case "true", "1", "yes":
		return true
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
