// Package realcloud reads the real-cloud test accounts from a clouds.yaml and
// keeps what tests print about them free of anything that names a provider,
// an account or a secret. Only tests import it: the Keystone v3 and
// versioning oracles in internal/swiftcontrol and the journeys
// (docs/journeys.md). It moved here unchanged from the oracle's own helpers
// so both suites share one set of selection and redaction rules.
package realcloud

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// Cloud is one validated clouds.yaml entry. The password is a field of its
// own and is never placed in, derived into, or logged from another field.
type Cloud struct {
	AuthURL       string
	Username      string
	Password      string
	ProjectName   string
	UserDomain    string
	ProjectDomain string
	Region        string
}

var (
	ErrCloudAbsent = errors.New("cloud entry not present in clouds.yaml")
	ErrNoPassword  = errors.New("cloud entry has no password")
	// ErrMalformedClouds deliberately carries none of the decoder's text: a
	// YAML type error echoes the offending scalar, which in this file can be
	// a password.
	ErrMalformedClouds = errors.New("clouds.yaml does not decode into the expected shape (clouds.<name>.auth with string fields)")
)

// ResolveCloudsPath picks the clouds.yaml to read. An explicitly selected path
// (ORACLE_CLOUDS, then OS_CLIENT_CONFIG_FILE) that cannot be used is an error,
// never a reason to read a different file: a typo in a disposable-cloud path
// must not direct real writes and deletes at another account whose entries
// happen to share the same names. The OpenStack-standard locations are tried
// only when no path was selected; "" means nothing is configured, so skip.
func ResolveCloudsPath() (string, error) {
	for _, key := range []string{"ORACLE_CLOUDS", "OS_CLIENT_CONFIG_FILE"} {
		if p := os.Getenv(key); p != "" {
			if _, err := os.Stat(p); err != nil {
				// The variable is named and the path is not: a file can be
				// named for the provider whose entries it holds.
				return "", fmt.Errorf("%s is set but the file it names cannot be used (%s)", key, Failure(err))
			}
			return p, nil
		}
	}
	// No fallback to a clouds.yaml in the working directory: that is the
	// package directory, inside the repository, where a credentials file
	// could be staged by accident.
	if p := filepath.Join(os.Getenv("HOME"), ".config", "openstack", "clouds.yaml"); os.Getenv("HOME") != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", nil
}

// LoadCloud decodes a clouds.yaml with a typed YAML decode and returns the
// named entry, validated. The structural decode keeps a blank auth_url from
// capturing the password on the following line; the endpoint policy is applied
// here so a URL with embedded credentials or a query is refused before anything
// is logged. An absent entry or a missing password is a skip for the caller;
// a malformed file or an unusable entry is an error whose text never includes
// a field value or the entry's own name, which can be a provider's.
func LoadCloud(raw []byte, name string) (Cloud, error) {
	var doc struct {
		Clouds map[string]struct {
			Auth struct {
				AuthURL           string `yaml:"auth_url"`
				Username          string `yaml:"username"`
				Password          string `yaml:"password"`
				ProjectName       string `yaml:"project_name"`
				UserDomainName    string `yaml:"user_domain_name"`
				ProjectDomainName string `yaml:"project_domain_name"`
			} `yaml:"auth"`
			RegionName string `yaml:"region_name"`
		} `yaml:"clouds"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Cloud{}, ErrMalformedClouds
	}
	entry, ok := doc.Clouds[name]
	if !ok {
		return Cloud{}, ErrCloudAbsent
	}
	a := entry.Auth
	if a.Password == "" {
		return Cloud{}, ErrNoPassword
	}
	authURL := strings.TrimRight(a.AuthURL, "/")
	if authURL != "" && !strings.HasSuffix(authURL, "/v3") {
		authURL += "/v3"
	}
	c := Cloud{
		AuthURL: authURL, Username: a.Username, Password: a.Password,
		ProjectName: a.ProjectName, UserDomain: a.UserDomainName,
		ProjectDomain: a.ProjectDomainName, Region: entry.RegionName,
	}
	if c.UserDomain == "" {
		c.UserDomain = "Default"
	}
	if c.ProjectDomain == "" {
		c.ProjectDomain = c.UserDomain
	}
	var missing []string
	for _, f := range []struct{ name, value string }{
		{"auth_url", c.AuthURL}, {"username", c.Username},
		{"project_name", c.ProjectName}, {"region_name", c.Region},
	} {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return Cloud{}, fmt.Errorf("the cloud entry is missing required fields: %s", strings.Join(missing, ", "))
	}
	// ValidateEndpointURL's messages name the rule, never the URL.
	if err := swiftcontrol.ValidateEndpointURL(c.AuthURL, swiftcontrol.TLSModeSystem); err != nil {
		return Cloud{}, fmt.Errorf("the cloud entry's auth_url: %w", err)
	}
	return c, nil
}

// Backend is one cloud the real-cloud tests run against. Label is the only
// name that reaches a subtest name or a line of output; Cloud is the
// clouds.yaml key and is used to find the entry and for nothing else.
type Backend struct{ Label, Cloud, Backend string }

// Backends are the two configured clouds.
var Backends = []Backend{
	{"ceph", "aeronautes-ceph", "Ceph RGW Swift"},
	{"swift", "aeronautes-swift", "OpenStack Swift"},
}

// SelectBackends applies a selection such as ORACLE_CLOUD. A known label or
// key selects that backend. Any other key is an ad-hoc entry, which runs
// under a fixed label: an operator may well have named the entry for its
// provider.
func SelectBackends(only string) []Backend {
	if only == "" {
		return Backends
	}
	for _, b := range Backends {
		if b.Label == only || b.Cloud == only {
			return []Backend{b}
		}
	}
	return []Backend{{"adhoc", only, "ad-hoc"}}
}

// Failure says what kind of failure an operation met without printing any of
// the error's own text. A transport or TLS error names hosts the
// configuration never held, a certificate's alternative names among them, so
// no list of substitutions makes that text safe to keep in a log. The
// adapter's error kind, the HTTP status and the Go types in the chain are
// fixed vocabulary and are enough to tell an auth refusal from a DNS failure.
func Failure(err error) string {
	var types []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		types = append(types, fmt.Sprintf("%T", e))
	}
	out := "error text withheld; types " + strings.Join(types, " > ")
	var se *swiftcontrol.SwiftError
	if errors.As(err, &se) {
		kind := string(se.Kind)
		if se.HTTPStatus != 0 {
			kind += fmt.Sprintf(", HTTP %d", se.HTTPStatus)
		}
		out = kind + "; " + out
	}
	return out
}

// DescribeBody stands for bytes a server returned in a line of output. The
// real-cloud tests print nothing a server sent as text: an unexpected body
// can be anything, a proxy's branded sign-in page for one. The length and
// the start of the digest are enough to tell which of the bodies a run wrote
// came back, or that none did.
func DescribeBody(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%d bytes (SHA-256 %x…)", len(b), sum[:6])
}
