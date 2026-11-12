package swiftcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// discoveryTimeout and discoveryBodyLimit bound the one unauthenticated
// read a v3 session may make before its first token request.
const (
	discoveryTimeout   = 30 * time.Second
	discoveryBodyLimit = 1 << 20
)

// keystoneV3Endpoint returns the URL a v3 token request is sent to (ADR
// 0113). An Identity URL whose path ends in v3 is that URL already;
// otherwise the Identity service is asked which versions it offers, and
// its stable v3 link is used only at the configured URL's own origin,
// because the token request carries the password. Every refusal names the
// configured URL and never the link the service advertised.
func keystoneV3Endpoint(ctx context.Context, client *http.Client, authURL string) (string, error) {
	configured, err := url.Parse(authURL)
	if err != nil {
		return "", fmt.Errorf("Identity URL %q: %w", authURL, err)
	}
	trimmed := strings.TrimSuffix(configured.Path, "/")
	if trimmed[strings.LastIndex(trimmed, "/")+1:] == "v3" {
		return authURL, nil
	}
	link, err := discoverV3Link(ctx, client, authURL)
	if err == nil && !sameOrigin(configured, link) {
		err = errors.New("its v3 link is at another address")
	}
	if err != nil {
		// A connect the caller gave up on is not a fault of the URL.
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		example := *configured
		example.Path = trimmed + "/v3"
		example.RawQuery, example.Fragment = "", ""
		return "", &NoKeystoneV3Error{AuthURL: authURL, Example: example.String(), Cause: err}
	}
	return link.String(), nil
}

// NoKeystoneV3Error refuses an Identity URL that offers no v3 at its own
// origin. It is a type so that a caller summarising a failure, such as the
// import's verification (ADR 0114), can name this case.
type NoKeystoneV3Error struct {
	AuthURL, Example string
	Cause            error
}

func (e *NoKeystoneV3Error) Error() string {
	return fmt.Sprintf("the Identity URL %s does not offer Keystone v3 at the same address (%v); give the v3 endpoint, for example %s", e.AuthURL, e.Cause, e.Example)
}

func (e *NoKeystoneV3Error) Unwrap() error { return e.Cause }

// keystoneVersion is one entry of a Keystone version document.
type keystoneVersion struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Links  []struct {
		Rel  string `json:"rel"`
		Href string `json:"href"`
	} `json:"links"`
}

// discoverV3Link reads the version document at authURL, in either shape
// Keystone sends: a list of versions, normally with status 300, or a
// single version when the URL is a v3 root under another path. The errors
// say what was wrong without quoting the document.
func discoverV3Link(ctx context.Context, client *http.Client, authURL string) (*url.URL, error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("the version request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMultipleChoices {
		return nil, fmt.Errorf("the version request answered %d", resp.StatusCode)
	}
	var doc struct {
		Versions struct {
			Values []keystoneVersion `json:"values"`
		} `json:"versions"`
		Version *keystoneVersion `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, discoveryBodyLimit)).Decode(&doc); err != nil {
		return nil, errors.New("its version document could not be read")
	}
	candidates := doc.Versions.Values
	if doc.Version != nil {
		candidates = append(candidates, *doc.Version)
	}
	var links []string
	for _, v := range candidates {
		if !strings.HasPrefix(v.ID, "v3.") || !(strings.EqualFold(v.Status, "stable") || strings.EqualFold(v.Status, "current")) {
			continue
		}
		for _, l := range v.Links {
			if l.Rel == "self" && l.Href != "" && !slices.Contains(links, l.Href) {
				links = append(links, l.Href)
			}
		}
	}
	switch len(links) {
	case 0:
		return nil, errors.New("its version document names no stable v3")
	case 1:
	default:
		// Two different stable v3 links leave the choice to a guess.
		return nil, errors.New("its version document names more than one stable v3")
	}
	link, err := url.Parse(links[0])
	// A link carrying user information would add a credential of the
	// service's choosing to the token request.
	if err != nil || !link.IsAbs() || link.User != nil {
		return nil, errors.New("its v3 link could not be read")
	}
	return link, nil
}

// sameOrigin reports whether two URLs share scheme, host and port, with an
// absent port read as the scheme's default.
func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if strings.EqualFold(u.Scheme, "https") {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

// newDiscoveryClient is the client the version request uses: the session's
// own transport, so the bookmark's TLS rules apply, and no redirect
// followed, so the document comes from the configured origin.
func newDiscoveryClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirect refused")
		},
	}
}
