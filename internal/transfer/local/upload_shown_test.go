package local

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// ADR 0085's acknowledged baseline comes from the HEADs this reading already
// takes to say what backs a replaced name. They were previously read and thrown
// away apart from counting fragments, so keeping them costs no request — which
// is what PR 218 decided after measuring that the preflight alone reads nothing
// authoritative for most selections.
// dropHeadHeader answers HEADs of one name without a chosen response header, so
// an incomplete observation can be exercised. The adapter accepts such a HEAD:
// a missing entity tag arrives as an empty one, and a missing length arrives as
// a zero that is indistinguishable from a genuinely empty object.
func dropHeadHeader(t *testing.T, mock http.Handler, escapedPath, drop string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && r.URL.EscapedPath() == escapedPath {
			mock.ServeHTTP(&droppingWriter{ResponseWriter: w, drop: drop}, r)
			return
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

type droppingWriter struct {
	http.ResponseWriter
	drop string
}

func (d *droppingWriter) WriteHeader(code int) {
	d.ResponseWriter.Header().Del(d.drop)
	d.ResponseWriter.WriteHeader(code)
}

// ADR 0085's map keeps a contract: a name is identified, or it is absent. A
// successful HEAD is not enough for the first, which db:830 found by reading the
// adapter rather than this code — it accepts a HEAD with no entity tag and one
// with no length, and the second arrives as a zero indistinguishable from a
// genuinely empty object. An incomplete observation must stay absent, and a real
// empty object must still be identified.
func TestFragmentPlanKeepsOnlyACompleteIdentity(t *testing.T) {
	for _, c := range []struct {
		name       string
		drop       string
		content    string
		identified bool
	}{
		{name: "no entity tag", drop: "Etag", content: "some bytes", identified: false},
		{name: "no stated length", drop: "Content-Length", content: "some bytes", identified: false},
		{name: "a genuinely empty object, length stated as zero", content: "", identified: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("media", "standard")
			seed(mock, "media", "docs/subject", c.content)
			var server *httptest.Server
			if c.drop != "" {
				server = dropHeadHeader(t, mock, "/v1/AUTH_test/media/docs/subject", c.drop)
			} else {
				server = httptest.NewServer(mock)
				t.Cleanup(server.Close)
			}
			s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
			if err != nil {
				t.Fatal(err)
			}
			// The fixture has to actually produce the incomplete read, or the
			// case proves nothing about the code under test.
			detail, err := s.ObjectDetail(t.Context(), "media", "docs/subject")
			if err != nil {
				t.Fatalf("the fixture made the name unreadable rather than incompletely read: %v", err)
			}
			if c.drop == "Etag" && strings.Trim(detail.ETag, `"`) != "" {
				t.Fatalf("the fixture did not remove the entity tag: %+v", detail.ETag)
			}
			if c.drop == "Content-Length" {
				if _, stated := detail.RawHeaders["Content-Length"]; stated {
					t.Fatal("the fixture did not remove the stated length")
				}
			}

			reading := FragmentPlan(t.Context(), s, "media", []string{"docs/subject"})
			shown, identified := reading.Shown["docs/subject"]
			if identified != c.identified {
				t.Fatalf("identified = %v, want %v (kept %+v)", identified, c.identified, shown)
			}
			if c.identified && (shown.ETag == "" || shown.Length != 0) {
				t.Errorf("the empty object was not identified as itself: %+v", shown)
			}
		})
	}
}

func TestFragmentPlanKeepsWhatItReadAtEachName(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	mock.AddContainer("media_segments", "standard")
	// A plain object: this reading skips it for FRAGMENT purposes, after it has
	// already read it, so its identity must still be kept.
	seed(mock, "media", "docs/plain", "the bytes already there")
	// A dynamic manifest, whose listing row would be zero-byte with the
	// manifest object's own hash — which is why the baseline is HEAD-grade.
	seed(mock, "media_segments", "big/0001", "a segment")
	mock.PutObject("media", "docs/manifest", swifttest.Object{
		Bytes: 9, Manifest: "media_segments/big/"})
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}

	reading := FragmentPlan(t.Context(), s, "media", []string{"docs/plain", "docs/manifest", "docs/absent"})

	// The expectation is an independent HEAD of the same name rather than a
	// literal written here: what was kept has to equal what the store reports,
	// and a value invented by the test would prove only that two of my own
	// constants agree.
	for _, name := range []string{"docs/plain", "docs/manifest"} {
		shown, ok := reading.Shown[name]
		if !ok {
			t.Fatalf("%s was read and then not kept", name)
		}
		detail, err := s.ObjectDetail(t.Context(), "media", name)
		if err != nil {
			t.Fatal(err)
		}
		want := ShownObject{Class: string(detail.Class), Length: detail.ContentLength,
			ETag: strings.Trim(detail.ETag, `"`)}
		if shown != want {
			t.Errorf("%s was kept as %+v, want what its HEAD reports %+v", name, shown, want)
		}
		if shown.ETag == "" || shown.Class == "" {
			t.Errorf("%s was kept without an identity: %+v", name, shown)
		}
	}
	// And the two are genuinely different shapes, so the pair is not one case
	// twice: a plain object and a dynamic manifest, the manifest's own entity
	// tag being an aggregate over its segments rather than a content digest.
	if reading.Shown["docs/plain"].Class == reading.Shown["docs/manifest"].Class {
		t.Fatalf("the fixture no longer distinguishes a plain object from a manifest: %+v", reading.Shown)
	}
	// A name this reading could not identify is ABSENT rather than present and
	// empty. ADR 0085 makes that absence a refusal to overwrite, so it has to be
	// distinguishable from an identity that happens to be zero.
	if shown, ok := reading.Shown["docs/absent"]; ok {
		t.Errorf("a name that could not be read was kept anyway: %+v", shown)
	}
	if reading.Unknown == "" {
		t.Error("a name that could not be read left the reading claiming it knew everything")
	}
}
