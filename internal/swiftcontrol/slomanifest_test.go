package swiftcontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newSLOManifestSession(t *testing.T, manifestJSON string) *Session {
	t.Helper()
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	mux.HandleFunc("/v1/AUTH_acct/media/archive.tar", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("multipart-manifest") != "get" {
			t.Errorf("manifest fetch missing multipart-manifest=get: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(manifestJSON))
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

func TestSLOManifestExactModel(t *testing.T) {
	// One of everything, deliberately interleaved: a plain segment, a
	// ranged one, an inline data entry, a sub-SLO, and a future entry
	// shape — order must survive exactly (§23).
	s := newSLOManifestSession(t, `[
		{"name": "/slo-parts/backup/part-000001", "hash": "aaa111", "bytes": 500, "content_type": "application/octet-stream"},
		{"name": "/slo-parts/backup/part-000002", "hash": "bbb222", "bytes": 100, "range": "10-109", "future_hint": "keep-me"},
		{"data": "aGVsbG8="},
		{"name": "/other/nested-manifest", "hash": "ccc333", "bytes": 900, "sub_slo": true},
		{"mystery": "entry-from-the-future"}
	]`)

	m, err := s.SLOManifest(context.Background(), "media", "archive.tar")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 5 {
		t.Fatalf("entries = %d, want 5", len(m.Entries))
	}

	plain, ok := m.Entries[0].(ObjectSegment)
	if !ok || plain.Path != (ObjectRef{Container: "slo-parts", Object: "backup/part-000001"}) ||
		plain.ETag == nil || *plain.ETag != "aaa111" || plain.Size != 500 ||
		plain.Range != nil || plain.NestedSLO {
		t.Errorf("entry 0 = %#v", m.Entries[0])
	}

	ranged, ok := m.Entries[1].(ObjectSegment)
	if !ok || ranged.Range == nil || ranged.Range.Start != 10 || ranged.Range.End != 109 {
		t.Errorf("entry 1 = %#v, want range 10-109", m.Entries[1])
	}
	if ranged.RawFields["future_hint"] != "keep-me" {
		t.Error("unknown field on a recognised entry must survive in RawFields")
	}

	inline, ok := m.Entries[2].(InlineDataSegment)
	if !ok || string(inline.Data) != "hello" {
		t.Errorf("entry 2 = %#v, want decoded inline data", m.Entries[2])
	}

	nested, ok := m.Entries[3].(ObjectSegment)
	if !ok || !nested.NestedSLO {
		t.Errorf("entry 3 = %#v, want sub-SLO", m.Entries[3])
	}

	raw, ok := m.Entries[4].(RawEntry)
	if !ok || raw.Fields["mystery"] != "entry-from-the-future" {
		t.Errorf("entry 4 = %#v, want verbatim RawEntry", m.Entries[4])
	}
}

func TestSLOManifestBadShapes(t *testing.T) {
	cases := map[string]string{
		"no container/object split": `[{"name": "/justonepart", "bytes": 1}]`,
		"bad range":                 `[{"name": "/c/o", "range": "10-"}]`,
		"bad inline base64":         `[{"data": "%%%"}]`,
	}
	for label, manifest := range cases {
		s := newSLOManifestSession(t, manifest)
		if _, err := s.SLOManifest(context.Background(), "media", "archive.tar"); err == nil {
			t.Errorf("%s: parse must fail loudly, never drop or invent entries", label)
		}
	}
}
