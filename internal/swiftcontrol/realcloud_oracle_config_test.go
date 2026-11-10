// Config helpers for the real-cloud oracle (realcloud_oracle_test.go) and the
// regressions that keep them safe. No build tag on purpose: the regressions run
// in the default build without credentials or network, so these properties
// hold in ordinary CI, not only when someone runs the oracle itself.
package swiftcontrol_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
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
				// The variable is named and the path is not: a file can be
				// named for the provider whose entries it holds.
				return "", fmt.Errorf("%s is set but the file it names cannot be used (%s)", key, oracleFailure(err))
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

// loadCloud decodes a clouds.yaml with a typed YAML decode and returns the
// named entry, validated. The structural decode keeps a blank auth_url from
// capturing the password on the following line; the endpoint policy is applied
// here so a URL with embedded credentials or a query is refused before anything
// is logged. An absent entry or a missing password is a skip for the caller;
// a malformed file or an unusable entry is an error whose text never includes
// a field value or the entry's own name, which can be a provider's.
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
		return oracleCloud{}, fmt.Errorf("the cloud entry is missing required fields: %s", strings.Join(missing, ", "))
	}
	// ValidateEndpointURL's messages name the rule, never the URL.
	if err := swiftcontrol.ValidateEndpointURL(c.AuthURL, swiftcontrol.TLSModeSystem); err != nil {
		return oracleCloud{}, fmt.Errorf("the cloud entry's auth_url: %w", err)
	}
	return c, nil
}

// oracleBackend is one cloud the oracles run against. label is the only name
// that reaches a subtest name or a line of output; cloud is the clouds.yaml
// key and is used to find the entry and for nothing else.
type oracleBackend struct{ label, cloud, backend string }

var oracleBackends = []oracleBackend{
	{"ceph", "aeronautes-ceph", "Ceph RGW Swift"},
	{"swift", "aeronautes-swift", "OpenStack Swift"},
}

// selectOracleBackends applies ORACLE_CLOUD. A known label or key selects that
// backend. Any other key is an ad-hoc entry, which runs under a fixed label:
// an operator may well have named the entry for its provider.
func selectOracleBackends(only string) []oracleBackend {
	if only == "" {
		return oracleBackends
	}
	for _, b := range oracleBackends {
		if b.label == only || b.cloud == only {
			return []oracleBackend{b}
		}
	}
	return []oracleBackend{{"adhoc", only, "ad-hoc"}}
}

// oracleFailure says what kind of failure an operation met without printing
// any of the error's own text. A transport or TLS error names hosts the
// configuration never held, a certificate's alternative names among them, so
// no list of substitutions makes that text safe to keep in a log. The
// adapter's error kind, the HTTP status and the Go types in the chain are
// fixed vocabulary and are enough to tell an auth refusal from a DNS failure.
func oracleFailure(err error) string {
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

// describeBody stands for bytes a server returned in a line of output. The
// oracles print nothing a server sent as text: an unexpected body can be
// anything, a proxy's branded sign-in page for one. The length and the start
// of the digest are enough to tell which of the bodies a run wrote came back,
// or that none did.
func describeBody(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%d bytes (SHA-256 %x…)", len(b), sum[:6])
}

// describeBodies is describeBody over what an archive held, in order.
func describeBodies(bodies []string) string {
	described := make([]string, len(bodies))
	for i, body := range bodies {
		described[i] = describeBody([]byte(body))
	}
	return "[" + strings.Join(described, ", ") + "]"
}

// bodyMismatch is the failure line for a body that is not the one expected,
// or "" when it is.
func bodyMismatch(what string, got, want []byte) string {
	if bytes.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("%s: read %s, want %s", what, describeBody(got), describeBody(want))
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
	const noPassword = "clouds:\n  aeronautes-ceph:\n    auth:\n      auth_url: https://identity.example/\n      username: someone\n      project_name: \"00000000\"\n    region_name: r1\n"
	const valid = "clouds:\n  aeronautes-ceph:\n    auth:\n      auth_url: https://identity.example/\n      username: someone\n      password: \"pw\"\n      project_name: \"00000000\"\n    region_name: r1\n"
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
			if got.AuthURL != "https://identity.example/v3" || got.ProjectDomain != "Default" || got.Region != "r1" || got.ProjectName != "00000000" {
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

// A failure line must carry nothing that names where the oracle ran, however
// the error spells it: the host with and without its port, a name that only a
// certificate holds, the request URL and its account path.
func TestOracleFailureCarriesNoErrorText(t *testing.T) {
	const tokens = "https://identity.provider.example:5000/v3/auth/tokens"
	dns := &url.Error{Op: "Post", URL: tokens, Err: &net.OpError{Op: "dial", Net: "tcp",
		Err: &net.DNSError{Err: "no such host", Name: "identity.provider.example", IsNotFound: true}}}
	tls := &url.Error{Op: "Post", URL: tokens, Err: x509.HostnameError{
		Certificate: &x509.Certificate{DNSNames: []string{"alternate.provider.example"}}, Host: "identity.provider.example"}}
	refused := fmt.Errorf("GET https://storage.provider.example/v1/AUTH_deadbeefcafe/cont: %w",
		&swiftcontrol.SwiftError{Kind: swiftcontrol.KindForbidden, HTTPStatus: 403, Message: "Forbidden", TransID: "tx-provider-region"})
	for name, err := range map[string]error{"dns": swiftcontrol.Normalize(dns), "tls": swiftcontrol.Normalize(tls), "refused": refused} {
		got := oracleFailure(err)
		for _, leak := range []string{"provider", "example", "5000", "AUTH_", "deadbeef", "tx-"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: %q reached the failure line: %s", name, leak, got)
			}
		}
		if !strings.Contains(got, "types ") {
			t.Errorf("%s: the failure line says nothing about the error: %s", name, got)
		}
	}
	if got := oracleFailure(refused); !strings.Contains(got, "forbidden, HTTP 403") {
		t.Errorf("a refused request lost its kind and status: %s", got)
	}
}

// A body that is not the one written fails the run without being printed: the
// line carries lengths and digests, which still tell the two bodies apart.
func TestOracleBodyMismatchPrintsNoContent(t *testing.T) {
	wrote := []byte("aeronautes keystone v3 oracle probe\n")
	page := []byte("<html>Sign in to identity.provider.example</html>")
	got := bodyMismatch("GET body", page, wrote)
	if got == "" {
		t.Fatal("a different body was not reported as a mismatch")
	}
	for _, leak := range []string{"provider", "Sign in", "html", "oracle probe"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q reached the mismatch line: %s", leak, got)
		}
	}
	if describeBody(page) == describeBody(wrote) {
		t.Errorf("the line cannot tell the two bodies apart: %s", got)
	}
	if line := bodyMismatch("GET body", wrote, wrote); line != "" {
		t.Errorf("the body that was written is reported as a mismatch: %s", line)
	}
}

// An ad-hoc ORACLE_CLOUD key finds its entry and is never a label, so an entry
// named for its provider cannot become a subtest name or a line of output; the
// two configured backends are selected by either of their names.
func TestOracleAdHocKeyIsNeverALabel(t *testing.T) {
	got := selectOracleBackends("provider-production")
	if len(got) != 1 || got[0].cloud != "provider-production" {
		t.Fatalf("the ad-hoc key does not select its own entry: %+v", got)
	}
	if strings.Contains(got[0].label+got[0].backend, "provider") {
		t.Fatalf("the ad-hoc key reached a displayed name: %+v", got[0])
	}
	for _, only := range []string{"ceph", "aeronautes-ceph"} {
		if got := selectOracleBackends(only); len(got) != 1 || got[0].label != "ceph" {
			t.Errorf("ORACLE_CLOUD=%s selected %+v, want the ceph backend", only, got)
		}
	}
	if got := selectOracleBackends(""); len(got) != len(oracleBackends) {
		t.Errorf("no selection ran %d backends, want all %d", len(got), len(oracleBackends))
	}
	// The refusals a misnamed entry can reach do not echo the key either.
	_, err := loadCloud([]byte("clouds:\n  provider-production:\n    auth:\n      password: pw\n"), "provider-production")
	if err == nil || strings.Contains(err.Error(), "provider") {
		t.Fatalf("a refused entry's error names its key: %v", err)
	}
}

func TestOracleTeardownOnlyWhatThisRunCreated(t *testing.T) {
	// created=false must issue no request at all, so a nil session is safe:
	// touching it would panic and fail this test.
	teardownProbe(nil, "aeronautes-oracle-not-ours", "probe-object", false)
}
