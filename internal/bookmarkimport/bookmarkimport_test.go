package bookmarkimport

import (
	"errors"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/bookmarks"
)

// fixtureSecret is in every fixture's key or password, so a test can show
// no reason or error ever carries it.
const fixtureSecret = "s3cret-fixture"

func readOne(t *testing.T, file, secure string) map[string]Source {
	t.Helper()
	var secureBytes []byte
	if secure != "" {
		secureBytes = []byte(secure)
	}
	sources, err := Read([]byte(file), secureBytes)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	byName := map[string]Source{}
	for _, s := range sources {
		if strings.Contains(s.Skip, fixtureSecret) {
			t.Errorf("%s: skip reason carries the secret", s.Name)
		}
		byName[s.Name] = s
	}
	return byName
}

func TestRcloneRemotesBecomeBookmarks(t *testing.T) {
	file := `
# a comment
[tempauth]
type = swift
user = acct:user
key = ` + fixtureSecret + `
auth = https://swift.example/auth/v1.0
storage_url = https://swift.example/v1/AUTH_acct

[keystone]
type = swift
user = keystoneuser
key = ` + fixtureSecret + `
auth = https://identity.example/v3
domain = users
tenant = demo
region = RegionOne

[both]
type = swift
user = byname
user_id = u-1
domain = users
tenant = demo
tenant_id = p-1
auth = https://identity.example/v3
region = RegionOne
key = ` + fixtureSecret + `

[ids]
type = swift
user_id = u-2
tenant = demo
auth_version = 3
auth = https://identity.example
region = RegionOne

[s3]
type = s3
`
	got := readOne(t, file, "")
	if _, ok := got["s3"]; ok {
		t.Error("a non-swift remote was offered")
	}
	cases := map[string]bookmarks.Bookmark{
		"tempauth": {DisplayName: "tempauth", AuthVersion: 1, AuthURL: "https://swift.example/auth/v1.0", Username: "acct:user", StorageURLOverride: "https://swift.example/v1/AUTH_acct"},
		"keystone": {DisplayName: "keystone", AuthVersion: 3, AuthURL: "https://identity.example/v3", Username: "keystoneuser", UserDomain: "users", ProjectName: "demo", ProjectDomain: "users", Region: "RegionOne"},
		// The library sends the user's name beside its ID, which Keystone
		// resolves by the name, and only the project's ID.
		"both": {DisplayName: "both", AuthVersion: 3, AuthURL: "https://identity.example/v3", Username: "byname", UserDomain: "users", ProjectID: "p-1", Region: "RegionOne"},
		// A user named by ID has no domain, so the project's falls back to
		// Default, as the library's does.
		"ids": {DisplayName: "ids", AuthVersion: 3, AuthURL: "https://identity.example", UserID: "u-2", ProjectName: "demo", ProjectDomain: "Default", Region: "RegionOne"},
	}
	for name, want := range cases {
		s := got[name]
		if s.Skip != "" || s.Bookmark != want {
			t.Errorf("%s = %+v (skip %q), want %+v", name, s.Bookmark, s.Skip, want)
		}
	}
	if got["keystone"].Secret != fixtureSecret || got["ids"].Secret != "" {
		t.Errorf("secrets = %q and %q, want the key and none", got["keystone"].Secret, got["ids"].Secret)
	}
}

func TestRcloneSkipsWhatItCannotReproduce(t *testing.T) {
	base := "type = swift\nkey = " + fixtureSecret + "\nuser = u\ndomain = d\ntenant = p\nregion = R\n"
	cases := []struct{ name, options, want string }{
		{"environment", "env_auth = true\nauth = https://i.example/v3\n", SkipEnvironment},
		{"token", "auth_token = abc\nauth = https://i.example/v3\n", SkipMethod},
		{"application credential", "application_credential_id = x\nauth = https://i.example/v3\n", SkipMethod},
		{"internal interface", "endpoint_type = internal\nauth = https://i.example/v3\n", SkipInterface},
		{"version 2 from the URL", "auth = https://i.example/v2.0\n", SkipKeystoneV2},
		{"no version anywhere", "auth = https://identity.example\n", SkipUnknownVersion},
		{"unknown stated version", "auth_version = 4\nauth = https://i.example\n", SkipUnsupportedAuth},
		{"v3 storage url", "auth = https://i.example/v3\nstorage_url = https://s.example/v1/AUTH_x\n", SkipV3StorageURL},
		{"no auth url", "", SkipNoAuthURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readOne(t, "[r]\n"+base+tc.options, "")
			if got["r"].Skip != tc.want {
				t.Errorf("skip = %q, want %q", got["r"].Skip, tc.want)
			}
		})
	}
	noDomain := readOne(t, "[r]\ntype = swift\nuser = u\nuser_id = u-1\ntenant = p\nregion = R\nauth = https://i.example/v3\n", "")
	if noDomain["r"].Skip != SkipUserDomain {
		t.Errorf("a user named by name without a domain = %q, want %q", noDomain["r"].Skip, SkipUserDomain)
	}
	noRegion := readOne(t, "[r]\ntype = swift\nuser = u\ndomain = d\ntenant = p\nauth = https://i.example/v3\n", "")
	if noRegion["r"].Skip != SkipNoRegion {
		t.Errorf("no region = %q, want %q", noRegion["r"].Skip, SkipNoRegion)
	}
}

func TestCloudsBecomeBookmarks(t *testing.T) {
	clouds := `
clouds:
  plain:
    auth:
      auth_url: https://identity.example:5000
      username: keystoneuser
      user_domain_name: users
      project_name: demo
      project_domain_name: projects
    region_name: RegionOne
    verify: true
  horizon:
    auth:
      auth_url: https://identity.example/v3
      username: keystoneuser
      user_id: u-1
      project_id: p-1
      project_name: demo
      project_domain_id: default
      user_domain_name: Default
    region_name: RegionOne
    interface: public
    identity_api_version: 3
  legacy:
    auth:
      auth-url: ignored-spelling
      auth_url: https://identity.example/v3
      username: keystoneuser
      tenant_name: demo
    domain_name: shared
    region_name: RegionOne
`
	secure := `
clouds:
  plain:
    auth:
      password: ` + fixtureSecret + `
  unnamed:
    auth:
      password: ` + fixtureSecret + `
`
	got := readOne(t, clouds, secure)
	if _, ok := got["unnamed"]; ok {
		t.Error("a cloud only secure.yaml names was offered")
	}
	cases := map[string]bookmarks.Bookmark{
		"plain": {DisplayName: "plain", AuthVersion: 3, AuthURL: "https://identity.example:5000", Username: "keystoneuser", UserDomain: "users", ProjectName: "demo", ProjectDomain: "projects", Region: "RegionOne"},
		// keystoneauth sends only the IDs, so the names and the domain ID
		// beside the project ID are not kept.
		"horizon": {DisplayName: "horizon", AuthVersion: 3, AuthURL: "https://identity.example/v3", UserID: "u-1", ProjectID: "p-1", Region: "RegionOne"},
		// tenant_name is project_name, and domain_name fills both domains.
		"legacy": {DisplayName: "legacy", AuthVersion: 3, AuthURL: "https://identity.example/v3", Username: "keystoneuser", UserDomain: "shared", ProjectName: "demo", ProjectDomain: "shared", Region: "RegionOne"},
	}
	for name, want := range cases {
		s := got[name]
		if s.Skip != "" || s.Bookmark != want {
			t.Errorf("%s = %+v (skip %q), want %+v", name, s.Bookmark, s.Skip, want)
		}
	}
	if got["plain"].Secret != fixtureSecret || got["horizon"].Secret != "" {
		t.Errorf("secrets = %q and %q, want secure.yaml's password and none", got["plain"].Secret, got["horizon"].Secret)
	}
}

func TestCloudsSkipWhatTheyCannotReproduce(t *testing.T) {
	cloud := func(extra, auth string) string {
		return "clouds:\n  c:\n    region_name: R\n" + extra + "    auth:\n      auth_url: https://i.example\n      password: " + fixtureSecret + "\n" + auth
	}
	byName := "      username: u\n      user_domain_name: d\n      project_name: p\n      project_domain_name: d\n"
	cases := []struct{ name, file, want string }{
		{"application credential", cloud("    auth_type: v3applicationcredential\n", byName), SkipMethod},
		{"token", cloud("    auth_type: token\n", byName), SkipMethod},
		{"Keystone v2 by version", cloud("    identity_api_version: 2\n", byName), SkipKeystoneV2},
		{"Keystone v2 by URL", "clouds:\n  c:\n    region_name: R\n    auth:\n      auth_url: https://i.example/v2.0\n" + byName, SkipKeystoneV2},
		{"internal interface", cloud("    interface: internal\n", byName), SkipInterface},
		{"verify false", cloud("    verify: false\n", byName), SkipTrust},
		{"a cacert", cloud("    cacert: /etc/ca.pem\n", byName), SkipTrust},
		{"a client key alone", cloud("    key: /etc/client.key\n", byName), SkipTrust},
		{"insecure", cloud("    insecure: true\n", byName), SkipTrust},
		{"user domain by ID", cloud("", "      username: u\n      user_domain_id: default\n      project_id: p-1\n"), SkipDomainID},
		// keystoneauth sends the ID over the name, so the pair is skipped too.
		{"user domain by name and ID", cloud("", "      username: u\n      user_domain_name: d\n      user_domain_id: default\n      project_id: p-1\n"), SkipDomainID},
		{"project domain by name and ID", cloud("", "      user_id: u-1\n      project_name: p\n      project_domain_name: d\n      project_domain_id: default\n"), SkipDomainID},
		// The loader copies both generic keys onto each side.
		{"generic domain name and ID", cloud("", "      username: u\n      domain_name: d\n      domain_id: default\n      project_id: p-1\n"), SkipDomainID},
		{"user without a domain", cloud("", "      username: u\n      project_id: p-1\n"), SkipUserDomain},
		{"project without a domain", cloud("", "      user_id: u-1\n      project_name: p\n"), SkipProjectDomain},
		{"no project", cloud("", "      user_id: u-1\n"), SkipNoProject},
		{"no region", "clouds:\n  c:\n    auth:\n      auth_url: https://i.example\n" + byName, SkipNoRegion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readOne(t, tc.file, "")
			if got["c"].Skip != tc.want {
				t.Errorf("skip = %q, want %q", got["c"].Skip, tc.want)
			}
		})
	}
}

func TestReadRefusesWhatIsNotAConfiguration(t *testing.T) {
	cases := []struct {
		name string
		file []byte
		want error
	}{
		{"encrypted after a comment", []byte("# Encrypted rclone configuration File\n\nRCLONE_ENCRYPT_V0:\nb64data" + fixtureSecret + "\n"), ErrEncrypted},
		{"neither kind", []byte("just some text " + fixtureSecret + "\n"), ErrNotConfig},
		{"broken YAML", []byte("clouds:\n  c: [unclosed " + fixtureSecret + "\n"), ErrNotConfig},
		{"too large", []byte(strings.Repeat("#", MaxFileSize+1)), ErrTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Read(tc.file, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Read = %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), fixtureSecret) {
				t.Error("the error carries the secret")
			}
		})
	}
	// A comment that names the marker does not make a plain file encrypted.
	got := readOne(t, "# RCLONE_ENCRYPT_V0:\n[r]\ntype = swift\nuser = u\nauth = https://s.example/auth/v1.0\n", "")
	if got["r"].Skip != "" || got["r"].Bookmark.AuthVersion != 1 {
		t.Errorf("plain file behind a marker comment = %+v", got["r"])
	}
	if _, err := Read([]byte("clouds:\n  c: {}\n"), []byte("not: [yaml")); !errors.Is(err, ErrSecureFile) {
		t.Errorf("a broken secure.yaml = %v, want ErrSecureFile", err)
	}
}
