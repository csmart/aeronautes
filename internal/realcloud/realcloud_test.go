// Regressions that keep the real-cloud helpers safe. They run in the default
// build without credentials or network, so these properties hold in ordinary
// CI, not only when someone runs a real-cloud suite.
package realcloud_test

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/realcloud"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

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
			got, err := realcloud.ResolveCloudsPath()
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
		{"absent entry skips", noPassword, "aeronautes-swift", realcloud.ErrCloudAbsent},
		{"entry without password skips", noPassword, "aeronautes-ceph", realcloud.ErrNoPassword},
		{"valid entry decodes with defaults", valid, "aeronautes-ceph", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := realcloud.LoadCloud([]byte(c.yaml), c.cloud)
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
			got, err := realcloud.LoadCloud([]byte(c.yaml), "aeronautes-ceph")
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
		got := realcloud.Failure(err)
		for _, leak := range []string{"provider", "example", "5000", "AUTH_", "deadbeef", "tx-"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: %q reached the failure line: %s", name, leak, got)
			}
		}
		if !strings.Contains(got, "types ") {
			t.Errorf("%s: the failure line says nothing about the error: %s", name, got)
		}
	}
	if got := realcloud.Failure(refused); !strings.Contains(got, "forbidden, HTTP 403") {
		t.Errorf("a refused request lost its kind and status: %s", got)
	}
}

// An ad-hoc ORACLE_CLOUD key finds its entry and is never a label, so an entry
// named for its provider cannot become a subtest name or a line of output; the
// two configured backends are selected by either of their names.
func TestOracleAdHocKeyIsNeverALabel(t *testing.T) {
	got := realcloud.SelectBackends("provider-production")
	if len(got) != 1 || got[0].Cloud != "provider-production" {
		t.Fatalf("the ad-hoc key does not select its own entry: %+v", got)
	}
	if strings.Contains(got[0].Label+got[0].Backend, "provider") {
		t.Fatalf("the ad-hoc key reached a displayed name: %+v", got[0])
	}
	for _, only := range []string{"ceph", "aeronautes-ceph"} {
		if got := realcloud.SelectBackends(only); len(got) != 1 || got[0].Label != "ceph" {
			t.Errorf("ORACLE_CLOUD=%s selected %+v, want the ceph backend", only, got)
		}
	}
	if got := realcloud.SelectBackends(""); len(got) != len(realcloud.Backends) {
		t.Errorf("no selection ran %d backends, want all %d", len(got), len(realcloud.Backends))
	}
	// The refusals a misnamed entry can reach do not echo the key either.
	_, err := realcloud.LoadCloud([]byte("clouds:\n  provider-production:\n    auth:\n      password: pw\n"), "provider-production")
	if err == nil || strings.Contains(err.Error(), "provider") {
		t.Fatalf("a refused entry's error names its key: %v", err)
	}
}
