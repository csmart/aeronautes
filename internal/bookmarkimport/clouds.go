package bookmarkimport

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

var ErrSecureFile = errors.New("the secure.yaml beside this clouds.yaml could not be read")

// parseClouds reports whether file is a YAML document with a top-level
// clouds map, and returns that map.
func parseClouds(file []byte) (map[string]any, bool) {
	var doc map[string]any
	if yaml.Unmarshal(file, &doc) != nil {
		return nil, false
	}
	clouds, ok := doc["clouds"].(map[string]any)
	return clouds, ok
}

// readClouds turns every cloud into a source, with secure's same-named
// cloud merged over it, as the OpenStack clients merge secure.yaml.
func readClouds(clouds map[string]any, secure []byte) ([]Source, error) {
	var secret map[string]any
	if secure != nil {
		var ok bool
		if secret, ok = parseClouds(secure); !ok {
			return nil, ErrSecureFile
		}
	}
	names := make([]string, 0, len(clouds))
	for name := range clouds {
		names = append(names, name)
	}
	slices.Sort(names)
	sources := make([]Source, 0, len(names))
	for _, name := range names {
		cloud, _ := clouds[name].(map[string]any)
		if over, ok := secret[name].(map[string]any); ok {
			cloud = merged(cloud, over)
		}
		source := cloudSource(cloud)
		source.Name = name
		source.Bookmark.DisplayName = name
		sources = append(sources, source)
	}
	return sources, nil
}

// cloudSource maps one cloud the way keystoneauth would connect with it
// (ADR 0114 decisions 3, 4 and 5). keystoneauth sends only the ID of a user
// or project given both, so the ID is what the bookmark keeps.
func cloudSource(cloud map[string]any) Source {
	var source Source
	auth := normalisedAuth(cloud)
	b := &source.Bookmark
	b.AuthVersion = 3
	b.AuthURL = text(auth["auth_url"])
	b.Region = text(cloud["region_name"])
	source.Secret = text(auth["password"])
	source.Skip = cloudSkip(cloud, auth, b.AuthURL)
	if source.Skip != "" {
		return source
	}
	switch {
	case text(auth["user_id"]) != "":
		b.UserID = text(auth["user_id"])
	case text(auth["username"]) != "":
		b.Username, b.UserDomain = text(auth["username"]), text(auth["user_domain_name"])
		if b.UserDomain == "" {
			source.Skip = domainSkip(auth["user_domain_id"], SkipUserDomain)
		}
	default:
		source.Skip = SkipNoUser
	}
	if source.Skip != "" {
		return source
	}
	switch {
	case text(auth["project_id"]) != "":
		b.ProjectID = text(auth["project_id"])
	case text(auth["project_name"]) != "":
		b.ProjectName, b.ProjectDomain = text(auth["project_name"]), text(auth["project_domain_name"])
		if b.ProjectDomain == "" {
			source.Skip = domainSkip(auth["project_domain_id"], SkipProjectDomain)
		}
	default:
		source.Skip = SkipNoProject
	}
	return source
}

// cloudSkip is why a cloud cannot be reproduced as a password-method v3
// bookmark under the system's trust, or "".
func cloudSkip(cloud, auth map[string]any, authURL string) string {
	authType := text(cloud["auth_type"])
	if authType == "" {
		authType = text(cloud["auth_plugin"])
	}
	trimmed := strings.TrimSuffix(authURL, "/")
	lastSegment := trimmed[strings.LastIndex(trimmed, "/")+1:]
	interfaceName := text(cloud["object_store_interface"])
	if interfaceName == "" {
		interfaceName = text(cloud["interface"])
	}
	switch {
	case authType != "" && authType != "password" && authType != "v3password":
		return SkipMethod
	case strings.HasPrefix(text(cloud["identity_api_version"]), "2") || strings.HasPrefix(lastSegment, "v2"):
		return SkipKeystoneV2
	case interfaceName != "" && interfaceName != "public":
		return SkipInterface
	case !systemTrust(cloud):
		return SkipTrust
	case authURL == "":
		return SkipNoAuthURL
	case text(cloud["region_name"]) == "":
		return SkipNoRegion
	}
	return ""
}

// systemTrust reports whether a cloud trusts the system's certificate
// authorities: verify absent or true, and no insecure, cacert or client
// certificate or key.
func systemTrust(cloud map[string]any) bool {
	if v, ok := cloud["verify"]; ok && !isTrue(text(v)) {
		return false
	}
	return !isTrue(text(cloud["insecure"])) && text(cloud["cacert"]) == "" &&
		text(cloud["cert"]) == "" && text(cloud["key"]) == ""
}

// domainSkip names a domain given only by ID, which a bookmark cannot
// carry, apart from a domain that is missing altogether.
func domainSkip(domainID any, missing string) string {
	if text(domainID) != "" {
		return SkipDomainID
	}
	return missing
}

// keyAliases are the spellings openstacksdk's loader accepts for each key,
// in its order, the last found winning (config/loader.py,
// _fix_backwards_project).
var keyAliases = []struct {
	key      string
	spelling []string
}{
	{"domain_id", []string{"domain_id", "domain-id"}},
	{"domain_name", []string{"domain_name", "domain-name"}},
	{"user_domain_id", []string{"user_domain_id", "user-domain-id"}},
	{"user_domain_name", []string{"user_domain_name", "user-domain-name"}},
	{"project_domain_id", []string{"project_domain_id", "project-domain-id"}},
	{"project_domain_name", []string{"project_domain_name", "project-domain-name"}},
	{"project_id", []string{"tenant_id", "tenant-id", "project_id", "project-id"}},
	{"project_name", []string{"tenant_name", "tenant-name", "project_name", "project-name"}},
}

// normalisedAuth is the cloud's auth section as openstacksdk's loader
// leaves it: each key under one spelling, read from auth or from the cloud
// itself, and, for a project-scoped cloud, domain_name and domain_id
// filling whichever user and project domain keys are absent
// (_handle_domain_id).
func normalisedAuth(cloud map[string]any) map[string]any {
	auth := map[string]any{}
	if given, ok := cloud["auth"].(map[string]any); ok {
		for k, v := range given {
			auth[k] = v
		}
	}
	for _, alias := range keyAliases {
		var found any
		for _, spelling := range alias.spelling {
			if v, ok := auth[spelling]; ok {
				found = v
				delete(auth, spelling)
			}
			if v, ok := cloud[spelling]; ok {
				found = v
			}
		}
		if text(found) != "" {
			auth[alias.key] = text(found)
		}
	}
	if text(auth["project_id"]) == "" && text(auth["project_name"]) == "" {
		return auth
	}
	for _, fill := range []struct{ from, user, project string }{
		{"domain_id", "user_domain_id", "project_domain_id"},
		{"domain_name", "user_domain_name", "project_domain_name"},
	} {
		if v, ok := auth[fill.from]; ok {
			for _, key := range []string{fill.user, fill.project} {
				if _, set := auth[key]; !set {
					auth[key] = v
				}
			}
			delete(auth, fill.from)
		}
	}
	return auth
}

// merged is base with over laid on top, maps merged key by key.
func merged(base, over map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if sub, ok := v.(map[string]any); ok {
			if inner, ok := out[k].(map[string]any); ok {
				out[k] = merged(inner, sub)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// text is a YAML scalar as a string; absent is "".
func text(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	return fmt.Sprint(v)
}
