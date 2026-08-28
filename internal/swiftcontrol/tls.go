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

// transport builds the http.RoundTripper for this policy; nil means the
// wire library's default (system verification).
func (t TLSConfig) transport() (http.RoundTripper, error) {
	switch t.Mode {
	case TLSModeSystem, "":
		return nil, nil
	case TLSModeInsecure:
		return &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}, nil
	case TLSModeCustomCA:
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(t.CACertPEM) {
			return nil, fmt.Errorf("custom CA: no certificates found in PEM data")
		}
		return &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		}, nil
	default:
		return nil, fmt.Errorf("unknown TLS mode %q", t.Mode)
	}
}
