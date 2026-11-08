package swiftcontrol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// newObjectHeadServer serves HEADs for a handful of canned objects under
// one container, plus auth.
func newObjectHeadServer(t *testing.T) *Session {
	t.Helper()
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)

	serve := func(path string, set func(h http.Header)) {
		mux.HandleFunc("/v1/AUTH_acct/media/"+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead && r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			set(w.Header())
			w.WriteHeader(http.StatusOK)
		})
	}

	serve("plain.txt", func(h http.Header) {
		h.Set("Content-Length", "12")
		h.Set("Content-Type", "text/plain")
		h.Set("Etag", "5eb63bbbe01eeed093cb22bb8f5acdc3")
		h.Set("Last-Modified", "Wed, 12 Aug 2026 10:21:00 GMT")
		h.Set("X-Object-Meta-Shoot", "wedding")
		h.Set("x-object-meta-lower", "case-insensitive")
		h.Set("X-Delete-At", "1786000000")
		h.Set("X-Trans-Id", "txheadplain")
	})
	serve("big.mp4", func(h http.Header) {
		h.Set("Content-Length", "3000000000")
		h.Set("Content-Type", "video/mp4")
		h.Set("Etag", `"9f2b8c6e4d2a1f2b7e9d3c8a4b1f0e21"`)
		h.Set("Last-Modified", "Wed, 12 Aug 2026 10:24:00 GMT")
		h.Set("X-Object-Manifest", "media_segments/big-video/")
	})
	serve("archive.tar", func(h http.Header) {
		h.Set("Content-Length", "3100000000")
		h.Set("Content-Type", "application/x-tar")
		h.Set("Etag", `"0d5cb1cbf5ec1e5c6f6cbba6c92b02d4"`)
		h.Set("Last-Modified", "Wed, 12 Aug 2026 10:25:00 GMT")
		h.Set("X-Static-Large-Object", "True")
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

func TestObjectDetailPlain(t *testing.T) {
	s := newObjectHeadServer(t)
	d, err := s.ObjectDetail(context.Background(), "media", "plain.txt")
	if err != nil {
		t.Fatal(err)
	}

	if d.Class != ClassPlain {
		t.Errorf("Class = %q, want plain", d.Class)
	}
	if d.Name != "plain.txt" || d.ContentLength != 12 || d.ContentType != "text/plain" {
		t.Errorf("identity fields wrong: %+v", d)
	}
	if d.Meta["Shoot"] != "wedding" || d.Meta["Lower"] != "case-insensitive" {
		t.Errorf("Meta = %v; categorisation must be case-insensitive", d.Meta)
	}
	want := time.Unix(1786000000, 0).UTC()
	if !d.DeleteAt.Equal(want) {
		t.Errorf("DeleteAt = %v, want %v", d.DeleteAt, want)
	}
	if d.TransID != "txheadplain" {
		t.Errorf("TransID = %q", d.TransID)
	}
	if d.EditableFingerprint == "" {
		t.Error("EditableFingerprint must be set")
	}
	if d.RawHeaders["X-Object-Meta-Shoot"] != "wedding" {
		t.Errorf("RawHeaders missing meta: %v", d.RawHeaders)
	}
}

func TestObjectDetailDLO(t *testing.T) {
	s := newObjectHeadServer(t)
	d, err := s.ObjectDetail(context.Background(), "media", "big.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if d.Class != ClassDLO {
		t.Errorf("Class = %q, want dlo", d.Class)
	}
	if d.Manifest != "media_segments/big-video/" {
		t.Errorf("Manifest = %q", d.Manifest)
	}
	// HEAD of a DLO reports the assembled logical size.
	if d.ContentLength != 3000000000 {
		t.Errorf("ContentLength = %d, want logical size", d.ContentLength)
	}
}

func TestObjectDetailSLO(t *testing.T) {
	s := newObjectHeadServer(t)
	d, err := s.ObjectDetail(context.Background(), "media", "archive.tar")
	if err != nil {
		t.Fatal(err)
	}
	if d.Class != ClassSLO {
		t.Errorf("Class = %q, want slo", d.Class)
	}
	if d.Manifest != "" {
		t.Errorf("Manifest = %q, must be empty for SLO", d.Manifest)
	}
}

// A failed HEAD keeps the transaction id the store stamped on its reply,
// as the account and container reads already do, so a caller recording
// the failure can quote it.
func TestObjectDetailFailureCarriesTransID(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	s := mustSession(t, srv.URL)
	mock.PutObject("media", "plain.txt", swifttest.Object{Bytes: 5, ContentType: "text/plain"})
	mock.FailNextObjectHEADs(1)
	_, err := s.ObjectDetail(context.Background(), "media", "plain.txt")
	var se *SwiftError
	if !errors.As(err, &se) || se.TransID != "txswifttest-object-head-fault" {
		t.Fatalf("err = %v, want a SwiftError carrying the failed HEAD's trans id", err)
	}
}

// A reply carrying both manifest markers is an SLO whatever order the
// headers arrive in: the store stamps that marker itself.
func TestBuildObjectDetailClassIsDeterministic(t *testing.T) {
	d := buildObjectDetail(canonicalHeaders(map[string]string{
		"X-Object-Manifest":     "media_segments/big-video/",
		"X-Static-Large-Object": "True",
	}), time.Now())
	if d.Class != ClassSLO {
		t.Errorf("Class = %q, want slo", d.Class)
	}
}

func TestObjectDetailNotFound(t *testing.T) {
	s := newObjectHeadServer(t)
	_, err := s.ObjectDetail(context.Background(), "media", "missing.txt")
	var se *SwiftError
	if !errors.As(err, &se) || se.Kind != KindNotFound {
		t.Fatalf("err = %v, want SwiftError not_found", err)
	}
}

func TestBuildObjectDetailFingerprintCoversEditable(t *testing.T) {
	base := map[string]string{
		"Content-Type":       "text/plain",
		"X-Object-Meta-Team": "ops",
	}
	a := buildObjectDetail(canonicalHeaders(base), time.Now())
	// Same editable content, different key casing: same fingerprint.
	b := buildObjectDetail(canonicalHeaders(map[string]string{
		"content-type":       "text/plain",
		"X-OBJECT-META-TEAM": "ops",
	}), time.Now())
	if a.EditableFingerprint != b.EditableFingerprint {
		t.Error("fingerprint must be case-insensitive")
	}
	// Changing a non-editable header must not move the fingerprint.
	c := buildObjectDetail(canonicalHeaders(map[string]string{
		"Content-Type":       "text/plain",
		"X-Object-Meta-Team": "ops",
		"X-Trans-Id":         "txwhatever",
	}), time.Now())
	if a.EditableFingerprint != c.EditableFingerprint {
		t.Error("non-editable headers must not affect the fingerprint")
	}
	// Changing editable content must.
	d := buildObjectDetail(canonicalHeaders(map[string]string{
		"Content-Type":       "text/html",
		"X-Object-Meta-Team": "ops",
	}), time.Now())
	if a.EditableFingerprint == d.EditableFingerprint {
		t.Error("editable changes must move the fingerprint")
	}
}
