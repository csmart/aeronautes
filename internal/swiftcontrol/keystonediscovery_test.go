package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// identityService is the mock Keystone with a version document at root:
// document answers a GET there, given the server's base URL, and every
// other request goes to the mock, which takes the v3 token request at
// /v3/auth/tokens. discoveries counts every GET outside the account, which
// is what a version request is.
type identityService struct {
	mock        *swifttest.Server
	srv         *httptest.Server
	discoveries atomic.Int32
}

func newIdentityService(t *testing.T, root string, document func(base string) (int, string)) *identityService {
	t.Helper()
	id := &identityService{mock: swifttest.New("keystoneuser", "keystonepass")}
	id.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/v1/") {
			id.discoveries.Add(1)
		}
		if r.Method == http.MethodGet && r.URL.Path == root {
			status, body := document(id.srv.URL)
			if status == http.StatusFound {
				http.Redirect(w, r, body, status)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
			return
		}
		id.mock.ServeHTTP(w, r)
	}))
	t.Cleanup(id.srv.Close)
	return id
}

func versionEntry(id, status, href string) string {
	return fmt.Sprintf(`{"id":%q,"status":%q,"links":[{"rel":"self","href":%q}]}`, id, status, href)
}

func versionList(entries ...string) string {
	return `{"versions":{"values":[` + strings.Join(entries, ",") + `]}}`
}

func unexpectedDocument(t *testing.T) func(string) (int, string) {
	return func(string) (int, string) {
		t.Error("a version request was made for a v3 URL")
		return http.StatusNotFound, ""
	}
}

// A URL that already ends in v3 is the endpoint: the token request goes
// there with no version request before it.
func TestKeystoneV3URLIsUsedAsGiven(t *testing.T) {
	for _, suffix := range []string{"/v3", "/v3/"} {
		t.Run(suffix, func(t *testing.T) {
			id := newIdentityService(t, "/", unexpectedDocument(t))
			cfg := v3Config(id.srv.URL)
			cfg.AuthURL = id.srv.URL + suffix
			s, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Authenticate(context.Background()); err != nil {
				t.Fatalf("authenticate: %v", err)
			}
			if n := id.discoveries.Load(); n != 0 {
				t.Errorf("%d version requests for a v3 URL", n)
			}
		})
	}
}

// A root URL, or a v3 root under another path, is asked once which
// versions it offers; the stable v3 link is used, and a later
// authentication on the same session reuses it without asking again.
func TestKeystoneV3DiscoversTheV3Endpoint(t *testing.T) {
	cases := []struct {
		name     string
		root     string
		document func(base string) (int, string)
	}{
		{"version list", "/", func(base string) (int, string) {
			return http.StatusMultipleChoices, versionList(
				versionEntry("v2.0", "stable", base+"/v2.0/"),
				versionEntry("v3.14", "stable", base+"/v3/"),
			)
		}},
		{"single version", "/identity", func(base string) (int, string) {
			return http.StatusOK, `{"version":` + versionEntry("v3.14", "CURRENT", base+"/v3/") + `}`
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := newIdentityService(t, tc.root, tc.document)
			cfg := v3Config(id.srv.URL)
			cfg.AuthURL = id.srv.URL + tc.root
			s, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err := s.Authenticate(ctx); err != nil {
				t.Fatalf("authenticate: %v", err)
			}
			if want := id.srv.URL + "/v1/AUTH_test"; s.StorageURL() != want {
				t.Errorf("StorageURL = %q, want %q", s.StorageURL(), want)
			}
			s.ForceReauth()
			if err := s.Authenticate(ctx); err != nil {
				t.Fatalf("second authenticate: %v", err)
			}
			if n := id.discoveries.Load(); n != 1 {
				t.Errorf("%d version requests, want 1 for the session", n)
			}
			if n := id.mock.AuthRequests(); n != 2 {
				t.Errorf("%d token requests, want 2", n)
			}
		})
	}
}

// Every way a URL can fail to offer v3 at its own origin is refused before
// any token request, and the refusal names the configured URL, asks for
// the v3 endpoint, and never quotes the link the service advertised.
func TestKeystoneV3DiscoveryRefusals(t *testing.T) {
	otherPort := func(base string) string {
		u, _ := url.Parse(base)
		port, _ := strconv.Atoi(u.Port())
		return "http://" + u.Hostname() + ":" + strconv.Itoa(port+1)
	}
	stable := func(href string) (int, string) {
		return http.StatusMultipleChoices, versionList(versionEntry("v3.14", "stable", href))
	}
	cases := []struct {
		name       string
		document   func(base string) (int, string)
		advertised func(base string) string
	}{
		{"another host", func(string) (int, string) { return stable("http://identity.other.invalid/v3/") },
			func(string) string { return "identity.other.invalid" }},
		{"another scheme", func(base string) (int, string) {
			return stable(strings.Replace(base, "http://", "https://", 1) + "/v3/")
		},
			func(base string) string { return strings.Replace(base, "http://", "https://", 1) }},
		{"another port", func(base string) (int, string) { return stable(otherPort(base) + "/v3/") }, otherPort},
		{"a redirect", func(base string) (int, string) { return http.StatusFound, base + "/v3/" }, nil},
		{"no stable v3", func(base string) (int, string) {
			return http.StatusMultipleChoices, versionList(
				versionEntry("v2.0", "stable", base+"/v2.0/"),
				versionEntry("v3.14", "deprecated", base+"/v3/"),
			)
		}, nil},
		{"two stable v3 links", func(base string) (int, string) {
			return http.StatusMultipleChoices, versionList(
				versionEntry("v3.14", "stable", base+"/v3/"),
				versionEntry("v3.10", "stable", base+"/identity/v3/"),
			)
		}, nil},
		{"user information in the link", func(base string) (int, string) {
			return stable(strings.Replace(base, "http://", "http://someone:secret@", 1) + "/v3/")
		}, func(string) string { return "someone" }},
		{"a malformed document", func(string) (int, string) { return http.StatusMultipleChoices, "<html>" }, nil},
		{"a failed request", func(string) (int, string) { return http.StatusInternalServerError, "{}" }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := newIdentityService(t, "/", tc.document)
			cfg := v3Config(id.srv.URL)
			cfg.AuthURL = id.srv.URL + "/"
			s, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			err = s.Authenticate(context.Background())
			if err == nil {
				t.Fatal("authenticated through a URL that offers no v3 at its own origin")
			}
			if n := id.mock.AuthRequests(); n != 0 {
				t.Errorf("%d token requests were sent; the password must not travel", n)
			}
			text := err.Error()
			for _, want := range []string{cfg.AuthURL, "give the v3 endpoint, for example " + id.srv.URL + "/v3"} {
				if !strings.Contains(text, want) {
					t.Errorf("refusal %q does not say %q", text, want)
				}
			}
			if tc.advertised != nil && strings.Contains(text, tc.advertised(id.srv.URL)) {
				t.Errorf("refusal %q quotes the advertised link", text)
			}
		})
	}
}

// A connect the caller gave up on reports the cancellation, not a URL that
// offers no v3, and sends no token request.
func TestKeystoneV3DiscoveryReportsCancellation(t *testing.T) {
	id := newIdentityService(t, "/", unexpectedDocument(t))
	cfg := v3Config(id.srv.URL)
	cfg.AuthURL = id.srv.URL + "/"
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = s.Authenticate(ctx)
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "does not offer Keystone v3") {
		t.Fatalf("Authenticate after cancel = %v, want the cancellation", err)
	}
	if n := id.mock.AuthRequests(); n != 0 {
		t.Errorf("%d token requests after a cancelled connect", n)
	}
}

// An absent port is the scheme's default, and host names compare without
// regard to case.
func TestSameOriginReadsDefaultPorts(t *testing.T) {
	parse := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	if !sameOrigin(parse("https://Identity.Example.com/"), parse("https://identity.example.com:443/v3/")) {
		t.Error("an implicit 443 differs from an explicit one")
	}
	if sameOrigin(parse("https://identity.example.com/"), parse("http://identity.example.com:443/v3/")) {
		t.Error("http on port 443 matched https")
	}
}
