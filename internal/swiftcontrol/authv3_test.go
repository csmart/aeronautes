package swiftcontrol

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// v3Config returns a minimal Keystone v3 password-method, project-scoped
// bookmark config pointing at the mock's /v3 endpoint.
func v3Config(authBase string) Config {
	return Config{
		AuthVersion:   3,
		AuthURL:       authBase + "/v3",
		Username:      "keystoneuser",
		APIKey:        "keystonepass",
		UserDomain:    "Default",
		ProjectName:   "demo",
		ProjectDomain: "Default",
		Region:        "RegionOne",
	}
}

// TestKeystoneV3AuthenticatesAndReadsAccount drives the real built-in v3
// authenticator (wrapped by guardedAuth) against the mock Keystone: the
// catalog's object-store endpoint is validated and captured, the token's
// expiry is read, and the captured token then authenticates a real
// account read — proving the whole v3 path end to end, not just token
// capture.
func TestKeystoneV3AuthenticatesAndReadsAccount(t *testing.T) {
	mock := swifttest.New("keystoneuser", "keystonepass")
	mock.AddContainer("demo", "")
	srv := httptest.NewServer(mock)
	defer srv.Close()

	s, err := New(v3Config(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Authenticate(ctx); err != nil {
		t.Fatalf("v3 authenticate: %v", err)
	}

	if want := srv.URL + "/v1/AUTH_test"; s.StorageURL() != want {
		t.Errorf("StorageURL = %q, want the validated catalog endpoint %q", s.StorageURL(), want)
	}
	if s.AuthToken() == "" {
		t.Error("no token captured from the v3 subject token")
	}
	if exp := s.TokenExpires(); !exp.After(time.Now()) {
		t.Errorf("TokenExpires = %v, want a future time from the token's expires_at", exp)
	}

	p, err := s.AccountPortrait(ctx)
	if err != nil {
		t.Fatalf("account portrait after v3 auth: %v", err)
	}
	if p.Containers != 1 {
		t.Errorf("Containers = %d, want 1 (the token must work against the storage endpoint)", p.Containers)
	}
}

// TestKeystoneV3RejectsInsecureCatalogEndpoint proves guardedAuth runs the
// storage-URL gate on the endpoint the identity catalog chose: a public
// http endpoint is refused before the token is ever used, exactly as it
// would be for a v1 storage URL.
func TestKeystoneV3RejectsInsecureCatalogEndpoint(t *testing.T) {
	mock := swifttest.New("keystoneuser", "keystonepass")
	mock.V3StorageEndpoint = "http://storage.example.invalid/v1/AUTH_test"
	srv := httptest.NewServer(mock)
	defer srv.Close()

	s, err := New(v3Config(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	err = s.Authenticate(context.Background())
	if err == nil {
		t.Fatal("expected the catalog's public http endpoint to be refused")
	}
	if !strings.Contains(err.Error(), "storage URL") {
		t.Errorf("error = %v, want a storage URL rejection", err)
	}
}

// TestKeystoneV3RejectsBadCredentials confirms a failed v3 login surfaces
// an error rather than a half-built session.
func TestKeystoneV3RejectsBadCredentials(t *testing.T) {
	mock := swifttest.New("keystoneuser", "keystonepass")
	srv := httptest.NewServer(mock)
	defer srv.Close()

	cfg := v3Config(srv.URL)
	cfg.APIKey = "wrongpass"
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Authenticate(context.Background()); err == nil {
		t.Fatal("expected bad v3 credentials to fail authentication")
	}
}

// TestNewRejectsV3StorageOverride guards the v3 contract: Keystone selects
// the endpoint from its catalog, so a storage-URL override is refused
// rather than silently ignored.
func TestNewRejectsV3StorageOverride(t *testing.T) {
	_, err := New(Config{
		AuthVersion:        3,
		AuthURL:            "https://keystone.example/v3",
		Username:           "u",
		APIKey:             "p",
		StorageURLOverride: "https://storage.example/v1/AUTH_test",
	})
	if err == nil || !strings.Contains(err.Error(), "Keystone v3") {
		t.Fatalf("err = %v, want a v3 override rejection", err)
	}
}

// TestNewRejectsUnsupportedAuthVersion keeps New from silently accepting a
// protocol it does not implement.
func TestNewRejectsUnsupportedAuthVersion(t *testing.T) {
	_, err := New(Config{
		AuthVersion: 2,
		AuthURL:     "https://swift.example/v2",
		Username:    "u",
		APIKey:      "p",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported auth version") {
		t.Fatalf("err = %v, want an unsupported-version rejection", err)
	}
}

// TestNewRejectsV3WithoutRegion keeps v3 from selecting an endpoint
// ambiguously: a multi-region catalogue with no configured region could
// bind the token to the wrong namespace (review db:1015).
func TestNewRejectsV3WithoutRegion(t *testing.T) {
	cfg := v3Config("https://keystone.example")
	cfg.Region = ""
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("err = %v, want a missing-region rejection", err)
	}
}

// TestKeystoneV3PinsRegionAcrossReorder is the regression for db:1015: a
// re-authentication against a reordered multi-region catalogue must keep
// the configured region's endpoint, never silently switch to another
// region's namespace.
func TestKeystoneV3PinsRegionAcrossReorder(t *testing.T) {
	mock := swifttest.New("keystoneuser", "keystonepass")
	srv := httptest.NewServer(mock)
	defer srv.Close()
	one := srv.URL + "/v1/AUTH_test"
	two := "https://two.example.invalid/v1/AUTH_other"
	mock.V3Endpoints = []swifttest.V3Endpoint{
		{Region: "RegionOne", URL: one},
		{Region: "RegionTwo", URL: two},
	}

	s, err := New(v3Config(srv.URL)) // Region RegionOne
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Authenticate(ctx); err != nil {
		t.Fatalf("first auth: %v", err)
	}
	if s.StorageURL() != one {
		t.Fatalf("StorageURL = %q, want RegionOne %q", s.StorageURL(), one)
	}

	// The catalogue now lists RegionTwo first; a re-auth must still bind
	// RegionOne, not the first entry.
	mock.V3Endpoints = []swifttest.V3Endpoint{
		{Region: "RegionTwo", URL: two},
		{Region: "RegionOne", URL: one},
	}
	s.ForceReauth()
	if err := s.Authenticate(ctx); err != nil {
		t.Fatalf("re-auth after reorder: %v", err)
	}
	if s.StorageURL() != one {
		t.Errorf("after reorder StorageURL = %q, want RegionOne %q; region pinning must defeat catalogue order", s.StorageURL(), one)
	}
}

// TestKeystoneV3RefusesEndpointSwitchOnReauth is the defence-in-depth
// guard: even within the configured region, a re-auth that advertises a
// different endpoint URL is refused rather than silently adopted, so the
// namespace the operator reviewed cannot move mid-session.
func TestKeystoneV3RefusesEndpointSwitchOnReauth(t *testing.T) {
	mock := swifttest.New("keystoneuser", "keystonepass")
	srv := httptest.NewServer(mock)
	defer srv.Close()
	mock.V3StorageEndpoint = srv.URL + "/v1/AUTH_test"

	s, err := New(v3Config(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Authenticate(ctx); err != nil {
		t.Fatalf("first auth: %v", err)
	}

	// The same region now advertises a moved endpoint; the re-auth refuses.
	mock.V3StorageEndpoint = "https://moved.example.invalid/v1/AUTH_test"
	s.ForceReauth()
	if err := s.Authenticate(ctx); err == nil {
		t.Fatal("expected a refusal when the re-auth endpoint differs from the one in use")
	}
}
