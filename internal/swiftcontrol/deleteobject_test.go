package swiftcontrol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

const plainName = "normal/hello.txt"

func mustErr(_ *ObjectDetail, err error) error { return err }

func countObjects(t *testing.T, s *Session, container string) int {
	t.Helper()
	n := 0
	for entry, err := range s.Objects(context.Background(), container, ObjectListOptions{Deep: true}) {
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := entry.(ObjectEntry); ok {
			n++
		}
	}
	return n
}

// requestsFor are the requests the store received for one object after the
// first skip of its log, as "METHOD query" in order, the query empty for a
// plain request.
func requestsFor(mock *swifttest.Server, skip int, container, name string) []string {
	var seen []string
	for _, line := range mock.RequestLog()[skip:] {
		method, target, _ := strings.Cut(line, " ")
		path, query, _ := strings.Cut(target, "?")
		if strings.HasSuffix(path, "/"+container+"/"+name) {
			seen = append(seen, strings.TrimSpace(method+" "+query))
		}
	}
	return seen
}

// ADR 0122 decision 6: one DELETE, and the store's answer is the result. The
// name is not read before it or after it, and nothing else moves.
func TestDeleteNameSendsOneDeleteAndTakesTheAnswer(t *testing.T) {
	s, mock := newFixtureSessionWithMock(t)
	ctx := context.Background()
	siblings := countObjects(t, s, "fixtures")
	before := len(mock.RequestLog())

	result, err := s.DeleteName(ctx, "fixtures", plainName)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateApplied || result.Gone || result.Status != http.StatusNoContent || len(result.TransIDs) != 1 {
		t.Fatalf("result = %+v, want applied by a 204 with its transaction id", result)
	}
	if got := requestsFor(mock, before, "fixtures", plainName); len(got) != 1 || got[0] != "DELETE" {
		t.Fatalf("the store received %q for the object, want one DELETE and no read", got)
	}
	if !IsNotFound(mustErr(s.ObjectDetail(ctx, "fixtures", plainName))) {
		t.Fatal("the object is still readable after the delete")
	}
	if got := countObjects(t, s, "fixtures"); got != siblings-1 {
		t.Fatalf("fixtures holds %d objects, want %d: a sibling moved", got, siblings-1)
	}
}

// What stands at the name does not matter: another writer's object, written
// after the operator looked, is deleted, and so is a manifest, whose segments
// stay because the DELETE is the plain one.
func TestDeleteNameDeletesWhateverTheNameHolds(t *testing.T) {
	s, mock := newFixtureSessionWithMock(t)
	ctx := context.Background()

	mock.PutObject("fixtures", plainName, swifttest.Object{Bytes: 22, ETag: "0123456789abcdef0123456789abcdef", ContentType: "text/plain"})
	if result, err := s.DeleteName(ctx, "fixtures", plainName); err != nil || result.Outcome != UpdateApplied {
		t.Fatalf("an object replaced since it was shown: result %+v, err %v", result, err)
	}

	segments := countObjects(t, s, "external-dlo-segments")
	before := len(mock.RequestLog())
	if result, err := s.DeleteName(ctx, "fixtures", "external-dlo"); err != nil || result.Outcome != UpdateApplied {
		t.Fatalf("a dynamic manifest: result %+v, err %v", result, err)
	}
	if got := requestsFor(mock, before, "fixtures", "external-dlo"); len(got) != 1 || got[0] != "DELETE" {
		t.Fatalf("the store received %q for the manifest, want one plain DELETE", got)
	}
	if got := countObjects(t, s, "external-dlo-segments"); got != segments {
		t.Fatalf("the segments container holds %d objects, want the %d it held", got, segments)
	}
}

// A name that is already gone is the outcome asked for, not a failure.
func TestDeleteNameTakesNotFoundAsAlreadyGone(t *testing.T) {
	s := newFixtureSession(t)
	result, err := s.DeleteName(context.Background(), "fixtures", "never/was/here")
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateApplied || !result.Gone || result.Status != http.StatusNotFound {
		t.Fatalf("result = %+v, want applied and gone by a 404", result)
	}
}

// No answer, and a server error, are the unknown: the request may have been
// applied. Nothing is read to find out, so a store that goes on serving the
// object, as a lagging replica does, changes nothing either way.
func TestDeleteNameReportsWhatItCannotKnow(t *testing.T) {
	ctx := context.Background()

	t.Run("lost reply", func(t *testing.T) {
		s, mock := newFixtureSessionWithMock(t)
		mock.DropNextObjectDELETE()
		before := len(mock.RequestLog())
		result, err := s.DeleteName(ctx, "fixtures", plainName)
		if !errors.Is(err, ErrUnverifiedDelete) {
			t.Fatalf("err = %v, want unverified", err)
		}
		if result == nil || result.Outcome != UpdateUnverified || result.Status != 0 {
			t.Fatalf("result = %+v, want unverified with no status", result)
		}
		if got := requestsFor(mock, before, "fixtures", plainName); len(got) != 1 {
			t.Fatalf("the store received %q, want the one DELETE and no read after it", got)
		}
	})

	t.Run("server error", func(t *testing.T) {
		s, mock := newFixtureSessionWithMock(t)
		mock.FailDeletes = true
		before := len(mock.RequestLog())
		result, err := s.DeleteName(ctx, "fixtures", plainName)
		if !errors.Is(err, ErrUnverifiedDelete) {
			t.Fatalf("err = %v, want unverified", err)
		}
		if result == nil || result.Outcome != UpdateUnverified || result.Status < 500 {
			t.Fatalf("result = %+v, want unverified with the server's status", result)
		}
		if got := requestsFor(mock, before, "fixtures", plainName); len(got) != 1 {
			t.Fatalf("the store received %q, want the one DELETE and no read after it", got)
		}
	})
}

// A status that is neither success, not found nor a server error is a
// refusal that deleted nothing: forbidden, and a redirect, which the
// session's transport does not follow.
func TestDeleteNameReportsARefusalWithItsStatus(t *testing.T) {
	for name, status := range map[string]int{"forbidden": http.StatusForbidden, "redirect": http.StatusTemporaryRedirect} {
		t.Run(name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.LoadCanonicalFixtures()
			server := gatedServer(t, mock, func(w http.ResponseWriter, r *http.Request, forward func()) {
				if isDelete(r, plainName) {
					w.WriteHeader(status)
					return
				}
				forward()
			})
			s := sessionThrough(t, server)
			ctx := context.Background()
			result, err := s.DeleteName(ctx, "fixtures", plainName)
			var se *SwiftError
			if !errors.As(err, &se) || se.HTTPStatus != status || errors.Is(err, ErrUnverifiedDelete) {
				t.Fatalf("err = %v, want the store's refusal with HTTP %d", err, status)
			}
			if result == nil || result.Outcome != "" || result.Status != status {
				t.Fatalf("result = %+v, want no outcome and the status", result)
			}
			if _, err := s.ObjectDetail(ctx, "fixtures", plainName); err != nil {
				t.Fatalf("the object is gone after a refused DELETE: %v", err)
			}
		})
	}
}

// A token the store refuses deleted nothing, so it is renewed and the DELETE
// sent once more. A second refusal is the answer.
func TestDeleteNameRenewsARefusedTokenOnce(t *testing.T) {
	for name, refusals := range map[string]int{"refused once": 1, "refused twice": 2} {
		t.Run(name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.LoadCanonicalFixtures()
			deletes, auths := 0, 0
			server := gatedServer(t, mock, func(w http.ResponseWriter, r *http.Request, forward func()) {
				switch {
				case r.URL.Path == "/auth/v1.0":
					auths++
				case isDelete(r, plainName):
					deletes++
					if deletes <= refusals {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
				}
				forward()
			})
			s := sessionThrough(t, server)
			ctx := context.Background()
			if _, err := s.Credentials(ctx, 0); err != nil {
				t.Fatal(err)
			}
			signedIn := auths
			result, err := s.DeleteName(ctx, "fixtures", plainName)
			if refusals == 1 {
				if err != nil || result.Outcome != UpdateApplied || deletes != 2 || auths != signedIn+1 {
					t.Fatalf("result %+v, err %v, %d DELETEs, %d sign-ins: want the second DELETE accepted after one sign-in", result, err, deletes, auths-signedIn)
				}
				return
			}
			var se *SwiftError
			if !errors.As(err, &se) || se.Kind != KindUnauthorized || deletes != 2 {
				t.Fatalf("err = %v after %d DELETEs, want the refusal after exactly two", err, deletes)
			}
			if _, err := s.ObjectDetail(ctx, "fixtures", plainName); err != nil {
				t.Fatalf("the object is gone after refused DELETEs: %v", err)
			}
		})
	}
}

// An empty object name is the container's own path: refused before any
// request at the session and at the raw boundary, so no caller can turn an
// object delete into a container delete.
func TestDeleteNameRefusesAnEmptyTargetPart(t *testing.T) {
	s, mock := newFixtureSessionWithMock(t)
	ctx := context.Background()
	before := mock.Requests()
	for name, parts := range map[string][2]string{"empty name": {"fixtures", ""}, "empty container": {"", plainName}} {
		result, err := s.DeleteName(ctx, parts[0], parts[1])
		if err == nil || result != nil {
			t.Fatalf("%s: result = %+v, err = %v, want a refusal with no result", name, result, err)
		}
	}
	credentials, err := s.Credentials(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, err := s.rawDelete(ctx, credentials, "fixtures", ""); err == nil || status != 0 {
		t.Fatalf("raw DELETE of an empty name: status %d, err %v", status, err)
	}
	if got := mock.Requests(); got != before {
		t.Fatalf("%d requests reached the store for empty target parts", got-before)
	}
}

// The literal read is comparable with the inspector's identity and does not
// follow or expand what it finds.
func TestLiteralObjectHeadReportsWhatIsStoredAtTheName(t *testing.T) {
	s, mock := newFixtureSessionWithMock(t)
	ctx := context.Background()

	shown, err := s.ObjectDetail(ctx, "fixtures", plainName)
	if err != nil {
		t.Fatal(err)
	}
	literal, err := s.LiteralObjectHead(ctx, "fixtures", plainName)
	if err != nil {
		t.Fatal(err)
	}
	if literal.ContentFingerprint() != shown.ContentFingerprint() || literal.ContentLength != shown.ContentLength || etagValue(literal.ETag) != etagValue(shown.ETag) {
		t.Fatalf("literal = %+v, shown = %+v: the identities must compare equal", literal, shown)
	}

	mock.PutObject("fixtures", "dangling", swifttest.Object{SymlinkTarget: "fixtures/nowhere"})
	if !IsNotFound(mustErr(s.ObjectDetail(ctx, "fixtures", "dangling"))) {
		t.Fatal("a following read was expected to report the dangling link's target as not found")
	}
	link, err := s.LiteralObjectHead(ctx, "fixtures", "dangling")
	if err != nil || link.SymlinkTarget != "fixtures/nowhere" {
		t.Fatalf("literal read of a dangling link = %+v, %v", link, err)
	}

	manifest, err := s.LiteralObjectHead(ctx, "fixtures", "external-dlo")
	if err != nil || manifest.Class != ClassDLO || manifest.Manifest == "" || manifest.ContentLength != 0 {
		t.Fatalf("literal read of a manifest = %+v, %v: want the stored manifest, not the assembled object", manifest, err)
	}
	if assembled, err := s.ObjectDetail(ctx, "fixtures", "external-dlo"); err != nil || assembled.ContentLength == 0 {
		t.Fatalf("the following read should assemble the manifest: %+v %v", assembled, err)
	}
}

// gatedSession builds a session whose every request passes through gate, a
// proxy in front of the fixture mock. The mock derives the storage URL from
// the Host it sees, so a session built against the gate keeps flowing through
// it; forward() hands the untouched request to the mock.
type gateFn func(w http.ResponseWriter, r *http.Request, forward func())

func gatedServer(t *testing.T, mock *swifttest.Server, gate gateFn) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, func() { mock.ServeHTTP(w, r) })
	}))
	t.Cleanup(server.Close)
	return server
}

func sessionThrough(t *testing.T, server *httptest.Server) *Session {
	t.Helper()
	s, err := New(Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func isDelete(r *http.Request, name string) bool {
	return r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/fixtures/"+name)
}
