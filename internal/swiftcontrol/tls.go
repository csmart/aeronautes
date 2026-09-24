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
// Every mode also caps the connections to one host at maxConnsPerHost
// (ADR 0074 decision 4): system trust rides a copy of the default
// transport rather than the shared one, so the cap is this session's;
// the custom-CA and insecure transports keep their own settings. A
// process whose default transport is not the standard one (tests
// substitute a round tripper to observe requests) keeps using it as it
// is, uncapped, since only the standard transport carries the cap.
func (t TLSConfig) transport(maxConnsPerHost int) (http.RoundTripper, error) {
	config, err := t.ClientConfig()
	if err != nil {
		return nil, err
	}
	var inner *http.Transport
	if config == nil {
		standard, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return redirectRefusingTransport{inner: http.DefaultTransport}, nil
		}
		inner = standard.Clone()
	} else {
		inner = &http.Transport{TLSClientConfig: config}
	}
	inner.MaxConnsPerHost = maxConnsPerHost
	return redirectRefusingTransport{inner: inner}, nil
}

// ClientConfig keeps native requests and the child gateway on the same
// trust policy while allowing each to own its connection lifetime.
func (t TLSConfig) ClientConfig() (*tls.Config, error) {
	switch t.Mode {
	case TLSModeSystem, "":
		return nil, nil
	case TLSModeInsecure:
		return &tls.Config{InsecureSkipVerify: true}, nil
	case TLSModeCustomCA:
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(t.CACertPEM) {
			return nil, fmt.Errorf("custom CA: no certificates found in PEM data")
		}
		return &tls.Config{RootCAs: pool}, nil
	default:
		return nil, fmt.Errorf("unknown TLS mode %q", t.Mode)
	}
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
