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
// is logged. Nothing secret is logged, and nothing that names the provider or
// the account reaches success or failure output: a run is labelled by its
// backend, an ad-hoc clouds.yaml key is used only to find its entry, a
// failure prints the error's kind and types, never its text (oracleFailure),
// and nothing a server returned as text is printed: a body, an entity tag or
// a content type is reported by its length, its digest or whether it matches.
// Every fixture it creates has a random,
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
	"os"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func TestKeystoneV3Oracle(t *testing.T) {
	runOnOracleBackends(t, runOracle)
}

// runOnOracleBackends runs one oracle body per configured backend under the
// clouds.yaml selection and skip rules every real-cloud test shares, so a
// second oracle cannot drift from the first in how it finds, validates or
// declines a cloud.
func runOnOracleBackends(t *testing.T, run func(t *testing.T, label, backend string, c oracleCloud)) {
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
		t.Fatalf("the selected clouds.yaml could not be read (%s)", oracleFailure(err))
	}
	// Only the label is printed from here on; the path and the clouds.yaml
	// key can each be named for a provider.
	for _, b := range selectOracleBackends(os.Getenv("ORACLE_CLOUD")) {
		t.Run(b.label, func(t *testing.T) {
			cloud, err := loadCloud(raw, b.cloud)
			switch {
			case errors.Is(err, errCloudAbsent):
				t.Skipf("the %s entry is not in the selected clouds.yaml; skipping (%s)", b.label, b.backend)
			case errors.Is(err, errNoPassword):
				t.Skipf("the %s entry has no password in clouds.yaml; add it to run the oracle", b.label)
			case err != nil:
				t.Fatalf("the %s entry is misconfigured: %v", b.label, err)
			}
			run(t, b.label, b.backend, cloud)
		})
	}
}

func runOracle(t *testing.T, label, backend string, c oracleCloud) {
	// Nothing that names the provider is logged, on success or failure: no
	// endpoint, region or host, as well as no password, user or project. A run's
	// output is evidence that gets kept and pasted, and a CI log is public.
	t.Logf("[%s] backend=%s", label, backend)

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
		t.Fatalf("New(v3): %s", oracleFailure(err))
	}
	defer s.Close()
	// fail reports which operation failed and what kind of error it met. The
	// error's text is never printed: it carries the request URL, whose path
	// names the project, and hosts no configured value can predict.
	fail := func(op string, err error) {
		t.Helper()
		t.Fatalf("%s: %s", op, oracleFailure(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// (1) The real test: guardedAuth against a genuine Keystone token+catalog.
	if err := s.Authenticate(ctx); err != nil {
		fail("Authenticate (guardedAuth v3) against "+label, err)
	}
	expires := s.TokenExpires()
	t.Logf("AUTH OK: token expires in %s", time.Until(expires).Round(time.Second))
	if expires.IsZero() {
		t.Error("v3 token expiry should be captured (Keystone advertises expires_at)")
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
	if _, err := s.PutObject(ctx, probe, objName, bytes.NewReader(body), "text/plain", nil); err != nil {
		fail("PutObject", err)
	}
	t.Logf("PUT %s/%s", probe, objName)

	detail, err := s.ObjectDetail(ctx, probe, objName)
	if err != nil {
		fail("ObjectDetail after put", err)
	}
	t.Logf("HEAD: length=%d", detail.ContentLength)

	rc, err := s.OpenObjectContent(ctx, probe, objName)
	if err != nil {
		fail("OpenObjectContent", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		fail("read body", err)
	}
	if line := bodyMismatch("GET body", got, body); line != "" {
		t.Fatal(line)
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
	t.Logf("deleted probe container %s — full v3 round-trip OK against %s (%s)", probe, label, backend)
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
