package swiftcontrol

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateEndpointURL applies the same trust policy to saved endpoints
// and endpoints received from authentication, before any secret travels.
func ValidateEndpointURL(raw string, mode TLSMode) error {
	if strings.ContainsFunc(raw, func(c rune) bool { return c < 0x21 || c == 0x7f }) {
		return fmt.Errorf("endpoint contains whitespace or control characters")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("endpoint is not a valid URL")
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("endpoint must use http or https")
	case u.User != nil:
		return fmt.Errorf("endpoint must not embed credentials")
	case u.Fragment != "" || u.RawQuery != "" || u.ForceQuery:
		return fmt.Errorf("endpoint must not carry a query or fragment")
	case u.Hostname() == "":
		return fmt.Errorf("endpoint has no hostname")
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	// A hostname's current DNS answer is not authority for future requests.
	// Only literal private addresses qualify for the lab HTTP opt-in.
	if ip == nil || !ip.IsPrivate() {
		return fmt.Errorf("plaintext HTTP to a public endpoint is refused; use HTTPS")
	}
	if mode != TLSModeInsecure {
		return fmt.Errorf("private-network HTTP sends credentials in cleartext; select insecure TLS mode to opt in")
	}
	return nil
}

// ValidateStorageURL also prevents an HTTPS authentication exchange from
// handing its token to a plaintext endpoint, including an explicit override.
func ValidateStorageURL(authURL, storage string, mode TLSMode) error {
	if err := ValidateEndpointURL(storage, mode); err != nil {
		return fmt.Errorf("storage URL: %w", err)
	}
	au, err := url.Parse(authURL)
	if err != nil {
		return fmt.Errorf("auth URL is not valid")
	}
	su, _ := url.Parse(storage)
	if au.Scheme == "https" && su.Scheme == "http" {
		return fmt.Errorf("HTTPS authentication cannot use an HTTP storage URL; the token would travel in cleartext")
	}
	return nil
}
