package swiftcontrol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newContainerHeadSession(t *testing.T, set func(h http.Header)) *Session {
	t.Helper()
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	mux.HandleFunc("/v1/AUTH_acct/media", func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Container-Object-Count", "7")
		h.Set("X-Container-Bytes-Used", "4096")
		set(h)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	auth.serverURL = srv.URL

	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestContainerDetailFull(t *testing.T) {
	s := newContainerHeadSession(t, func(h http.Header) {
		h.Set("X-Storage-Policy", "gold-fast")
		h.Set("X-Container-Read", ".r:*,.rlistings")
		h.Set("X-Container-Write", "acct:writer")
		h.Set("X-Versions-Location", "media-versions")
		h.Set("X-Container-Meta-Owner", "alice")
		h.Set("x-container-meta-lower", "case-insensitive")
		h.Set("X-Container-Meta-Quota-Bytes", "1000000")
		h.Set("X-Container-Meta-Quota-Count", "500")
		h.Set("X-Container-Meta-Temp-URL-Key", "sekrit-tempurl")
		h.Set("X-Container-Meta-Access-Control-Allow-Origin", "https://app.example")
		h.Set("X-Container-Meta-Web-Index", "index.html")
		h.Set("X-Trans-Id", "txcontainerhead")
	})

	d, err := s.ContainerDetail(context.Background(), "media")
	if err != nil {
		t.Fatal(err)
	}

	if d.Name != "media" || d.ObjectCount != 7 || d.BytesUsed != 4096 {
		t.Errorf("identity = %+v", d)
	}
	if d.StoragePolicy != "gold-fast" {
		t.Errorf("StoragePolicy = %q", d.StoragePolicy)
	}
	if d.ReadACL != ".r:*,.rlistings" || d.WriteACL != "acct:writer" {
		t.Errorf("ACLs = %q / %q", d.ReadACL, d.WriteACL)
	}
	if d.Versioning.Mode != VersioningVersionsLocation || d.Versioning.Archive != "media-versions" {
		t.Errorf("Versioning = %+v", d.Versioning)
	}
	if d.QuotaBytes != 1000000 || d.QuotaCount != 500 {
		t.Errorf("quotas = %d / %d", d.QuotaBytes, d.QuotaCount)
	}
	if !d.CORS || !d.StaticWeb {
		t.Errorf("feature presence: CORS=%v StaticWeb=%v", d.CORS, d.StaticWeb)
	}
	if len(d.TempURLKeySlots) != 1 || d.TempURLKeySlots[0] != "Temp-Url-Key" {
		t.Errorf("TempURLKeySlots = %v", d.TempURLKeySlots)
	}
	if d.Meta["Owner"] != "alice" || d.Meta["Lower"] != "case-insensitive" {
		t.Errorf("Meta = %v", d.Meta)
	}
	for _, special := range []string{"Quota-Bytes", "Quota-Count", "Temp-Url-Key", "Access-Control-Allow-Origin", "Web-Index"} {
		if _, leaked := d.Meta[special]; leaked {
			t.Errorf("special key %s leaked into plain Meta", special)
		}
	}
	if d.TransID != "txcontainerhead" {
		t.Errorf("TransID = %q", d.TransID)
	}
	if got := d.RawHeaders["X-Container-Meta-Temp-Url-Key"]; got != "[redacted]" {
		t.Errorf("TempURL key in raw view = %q, want redacted", got)
	}
	if strings.Contains(strings.Join(mapValues(d.RawHeaders), "\n"), "sekrit-tempurl") {
		t.Error("TempURL secret leaked into the raw header view")
	}
	if d.EditableFingerprint == "" {
		t.Error("EditableFingerprint must be set")
	}
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestContainerDetailVersioningVariants(t *testing.T) {
	off := buildContainerDetail(canonicalHeaders(map[string]string{}), time.Now())
	if off.Versioning.Mode != VersioningOff {
		t.Errorf("no headers → %+v, want off", off.Versioning)
	}

	history := buildContainerDetail(canonicalHeaders(map[string]string{
		"X-History-Location": "media-history",
	}), time.Now())
	if history.Versioning.Mode != VersioningHistoryLocation || history.Versioning.Archive != "media-history" {
		t.Errorf("history = %+v", history.Versioning)
	}

	// Both present (servers reject this, but display must be
	// deterministic): history wins regardless of map order.
	both := buildContainerDetail(canonicalHeaders(map[string]string{
		"X-Versions-Location": "media-versions",
		"X-History-Location":  "media-history",
	}), time.Now())
	if both.Versioning.Mode != VersioningHistoryLocation {
		t.Errorf("both headers = %+v, want history to win", both.Versioning)
	}
}

func TestContainerDetailFingerprint(t *testing.T) {
	base := map[string]string{
		"X-Container-Meta-Team": "ops",
		"X-Container-Read":      ".r:*",
	}
	a := buildContainerDetail(canonicalHeaders(base), time.Now())
	b := buildContainerDetail(canonicalHeaders(map[string]string{
		"x-container-meta-team": "ops",
		"X-CONTAINER-READ":      ".r:*",
	}), time.Now())
	if a.EditableFingerprint != b.EditableFingerprint {
		t.Error("fingerprint must be case-insensitive")
	}
	c := buildContainerDetail(canonicalHeaders(map[string]string{
		"X-Container-Meta-Team": "ops",
		"X-Container-Read":      ".r:*",
		"X-Storage-Policy":      "gold", // not editable
	}), time.Now())
	if a.EditableFingerprint != c.EditableFingerprint {
		t.Error("non-editable headers must not move the fingerprint")
	}
	d := buildContainerDetail(canonicalHeaders(map[string]string{
		"X-Container-Meta-Team": "ops",
		"X-Container-Read":      ".r:*,acct:new",
	}), time.Now())
	if a.EditableFingerprint == d.EditableFingerprint {
		t.Error("an ACL change must move the fingerprint")
	}
}

func TestContainerDetailNotFound(t *testing.T) {
	s := newContainerHeadSession(t, func(http.Header) {})
	_, err := s.ContainerDetail(context.Background(), "missing")
	var se *SwiftError
	if !errors.As(err, &se) || se.Kind != KindNotFound {
		t.Fatalf("err = %v, want not_found", err)
	}
}

func TestVersionsEnabledParses(t *testing.T) {
	d := buildContainerDetail(map[string]string{
		"X-Versions-Enabled": "true",
	}, time.Now())
	if d.Versioning.Mode != VersioningEnabled {
		t.Fatalf("mode = %q, want enabled (object_versioning)", d.Versioning.Mode)
	}
}
