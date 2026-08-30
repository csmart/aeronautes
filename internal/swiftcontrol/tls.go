package swiftcontrol

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
)

// TLSMode is a bookmark's TLS policy (§12.3). Insecure is per-bookmark,
// explicit, and visually flagged by the UI (K-SEC-005); this layer only
// implements the transport for it.
type TLSMode string

const (
	// TLSModeSystem verifies against the system trust store (default).
	TLSModeSystem TLSMode = "system"
	// TLSModeCustomCA verifies against exactly the configured CA (§12.2).
	TLSModeCustomCA TLSMode = "custom_ca"
	// TLSModeInsecure disables certificate verification.
	TLSModeInsecure TLSMode = "insecure"
)

// TLSConfig is the TLS half of a bookmark.
type TLSConfig struct {
	Mode TLSMode
	// CACertPEM is the PEM-encoded CA bundle for TLSModeCustomCA.
	CACertPEM []byte
}

// transport builds the http.RoundTripper for this policy. Every mode —
// system trust included — rides the redirect-refusing wrapper: the wire
// library's own client follows redirects (its CheckRedirect is unset),
// and Go does not hop-restrict X-Auth-Key/X-Auth-Token, so a 302 from
// an auth or storage endpoint would resend the credentials wherever it
// pointed (review G22). Swift never legitimately redirects these calls.
func (t TLSConfig) transport() (http.RoundTripper, error) {
	var inner http.RoundTripper
	switch t.Mode {
	case TLSModeSystem, "":
		inner = http.DefaultTransport
	case TLSModeInsecure:
		inner = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	case TLSModeCustomCA:
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(t.CACertPEM) {
			return nil, fmt.Errorf("custom CA: no certificates found in PEM data")
		}
		inner = &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		}
	default:
		return nil, fmt.Errorf("unknown TLS mode %q", t.Mode)
	}
	return redirectRefusingTransport{inner: inner}, nil
}

// redirectRefusingTransport strips the Location header off any 3xx so
// the client above it CANNOT follow — the token never travels to a
// redirect target, whichever client wraps this transport.
type redirectRefusingTransport struct {
	inner http.RoundTripper
}

func (t redirectRefusingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err == nil && resp != nil && resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" {
		resp.Header.Del("Location")
		resp.Header.Set("X-Aeronautes-Redirect-Refused", "the endpoint answered with a redirect; native Swift calls never follow one")
	}
	return resp, err
}
