package swiftcontrol

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
		"empty object name":         `[{"name": "/c/", "bytes": 1}]`,
	}
	for label, manifest := range cases {
		s := newSLOManifestSession(t, manifest)
		if _, err := s.SLOManifest(context.Background(), "media", "archive.tar"); err == nil {
			t.Errorf("%s: parse must fail loudly, never drop or invent entries", label)
		}
	}
}

// SLOManifestDigests is the bounded input a content comparison rests on,
// so every shape it cannot vouch for has to refuse rather than be
// reduced to something it is not.
func TestSLOManifestDigestsBoundedInput(t *testing.T) {
	const good = `[{"name": "/parts/a", "hash": "d41d8cd98f00b204e9800998ecf8427e", "bytes": 4},
	               {"name": "/parts/b", "hash": "0cc175b9c0f1b6a831c399e269772661", "bytes": 6}]`
	t.Run("whole_segments_admitted", func(t *testing.T) {
		s := newSLOManifestSession(t, good)
		digests, err := s.SLOManifestDigests(context.Background(), "media", "archive.tar", 10, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if len(digests) != 2 || digests[0].Size != 4 || digests[1].ETag != "0cc175b9c0f1b6a831c399e269772661" {
			t.Fatalf("digests = %+v", digests)
		}
	})
	for label, manifest := range map[string]string{
		"ranged_entry":       `[{"name": "/parts/a", "hash": "d41d8cd98f00b204e9800998ecf8427e", "bytes": 4, "range": "0-3"}]`,
		"nested_manifest":    `[{"name": "/parts/a", "hash": "d41d8cd98f00b204e9800998ecf8427e", "bytes": 4, "sub_slo": true}]`,
		"unrecognised_entry": `[{"mystery": "entry-from-the-future"}]`,
		"no_recorded_hash":   `[{"name": "/parts/a", "bytes": 4}]`,
		"not_a_list":         `{"name": "/parts/a"}`,
		"truncated_list":     `[{"name": "/parts/a", "hash": "d41d8cd98f00b204e9800998ecf8427e", "bytes": 4}`,
		"trailing_content":   good + ` []`,
	} {
		t.Run(label, func(t *testing.T) {
			s := newSLOManifestSession(t, manifest)
			if _, err := s.SLOManifestDigests(context.Background(), "media", "archive.tar", 10, 1<<20); err == nil {
				t.Fatal("a shape the reader cannot vouch for was admitted")
			}
		})
	}
	t.Run("inline_data_is_read_not_refused", func(t *testing.T) {
		// An inline entry carries its own bytes, so its run is read from
		// them rather than the source class being narrowed to exclude it.
		s := newSLOManifestSession(t, `[{"data": "aGVsbG8="}]`)
		digests, err := s.SLOManifestDigests(context.Background(), "media", "archive.tar", 10, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		sum := md5.Sum([]byte("hello"))
		if len(digests) != 1 || digests[0].Size != 5 || digests[0].ETag != hex.EncodeToString(sum[:]) {
			t.Fatalf("inline entry = %+v, want the length and MD5 of its own bytes", digests)
		}
	})
	t.Run("entry_ceiling", func(t *testing.T) {
		s := newSLOManifestSession(t, good)
		if _, err := s.SLOManifestDigests(context.Background(), "media", "archive.tar", 1, 1<<20); err == nil {
			t.Fatal("a manifest past the entry ceiling was truncated instead of refused")
		}
	})
	t.Run("byte_ceiling", func(t *testing.T) {
		s := newSLOManifestSession(t, good)
		if _, err := s.SLOManifestDigests(context.Background(), "media", "archive.tar", 10, 8); err == nil {
			t.Fatal("a manifest past the byte ceiling was truncated instead of refused")
		}
	})
	t.Run("display_getter_is_unchanged", func(t *testing.T) {
		// The exact model still keeps every entry shape in place; only the
		// safety reader narrows what it will vouch for.
		s := newSLOManifestSession(t, `[{"data": "aGVsbG8="}]`)
		m, err := s.SLOManifest(context.Background(), "media", "archive.tar")
		if err != nil || len(m.Entries) != 1 {
			t.Fatalf("display getter changed: %v %+v", err, m)
		}
		if _, ok := m.Entries[0].(InlineDataSegment); !ok {
			t.Fatalf("display getter lost the inline entry: %#v", m.Entries[0])
		}
	})
}

// consumptionReader records exactly how many bytes the parser pulled, so
// the budget can be shown to bound the read rather than describe it.
type consumptionReader struct {
	inner io.Reader
	count int64
}

func (c *consumptionReader) Read(p []byte) (int, error) {
	n, err := c.inner.Read(p)
	c.count += int64(n)
	return n, err
}

func TestSLOManifestDigestsBudgetBoundsTheRead(t *testing.T) {
	// db:471 finding 1: counting the bytes after the decoder has already
	// buffered them is not a bound. Each of these bodies is far larger
	// than the budget, and the parser must stop inside it.
	const budget = 256
	huge := strings.Repeat("A", 1<<20)
	for label, body := range map[string]string{
		"one_enormous_entry":  `[{"name": "/parts/` + huge + `", "hash": "d41d8cd98f00b204e9800998ecf8427e", "bytes": 4}]`,
		"leading_whitespace":  strings.Repeat(" ", 1<<20) + `[]`,
		"trailing_whitespace": `[]` + strings.Repeat(" ", 1<<20),
		"enormous_entry_list": "[" + strings.Repeat(`{"name": "/p/a", "hash": "d41d8cd98f00b204e9800998ecf8427e", "bytes": 1},`, 1<<14) + "]",
	} {
		t.Run(label, func(t *testing.T) {
			counted := &consumptionReader{inner: strings.NewReader(body)}
			if _, err := parseSLOManifestDigests(counted, 1<<20, budget, "media/archive.tar"); err == nil {
				t.Fatal("an oversized manifest was admitted")
			}
			// One byte past the budget is the overrun probe that tells a
			// body ending exactly at the limit from one byte too many.
			if counted.count > budget+1 {
				t.Fatalf("parser consumed %d bytes against a budget of %d", counted.count, budget)
			}
			t.Logf("%s: refused after %d of %d body bytes", label, counted.count, len(body))
		})
	}
	t.Run("a_manifest_inside_the_budget_still_parses", func(t *testing.T) {
		body := `[{"name": "/parts/a", "hash": "d41d8cd98f00b204e9800998ecf8427e", "bytes": 4}]`
		counted := &consumptionReader{inner: strings.NewReader(body)}
		digests, err := parseSLOManifestDigests(counted, 10, budget, "media/archive.tar")
		if err != nil || len(digests) != 1 {
			t.Fatalf("digests = %+v, err = %v", digests, err)
		}
		if counted.count > budget+1 {
			t.Fatalf("parser consumed %d bytes against a budget of %d", counted.count, budget)
		}
	})
	t.Run("a_body_ending_exactly_at_the_budget_is_admitted", func(t *testing.T) {
		// db:474: the limit is inclusive, so an exact fit is evidence
		// rather than a conservative refusal.
		body := `[{"name": "/parts/a", "hash": "d41d8cd98f00b204e9800998ecf8427e", "bytes": 4}]`
		counted := &consumptionReader{inner: strings.NewReader(body)}
		digests, err := parseSLOManifestDigests(counted, 10, int64(len(body)), "media/archive.tar")
		if err != nil || len(digests) != 1 {
			t.Fatalf("a manifest exactly at the budget was refused: %+v %v", digests, err)
		}
	})
	t.Run("one_byte_over_the_budget_refuses", func(t *testing.T) {
		body := `[{"name": "/parts/a", "hash": "d41d8cd98f00b204e9800998ecf8427e", "bytes": 4}] `
		counted := &consumptionReader{inner: strings.NewReader(body)}
		if _, err := parseSLOManifestDigests(counted, 10, int64(len(body))-1, "media/archive.tar"); err == nil {
			t.Fatal("a manifest one byte past the budget was admitted")
		}
	})
}

// joinedEOFReader ends with an EOF carrying another failure alongside it,
// which is a body that did not finish rather than one that ended.
type joinedEOFReader struct {
	inner io.Reader
	fault error
}

func (j *joinedEOFReader) Read(p []byte) (int, error) {
	n, err := j.inner.Read(p)
	if err == io.EOF {
		return n, errors.Join(io.EOF, j.fault)
	}
	return n, err
}

func TestSLOManifestDigestsRequiresACleanEnd(t *testing.T) {
	// db:474: errors.Is would accept an EOF joined with a read failure, so
	// a manifest whose body failed on its last read was admitted as
	// evidence. Only a clean end counts.
	body := `[{"name": "/parts/a", "hash": "d41d8cd98f00b204e9800998ecf8427e", "bytes": 4}]`
	fault := errors.New("body failed while ending")
	t.Run("eof_joined_with_a_failure_refuses", func(t *testing.T) {
		reader := &joinedEOFReader{inner: strings.NewReader(body), fault: fault}
		digests, err := parseSLOManifestDigests(reader, 10, 1<<20, "media/archive.tar")
		if err == nil {
			t.Fatalf("a body that failed while ending was admitted with %d runs", len(digests))
		}
		if !errors.Is(err, fault) {
			t.Fatalf("refusal lost the underlying failure: %v", err)
		}
	})
	t.Run("clean_eof_is_admitted", func(t *testing.T) {
		digests, err := parseSLOManifestDigests(strings.NewReader(body), 10, 1<<20, "media/archive.tar")
		if err != nil || len(digests) != 1 {
			t.Fatalf("a clean end was refused: %+v %v", digests, err)
		}
	})
}

// newSLOManifestPartialSession answers the manifest fetch with the given
// status and headers, for the completeness contract.
func newSLOManifestPartialSession(t *testing.T, status int, header, value, body string) *Session {
	t.Helper()
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	mux.HandleFunc("/v1/AUTH_acct/media/archive.tar", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if header != "" {
			w.Header().Set(header, value)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
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

func TestSLOManifestDigestsRequiresACompleteResponse(t *testing.T) {
	// db:471 finding 2: a partial body that happens to parse is not the
	// manifest. Same contract as OpenObjectContent: HTTP 200, no
	// Content-Range.
	const body = `[{"name": "/parts/a", "hash": "d41d8cd98f00b204e9800998ecf8427e", "bytes": 4}]`
	for label, tc := range map[string]struct {
		status        int
		header, value string
	}{
		"partial_content":            {http.StatusPartialContent, "Content-Range", "bytes 0-79/160"},
		"content_range_on_200":       {http.StatusOK, "Content-Range", "bytes 0-79/160"},
		"empty_content_range_on_200": {http.StatusOK, "Content-Range", ""},
	} {
		t.Run(label, func(t *testing.T) {
			s := newSLOManifestPartialSession(t, tc.status, tc.header, tc.value, body)
			if _, err := s.SLOManifestDigests(context.Background(), "media", "archive.tar", 10, 1<<20); err == nil {
				t.Fatal("a partial response was admitted as a complete manifest")
			}
		})
	}
	t.Run("complete_response_is_admitted", func(t *testing.T) {
		s := newSLOManifestPartialSession(t, http.StatusOK, "", "", body)
		digests, err := s.SLOManifestDigests(context.Background(), "media", "archive.tar", 10, 1<<20)
		if err != nil || len(digests) != 1 {
			t.Fatalf("digests = %+v, err = %v", digests, err)
		}
	})
}
