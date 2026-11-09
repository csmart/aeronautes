//go:build realcloud

// Oracle: drive the real Keystone v3 path (guardedAuth wrapping the ncw/swift
// builtin authenticator) against GENUINE OpenStack clouds, instead of the
// swifttest /v3/auth/tokens fixture. The fixture cannot give a real token or a
// real, multi-region service catalog; this can. It covers two backends by a
// short subtest name and the clouds.yaml entry each reads (the entries keep an
// "aeronautes-" prefix so they do not collide with any other cloud of that
// name), named for the BACKEND under test, not the provider:
//
//	subtest  clouds.yaml entry  backend
//	ceph     aeronautes-ceph    Ceph RGW's Swift API
//	swift    aeronautes-swift   a genuine OpenStack Swift
//
// It never runs in the default build (the "realcloud" tag excludes it) and is
// never a required check. Credentials are read from a clouds.yaml at run time
// with a typed decode and validated, endpoint policy included, before anything
// is logged. Nothing secret is logged, and user/project identifiers stay out of
// success and failure output alike. Every fixture it creates has a random,
// unique name and is torn down only when this run actually created it. Run it
// from the repo root:
//
//	make oracle CLOUDS=/path/to/clouds.yaml
//
// CLOUD=ceph (or swift) runs one backend. The config helpers and their
// regressions live in realcloud_oracle_config_test.go and run in the default
// build; the legacy versioning oracle in realcloud_versioning_test.go shares
// the backend loop below.
package swiftcontrol_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

var oracleBackends = []struct{ name, cloud, backend string }{
	{"ceph", "aeronautes-ceph", "Ceph RGW Swift"},
	{"swift", "aeronautes-swift", "OpenStack Swift"},
}

func TestKeystoneV3Oracle(t *testing.T) {
	runOnOracleBackends(t, runOracle)
}

// runOnOracleBackends runs one oracle body per configured backend under the
// clouds.yaml selection and skip rules every real-cloud test shares, so a
// second oracle cannot drift from the first in how it finds, validates or
// declines a cloud.
func runOnOracleBackends(t *testing.T, run func(t *testing.T, cloudName, backend string, c oracleCloud)) {
	path, err := resolveCloudsPath()
	if err != nil {
		// An explicitly selected source that cannot be used is an error, never
		// a reason to silently read a different account's clouds.yaml.
		t.Fatalf("clouds.yaml source: %v", err)
	}
	if path == "" {
		t.Skip("no clouds.yaml configured (set ORACLE_CLOUDS or OS_CLIENT_CONFIG_FILE); skipping real-cloud oracle")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	backends := oracleBackends
	if only := os.Getenv("ORACLE_CLOUD"); only != "" {
		sel := struct{ name, cloud, backend string }{only, only, "ad-hoc"}
		for _, b := range oracleBackends {
			if b.name == only || b.cloud == only {
				sel = b
				break
			}
		}
		backends = []struct{ name, cloud, backend string }{sel}
	}
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			cloud, err := loadCloud(raw, b.cloud)
			switch {
			case errors.Is(err, errCloudAbsent):
				t.Skipf("cloud %q not in %s; skipping (%s)", b.cloud, path, b.backend)
			case errors.Is(err, errNoPassword):
				t.Skipf("cloud %q has no password in clouds.yaml; add it to run the oracle", b.cloud)
			case err != nil:
				t.Fatalf("cloud %q misconfigured: %v", b.cloud, err)
			}
			run(t, b.cloud, b.backend, cloud)
		})
	}
}

func runOracle(t *testing.T, cloudName, backend string, c oracleCloud) {
	// loadCloud has applied the endpoint policy, so this URL carries no userinfo
	// or query; the password is never logged and user/project identifiers are
	// deliberately omitted from every diagnostic.
	t.Logf("[%s] backend=%s auth=%s region=%s", cloudName, backend, c.AuthURL, c.Region)

	s, err := swiftcontrol.New(swiftcontrol.Config{
		AuthVersion:   3,
		AuthURL:       c.AuthURL,
		Username:      c.Username,
		APIKey:        c.Password,
		UserDomain:    c.UserDomain,
		ProjectName:   c.ProjectName,
		ProjectDomain: c.ProjectDomain,
		Region:        c.Region,
	})
	if err != nil {
		t.Fatalf("New(v3): %s", redactIdentifiers(err.Error(), c, ""))
	}
	defer s.Close()
	// fail reports an operation error with account identifiers redacted: a
	// transport or status error carries the full request URL, whose path names
	// the project.
	fail := func(op string, err error) {
		t.Helper()
		t.Fatalf("%s: %s", op, redactIdentifiers(err.Error(), c, s.StorageURL()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// (1) The real test: guardedAuth against a genuine Keystone token+catalog.
	if err := s.Authenticate(ctx); err != nil {
		fail("Authenticate (guardedAuth v3) against "+cloudName, err)
	}
	storageURL := s.StorageURL()
	expires := s.TokenExpires()
	host := storageURL
	if u, perr := url.Parse(storageURL); perr == nil && u.Host != "" {
		host = u.Host // the endpoint host, never the AUTH_<project> path
	}
	t.Logf("AUTH OK: endpoint host=%s, token expires in %s", host, time.Until(expires).Round(time.Second))
	if expires.IsZero() {
		t.Error("v3 token expiry should be captured (Keystone advertises expires_at)")
	}
	if !strings.Contains(strings.ToLower(storageURL), strings.ToLower(c.Region)) {
		t.Logf("note: endpoint host %q does not contain region %q (endpoint naming is cloud-specific)", host, c.Region)
	}

	// (2) Authenticated account read.
	portrait, err := s.AccountPortrait(ctx)
	if err != nil {
		fail("AccountPortrait", err)
	}
	t.Logf("account: containers=%d objects=%d bytesUsed=%d", portrait.Containers, portrait.Objects, portrait.BytesUsed)

	// (3) Self-contained round-trip in a container this run owns. The name is
	// random so it cannot collide with a pre-existing one, and cleanup is armed
	// only AFTER a successful create, so a refused create deletes nothing.
	probe := "aeronautes-oracle-" + randomSuffix(t)
	const objName = "probe-object"
	created := false
	defer func() { teardownProbe(s, probe, objName, created) }()

	if _, err := s.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: probe}); err != nil {
		fail("CreateContainer", err)
	}
	created = true
	t.Logf("created probe container %s", probe)

	body := []byte("aeronautes keystone v3 oracle probe\n")
	etag, err := s.PutObject(ctx, probe, objName, bytes.NewReader(body), "text/plain", nil)
	if err != nil {
		fail("PutObject", err)
	}
	t.Logf("PUT %s/%s etag=%s", probe, objName, etag)

	detail, err := s.ObjectDetail(ctx, probe, objName)
	if err != nil {
		fail("ObjectDetail after put", err)
	}
	t.Logf("HEAD: contentType=%s etag=%s", detail.ContentType, detail.ETag)

	rc, err := s.OpenObjectContent(ctx, probe, objName)
	if err != nil {
		fail("OpenObjectContent", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		fail("read body", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("GET body mismatch: got %q want %q", got, body)
	}
	t.Logf("GET body round-trips (%d bytes)", len(got))

	res, err := s.DeletePlainObject(ctx, probe, objName, detail.ContentFingerprint())
	if err != nil {
		fail("DeletePlainObject", err)
	}
	t.Logf("DELETE outcome=%v accepted=%v", res.Outcome, res.Accepted)
	if _, err := s.ObjectDetail(ctx, probe, objName); err == nil {
		t.Errorf("object still present after delete")
	}

	if err := s.DeleteContainer(ctx, probe); err != nil {
		fail("DeleteContainer", err)
	}
	t.Logf("deleted probe container %s — full v3 round-trip OK against %s (%s)", probe, cloudName, backend)
}

// randomSuffix is a collision-free container-name suffix (16 hex chars).
func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random suffix: %v", err)
	}
	return hex.EncodeToString(b)
}
