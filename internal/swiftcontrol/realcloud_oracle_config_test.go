// Config helpers for the real-cloud oracle (realcloud_oracle_test.go) and the
// regressions that keep them safe. No build tag on purpose: the regressions run
// in the default build without credentials or network, so these properties
// hold in ordinary CI, not only when someone runs the oracle itself.
package swiftcontrol_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// oracleCloud is one validated clouds.yaml entry. The password is a field of
// its own and is never placed in, derived into, or logged from another field.
type oracleCloud struct {
	AuthURL       string
	Username      string
	Password      string
	ProjectName   string
	UserDomain    string
	ProjectDomain string
	Region        string
}

var (
	errCloudAbsent = errors.New("cloud entry not present in clouds.yaml")
	errNoPassword  = errors.New("cloud entry has no password")
	// errMalformedClouds deliberately carries none of the decoder's text: a
	// YAML type error echoes the offending scalar, which in this file can be
	// a password.
	errMalformedClouds = errors.New("clouds.yaml does not decode into the expected shape (clouds.<name>.auth with string fields)")
)

// resolveCloudsPath picks the clouds.yaml to read. An explicitly selected path
// (ORACLE_CLOUDS, then OS_CLIENT_CONFIG_FILE) that cannot be used is an error,
// never a reason to read a different file: a typo in a disposable-cloud path
// must not direct real writes and deletes at another account whose entries
// happen to share the same names. The OpenStack-standard locations are tried
// only when no path was selected; "" means nothing is configured, so skip.
func resolveCloudsPath() (string, error) {
	for _, key := range []string{"ORACLE_CLOUDS", "OS_CLIENT_CONFIG_FILE"} {
		if p := os.Getenv(key); p != "" {
			if _, err := os.Stat(p); err != nil {
				return "", fmt.Errorf("%s is set but unusable: %w", key, err)
			}
			return p, nil
		}
	}
	for _, p := range []string{
		filepath.Join(os.Getenv("HOME"), ".config", "openstack", "clouds.yaml"),
		"clouds.yaml",
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", nil
}

// loadCloud decodes a clouds.yaml with a typed YAML decode and returns the
// named entry, validated. The structural decode keeps a blank auth_url from
// capturing the password on the following line; the endpoint policy is applied
// here so a URL with embedded credentials or a query is refused before anything
// is logged. An absent entry or a missing password is a skip for the caller;
// a malformed file or an unusable entry is an error whose text never includes
// a field value.
func loadCloud(raw []byte, name string) (oracleCloud, error) {
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
		return oracleCloud{}, errMalformedClouds
	}
	entry, ok := doc.Clouds[name]
	if !ok {
		return oracleCloud{}, errCloudAbsent
	}
	a := entry.Auth
	if a.Password == "" {
		return oracleCloud{}, errNoPassword
	}
	authURL := strings.TrimRight(a.AuthURL, "/")
	if authURL != "" && !strings.HasSuffix(authURL, "/v3") {
		authURL += "/v3"
	}
	c := oracleCloud{
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
		return oracleCloud{}, fmt.Errorf("cloud %q is missing required fields: %s", name, strings.Join(missing, ", "))
	}
	// ValidateEndpointURL's messages name the rule, never the URL.
	if err := swiftcontrol.ValidateEndpointURL(c.AuthURL, swiftcontrol.TLSModeSystem); err != nil {
		return oracleCloud{}, fmt.Errorf("cloud %q auth_url: %w", name, err)
	}
	return c, nil
}

var authAccountRe = regexp.MustCompile(`AUTH_[^/\s"']+`)

// redactIdentifiers strips the account identifiers an operation error can
// carry: the storage URL (whose path names the project), any AUTH_ account
// segment, the username and the project name. A failure diagnostic keeps the
// operation and the status without saying whose account it was.
func redactIdentifiers(msg string, c oracleCloud, storageURL string) string {
	for _, v := range []string{storageURL, c.Username, c.ProjectName} {
		if v != "" {
			msg = strings.ReplaceAll(msg, v, "<redacted>")
		}
	}
	return authAccountRe.ReplaceAllString(msg, "AUTH_<redacted>")
}

// teardownProbe removes the probe object and container, best-effort, on its own
// bounded context so it survives the test's context being cancelled. It acts
// only when this run created the container: a refused create (the name was
// already taken) must send no destructive request, because a fresh fingerprint
// proves an object's identity, not that this run owns it.
func teardownProbe(s *swiftcontrol.Session, container, objName string, created bool) {
	if !created {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if d, err := s.ObjectDetail(ctx, container, objName); err == nil {
		_, _ = s.DeletePlainObject(ctx, container, objName, d.ContentFingerprint())
	}
	_ = s.DeleteContainer(ctx, container)
}

// --- regressions: default build, no credentials, no network ---

func TestOracleCloudsPathSelection(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	present := filepath.Join(t.TempDir(), "clouds.yaml")
	if err := os.WriteFile(present, []byte("clouds: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, oracle, osClient, want string
		wantErr                      bool
	}{
		{"explicit ORACLE_CLOUDS missing is an error, not a fallback", missing, "", "", true},
		{"explicit OS_CLIENT_CONFIG_FILE missing is an error", "", missing, "", true},
		{"explicit ORACLE_CLOUDS is used as-is", present, "", present, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("ORACLE_CLOUDS", c.oracle)
			t.Setenv("OS_CLIENT_CONFIG_FILE", c.osClient)
			got, err := resolveCloudsPath()
			if (err != nil) != c.wantErr || got != c.want {
				t.Fatalf("got (%q, %v); want (%q, err=%v)", got, err, c.want, c.wantErr)
			}
		})
	}
}

func TestOracleLoadCloud(t *testing.T) {
	const noPassword = "clouds:\n  aeronautes-ceph:\n    auth:\n      auth_url: https://identity.example/\n      username: someone\n      project_name: \"12345\"\n    region_name: SYD\n"
	const valid = "clouds:\n  aeronautes-ceph:\n    auth:\n      auth_url: https://identity.example/\n      username: someone\n      password: \"pw\"\n      project_name: \"12345\"\n    region_name: SYD\n"
	cases := []struct {
		name, yaml, cloud string
		want              error // sentinel for errors.Is; nil means a valid decode
	}{
		{"absent entry skips", noPassword, "aeronautes-swift", errCloudAbsent},
		{"entry without password skips", noPassword, "aeronautes-ceph", errNoPassword},
		{"valid entry decodes with defaults", valid, "aeronautes-ceph", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := loadCloud([]byte(c.yaml), c.cloud)
			if c.want != nil {
				if !errors.Is(err, c.want) {
					t.Fatalf("want %v, got %v", c.want, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid entry: %v", err)
			}
			if got.AuthURL != "https://identity.example/v3" || got.ProjectDomain != "Default" || got.Region != "SYD" || got.ProjectName != "12345" {
				t.Fatalf("unexpected decode: %+v", got)
			}
		})
	}
}

// TestOracleRefusalsCarryNoFieldValue covers every loadCloud error path that a
// credential could reach: the blank auth_url that a line scanner would fill
// from the password line, an auth_url with embedded credentials, and the two
// malformed shapes whose raw decoder messages echo the offending scalar.
func TestOracleRefusalsCarryNoFieldValue(t *testing.T) {
	const canary = "canary-5f3a9b-not-a-real-secret"
	cases := []struct{ name, yaml string }{
		{"blank auth_url followed by the password line",
			"clouds:\n  aeronautes-ceph:\n    auth:\n      auth_url:\n      password: \"" + canary + "\"\n      username: someone\n      project_name: proj\n    region_name: r1\n"},
		{"auth_url with embedded credentials",
			"clouds:\n  aeronautes-ceph:\n    auth:\n      auth_url: https://someone:" + canary + "@identity.example/v3\n      password: pw\n      username: someone\n      project_name: proj\n    region_name: r1\n"},
		{"password scalar tagged as an integer",
			"clouds:\n  aeronautes-ceph:\n    auth:\n      auth_url: https://identity.example/v3\n      password: !!int " + canary + "\n      username: someone\n      project_name: proj\n    region_name: r1\n"},
		{"scalar where the auth mapping should be",
			"clouds:\n  aeronautes-ceph:\n    auth: " + canary + "\n    region_name: r1\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := loadCloud([]byte(c.yaml), "aeronautes-ceph")
			if err == nil {
				t.Fatal("the entry must be refused")
			}
			if strings.Contains(err.Error()+got.AuthURL+got.Username+got.Region, canary) {
				t.Fatalf("a field value leaked into the refusal: %v / %+v", err, got)
			}
		})
	}
}

func TestOracleRedactIdentifiers(t *testing.T) {
	c := oracleCloud{Username: "user-abc123", ProjectName: "proj-9876"}
	const storageURL = "https://storage.example/v1/AUTH_deadbeefcafe"
	msg := "GET " + storageURL + "/cont/obj: EOF (user user-abc123, project proj-9876)"
	got := redactIdentifiers(msg, c, storageURL)
	for _, leak := range []string{"AUTH_deadbeefcafe", "user-abc123", "proj-9876", storageURL} {
		if strings.Contains(got, leak) {
			t.Fatalf("%q survived redaction: %q", leak, got)
		}
	}
	if !strings.Contains(got, "GET") || !strings.Contains(got, "EOF") {
		t.Fatalf("redaction dropped the operation or status: %q", got)
	}
}

func TestOracleTeardownOnlyWhatThisRunCreated(t *testing.T) {
	// created=false must issue no request at all, so a nil session is safe:
	// touching it would panic and fail this test.
	teardownProbe(nil, "aeronautes-oracle-not-ours", "probe-object", false)
}
