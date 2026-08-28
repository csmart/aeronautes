package swiftcontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAccountPortrait(t *testing.T) {
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	mux.HandleFunc("/v1/AUTH_acct", func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Account-Container-Count", "3")
		h.Set("X-Account-Object-Count", "42")
		h.Set("X-Account-Bytes-Used", "1024")
		h.Set("X-Account-Meta-Owner", "alice")
		h.Set("X-Account-Meta-Quota-Bytes", "2048")
		h.Set("X-Account-Meta-Temp-URL-Key", "sekrit-tempurl-1")
		h.Set("X-Account-Meta-Temp-URL-Key-2", "sekrit-tempurl-2")
		h.Set("X-Account-Storage-Policy-Gold-Fast-Bytes-Used", "700")
		h.Set("X-Account-Storage-Policy-Gold-Fast-Object-Count", "7")
		h.Set("X-Trans-Id", "txportrait1")
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	auth.serverURL = srv.URL

	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.AccountPortrait(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if p.Containers != 3 || p.Objects != 42 || p.BytesUsed != 1024 {
		t.Errorf("counts = %d/%d/%d, want 3/42/1024", p.Containers, p.Objects, p.BytesUsed)
	}
	if p.QuotaBytes != 2048 {
		t.Errorf("QuotaBytes = %d, want 2048", p.QuotaBytes)
	}
	if got := p.Meta["Owner"]; got != "alice" {
		t.Errorf("Meta[Owner] = %q, want alice", got)
	}
	if _, present := p.Meta["Quota-Bytes"]; present {
		t.Error("quota must not appear as plain user metadata")
	}
	if len(p.TempURLKeySlots) != 2 ||
		p.TempURLKeySlots[0] != "Temp-Url-Key" || p.TempURLKeySlots[1] != "Temp-Url-Key-2" {
		t.Errorf("TempURLKeySlots = %v, want the two slots by name", p.TempURLKeySlots)
	}
	u, ok := p.PolicyUsage["gold-fast"]
	if !ok || u.BytesUsed != 700 || u.ObjectCount != 7 {
		t.Errorf("PolicyUsage[gold-fast] = %+v (ok=%v), want 700 bytes / 7 objects", u, ok)
	}
	if p.TransID != "txportrait1" {
		t.Errorf("TransID = %q, want txportrait1", p.TransID)
	}

	// The secret values must not appear anywhere in the portrait — not in
	// Meta, not in the raw view (§22, K-SEC-006).
	for name, v := range p.RawHeaders {
		if strings.Contains(v, "sekrit-tempurl") {
			t.Errorf("RawHeaders[%s] leaks a TempURL secret", name)
		}
	}
	for name, v := range p.Meta {
		if strings.Contains(v, "sekrit-tempurl") {
			t.Errorf("Meta[%s] leaks a TempURL secret", name)
		}
	}
	if p.RawHeaders["X-Account-Meta-Temp-Url-Key"] != redactedValue {
		t.Errorf("raw view should show the redaction placeholder, got %q", p.RawHeaders["X-Account-Meta-Temp-Url-Key"])
	}
}

// TestBuildAccountPortraitCaseInsensitive feeds header keys in hostile
// casing straight to the builder — the categoriser must not depend on
// canonical casing arriving on the wire.
func TestBuildAccountPortraitCaseInsensitive(t *testing.T) {
	p := buildAccountPortrait(canonicalHeaders(map[string]string{
		"x-account-meta-owner":        "alice",
		"X-ACCOUNT-META-TEMP-URL-KEY": "hunter2",
		"x-account-meta-quota-bytes":  "99",
	}), time.Now())

	if p.Meta["Owner"] != "alice" {
		t.Errorf("Meta = %v, want Owner=alice from lowercase wire form", p.Meta)
	}
	if len(p.TempURLKeySlots) != 1 {
		t.Errorf("TempURLKeySlots = %v, want one slot from uppercase wire form", p.TempURLKeySlots)
	}
	if p.QuotaBytes != 99 {
		t.Errorf("QuotaBytes = %d, want 99", p.QuotaBytes)
	}
}

func TestEditableFingerprint(t *testing.T) {
	a := editableFingerprint(map[string]string{"X-Account-Meta-A": "1", "X-Account-Meta-B": "2"})
	b := editableFingerprint(map[string]string{"X-Account-Meta-B": "2", "X-Account-Meta-A": "1"})
	if a != b {
		t.Error("fingerprint must be order-independent")
	}
	c := editableFingerprint(map[string]string{"X-Account-Meta-A": "1", "X-Account-Meta-B": "changed"})
	if a == c {
		t.Error("fingerprint must change when a value changes")
	}
	d := editableFingerprint(map[string]string{"x-account-meta-a": "1", "X-Account-Meta-B": "2"})
	if a != d {
		t.Error("fingerprint must be key-case-insensitive")
	}
}
