package bookmarks

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
)

var beginMarker = []byte("-----BEGIN ")

// parseCertificates decodes pem and re-encodes its bare, header-free, valid
// X.509 certificate blocks in order, dropping anything else. It is the
// permissive reading: a block that is not such a certificate, and any material
// pem.Decode skips between blocks, are simply left out. A backup's export and
// a restore's trust comparison both use it so they agree on the certificate
// set however the stored bundle happened to be encoded.
func parseCertificates(pemBytes []byte) []byte {
	var canonical []byte
	rest := pemBytes
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remaining
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			continue
		}
		canonical = append(canonical, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})...)
	}
	return canonical
}

// CertificatesOnly returns pem reduced to its valid certificate blocks in a
// canonical, header-free form, dropping a private key or any other block a
// stored CA bundle might hold. A backup uses it so trust material travels and
// nothing else does, and a restore uses it to compare trust sets consistently
// however the stored bundle happened to be encoded.
func CertificatesOnly(pemBytes []byte) []byte {
	return parseCertificates(pemBytes)
}

// CanonicalCACertificates validates a CA bundle strictly and returns its
// certificates in the same canonical form. Unlike CertificatesOnly it refuses
// the whole bundle if it holds anything but bare certificates — a private key,
// a header-bearing block, a malformed, truncated or invalid-base64
// certificate, or any non-whitespace material before, between or after the
// blocks — so a restore cannot store non-trust material or silently drop part
// of a hand-edited backup.
func CanonicalCACertificates(pemBytes []byte) ([]byte, error) {
	malformed := errors.New("CA bundle must contain only certificates")
	var canonical []byte
	rest := pemBytes
	for {
		rest = bytes.TrimLeft(rest, " \t\r\n")
		if len(rest) == 0 {
			break
		}
		// pem.Decode skips any non-PEM material, including a malformed block,
		// to reach the next valid one. Two checks stop it hiding a problem:
		// the remaining input must begin exactly at a block, so nothing is
		// skipped ahead of it, and the span it consumed must hold exactly one
		// BEGIN line, so no block was skipped within it. A base64 body cannot
		// contain the marker, so counting it is reliable.
		if !bytes.HasPrefix(rest, beginMarker) {
			return nil, malformed
		}
		block, remaining := pem.Decode(rest)
		if block == nil {
			return nil, malformed
		}
		consumed := rest[:len(rest)-len(remaining)]
		if bytes.Count(consumed, beginMarker) != 1 {
			return nil, malformed
		}
		rest = remaining
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, malformed
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, malformed
		}
		canonical = append(canonical, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})...)
	}
	if len(canonical) == 0 {
		return nil, errors.New("CA bundle has no usable certificate")
	}
	return canonical, nil
}
