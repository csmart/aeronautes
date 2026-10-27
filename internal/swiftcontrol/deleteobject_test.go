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

func shownIdentity(t *testing.T, s *Session, container, name string) string {
	t.Helper()
	d, err := s.ObjectDetail(context.Background(), container, name)
	if err != nil {
		t.Fatal(err)
	}
	return d.ContentFingerprint()
}

// The happy path: the shown identity is gone afterwards, nothing else moved.
func TestDeletePlainObjectAppliesWhenTheNameIsEmptyAfterwards(t *testing.T) {
	s := newFixtureSession(t)
	ctx := context.Background()
	siblings := countObjects(t, s, "fixtures")

	result, err := s.DeletePlainObject(ctx, "fixtures", plainName, shownIdentity(t, s, "fixtures", plainName))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateApplied || !result.Accepted || result.After != nil {
		t.Fatalf("result = %+v, want applied, accepted, nothing observed", result)
	}
	if result.Before == nil || result.Before.Class != ClassPlain {
		t.Fatalf("before = %+v", result.Before)
	}
	if !IsNotFound(mustErr(s.ObjectDetail(ctx, "fixtures", plainName))) {
		t.Fatal("the object is still readable after the delete")
	}
	if got := countObjects(t, s, "fixtures"); got != siblings-1 {
		t.Fatalf("fixtures holds %d objects, want %d: a sibling moved", got, siblings-1)
	}
}

func mustErr(_ *ObjectDetail, err error) error { return err }

// Every refusal leaves the namespace exactly as it was.
func TestDeletePlainObjectRefusesManifestsLinksAndABlankIdentity(t *testing.T) {
	s, mock := newFixtureSessionWithMock(t)
	ctx := context.Background()
	// A same-account link the HEAD follows, and a cross-account link the mock
	// represents unfollowed with its target header (the DTO contract for it).
	mock.PutObject("fixtures", "link", swifttest.Object{SymlinkTarget: "fixtures/" + plainName})
	mock.PutObject("fixtures", "far-link", swifttest.Object{SymlinkTarget: "elsewhere/object", SymlinkTargetAccount: "AUTH_other"})
	before := countObjects(t, s, "fixtures")

	cases := []struct {
		name, object, identity, wantInError string
	}{
		{"dynamic manifest", "external-dlo", shownIdentity(t, s, "fixtures", "external-dlo"), "manifest"},
		{"static manifest", "external-slo", shownIdentity(t, s, "fixtures", "external-slo"), "manifest"},
		{"followed link", "link", shownIdentity(t, s, "fixtures", "link"), "symlink"},
		{"unfollowed cross-account link", "far-link", shownIdentity(t, s, "fixtures", "far-link"), "symlink"},
		{"blank identity", plainName, "", "cannot say what it meant to destroy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.DeletePlainObject(ctx, "fixtures", tc.object, tc.identity)
			if err == nil || !strings.Contains(err.Error(), tc.wantInError) {
				t.Fatalf("err = %v, want a refusal mentioning %q", err, tc.wantInError)
			}
			if result != nil {
				t.Fatalf("a refusal returned a result: %+v", result)
			}
		})
	}
	// A name that is not there cannot supply an identity; the not-found HEAD
	// is what must surface, and a plausible identity cannot change that.
	if err := mustErr(nil, func() error { _, err := s.DeletePlainObject(ctx, "fixtures", "missing", "unused"); return err }()); !IsNotFound(err) {
		t.Errorf("missing object = %v, want not_found", err)
	}
	if got := countObjects(t, s, "fixtures"); got != before {
		t.Fatalf("fixtures holds %d objects after refusals, want %d", got, before)
	}
}

// ADR 0085's comparison: a replacement between the display and the request is
// refused with both identities, whether or not the length changed.
func TestDeletePlainObjectRefusesWhatItWasNotShown(t *testing.T) {
	ctx := context.Background()
	replacements := map[string]swifttest.Object{
		"a different length": {Bytes: 99, ContentType: "text/plain", ETag: "00000000000000000000000000000099"},
		"the same length":    {Bytes: 12, ContentType: "text/plain", ETag: "ffffffffffffffffffffffffffffffff"},
	}
	for name, replacement := range replacements {
		t.Run(name, func(t *testing.T) {
			s, mock := newFixtureSessionWithMock(t)
			shown := shownIdentity(t, s, "fixtures", plainName)
			mock.PutObject("fixtures", plainName, replacement)

			result, err := s.DeletePlainObject(ctx, "fixtures", plainName, shown)
			var conflict *IdentityConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("err = %v, want an identity conflict", err)
			}
			// The comparison HEAD travels with the conflict for the record's
			// transaction id, but no outcome: nothing was issued.
			if result == nil || result.Before == nil || result.Outcome != "" || result.Accepted || result.After != nil {
				t.Fatalf("a conflict returned %+v, want the comparison HEAD alone", result)
			}
			if conflict.Expected != shown || conflict.Found == shown || conflict.FoundLength != replacement.Bytes {
				t.Fatalf("conflict = %+v", conflict)
			}
			if d, err := s.ObjectDetail(ctx, "fixtures", plainName); err != nil || d.ContentLength != replacement.Bytes {
				t.Fatalf("the replacement was touched: %+v %v", d, err)
			}
		})
	}
}

// The literal read classifies before it compares: a successor of another kind
// or another plain identity is applied with the successor observed; the same
// identity is unverified.
func TestDeletePlainObjectClassifiesWhatTheLiteralReadFinds(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name          string
		object        string // the plain object deleted; the canonical one unless set
		successor     swifttest.Object
		wantOutcome   UpdateOutcome
		wantErr       error
		wantClass     ObjectClass
		wantLinkTo    string
		followingSees string // "absent" when a following read would call the name empty
	}{
		{name: "a dangling link", successor: swifttest.Object{SymlinkTarget: "fixtures/nowhere"},
			wantOutcome: UpdateApplied, wantClass: ClassPlain, wantLinkTo: "fixtures/nowhere", followingSees: "absent"},
		// An empty plain object and a literal link share the class, the zero
		// length and the empty entity tag the fingerprint hashes, so only
		// classifying before comparing tells them apart.
		{name: "an empty object replaced by a literal link", object: "empty-object", successor: swifttest.Object{SymlinkTarget: "fixtures/nowhere"},
			wantOutcome: UpdateApplied, wantClass: ClassPlain, wantLinkTo: "fixtures/nowhere", followingSees: "absent"},
		{name: "a manifest whose segments do not exist", successor: swifttest.Object{Manifest: "missing-segments/x/", ContentType: "video/mp4"},
			wantOutcome: UpdateApplied, wantClass: ClassDLO},
		{name: "a different plain object", successor: swifttest.Object{Bytes: 3, ContentType: "text/plain", ETag: "900150983cd24fb0d6963f7d28e17f72"},
			wantOutcome: UpdateApplied, wantClass: ClassPlain},
		{name: "the same identity written back", successor: swifttest.Object{Bytes: 12, ContentType: "text/plain", ETag: "5eb63bbbe01eeed093cb22bb8f5acdc3"},
			wantOutcome: UpdateUnverified, wantErr: ErrUnverifiedDelete, wantClass: ClassPlain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := newFixtureSessionWithMock(t)
			object := tc.object
			if object == "" {
				object = plainName
			}
			shown := shownIdentity(t, s, "fixtures", object)
			placed := false
			// The hook runs inside the DELETE's handler before its response is
			// flushed, so the successor stands at the name before the verifying
			// read can start: the writer the contract describes.
			mock.AfterRequest = func(method, path string) {
				if method == "DELETE" && strings.HasSuffix(path, "/fixtures/"+object) && !placed {
					placed = true
					mock.PutObject("fixtures", object, tc.successor)
				}
			}

			result, err := s.DeletePlainObject(ctx, "fixtures", object, shown)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("err = %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if !placed {
				t.Fatal("the successor was never placed; the case proved nothing")
			}
			if result == nil || result.Outcome != tc.wantOutcome || !result.Accepted {
				t.Fatalf("result = %+v, want %s and accepted", result, tc.wantOutcome)
			}
			if result.After == nil || result.After.Class != tc.wantClass || result.After.SymlinkTarget != tc.wantLinkTo {
				t.Fatalf("after = %+v, want class %s link %q", result.After, tc.wantClass, tc.wantLinkTo)
			}
			if tc.followingSees == "absent" {
				if !IsNotFound(mustErr(s.ObjectDetail(ctx, "fixtures", object))) {
					t.Fatal("a following read was expected to call the dangling link absent")
				}
			}
		})
	}
}

// A lost reply or a server error leaves the effect unproven whatever the name
// holds afterwards, and a client-side refusal is not ours to claim.
func TestDeletePlainObjectReportsUnprovenAndRefusedDeletes(t *testing.T) {
	ctx := context.Background()

	t.Run("lost reply", func(t *testing.T) {
		s, mock := newFixtureSessionWithMock(t)
		shown := shownIdentity(t, s, "fixtures", plainName)
		mock.DropNextObjectDELETE()
		result, err := s.DeletePlainObject(ctx, "fixtures", plainName, shown)
		if !errors.Is(err, ErrUnverifiedDelete) {
			t.Fatalf("err = %v, want unverified", err)
		}
		if result == nil || result.Outcome != UpdateUnverified || result.Accepted {
			t.Fatalf("result = %+v, want unverified and not accepted", result)
		}
		// The request landed and the reply was lost, so the name is empty
		// and still the outcome is not applied.
		if result.After != nil {
			t.Fatalf("after = %+v, want nothing observed", result.After)
		}
	})

	t.Run("server error", func(t *testing.T) {
		s, mock := newFixtureSessionWithMock(t)
		shown := shownIdentity(t, s, "fixtures", plainName)
		mock.FailDeletes = true
		result, err := s.DeletePlainObject(ctx, "fixtures", plainName, shown)
		if !errors.Is(err, ErrUnverifiedDelete) {
			t.Fatalf("err = %v, want unverified", err)
		}
		if result == nil || result.Outcome != UpdateUnverified || result.Accepted || result.After == nil {
			t.Fatalf("result = %+v, want unverified with the object still observed", result)
		}
	})

	t.Run("gone before the delete arrived", func(t *testing.T) {
		s, mock := newFixtureSessionWithMock(t)
		shown := shownIdentity(t, s, "fixtures", plainName)
		removed := false
		mock.AfterRequest = func(method, path string) {
			if method == "HEAD" && strings.HasSuffix(path, "/fixtures/"+plainName) && !removed {
				removed = true
				mock.RemoveObject("fixtures", plainName)
			}
		}
		result, err := s.DeletePlainObject(ctx, "fixtures", plainName, shown)
		if !IsNotFound(err) || errors.Is(err, ErrUnverifiedDelete) {
			t.Fatalf("err = %v, want the store's not found", err)
		}
		if result == nil || result.Accepted || result.Outcome != "" {
			t.Fatalf("result = %+v, want no accepted outcome", result)
		}
	})
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
	if err != nil || link.SymlinkTarget != "fixtures/nowhere" || literalPlain(link) {
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

func isComparisonHead(r *http.Request, name string) bool {
	return r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/fixtures/"+name) && r.URL.RawQuery == ""
}
func isLiteralHead(r *http.Request, name string) bool {
	return r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/fixtures/"+name) && strings.Contains(r.URL.RawQuery, "multipart-manifest=get")
}
func isDelete(r *http.Request, name string) bool {
	return r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/fixtures/"+name)
}

// ADR 0085's adjacency: nothing of ours, and no authentication, runs between
// the comparison HEAD and the DELETE. A token the store refuses on the DELETE
// is therefore a refusal, not a cue to authenticate and delete again.
func TestDeletePlainObjectRefusedTokenOnTheDeleteIsARefusal(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	deletes := 0
	server := gatedServer(t, mock, func(w http.ResponseWriter, r *http.Request, forward func()) {
		if isDelete(r, plainName) {
			deletes++
			if deletes == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		forward()
	})
	s := sessionThrough(t, server)
	ctx := context.Background()
	shown := shownIdentity(t, s, "fixtures", plainName)

	result, err := s.DeletePlainObject(ctx, "fixtures", plainName, shown)
	var se *SwiftError
	if !errors.As(err, &se) || se.Kind != KindUnauthorized {
		t.Fatalf("err = %v, want the token refusal", err)
	}
	if result == nil || result.Accepted || result.Outcome != "" {
		t.Fatalf("result = %+v, want nothing accepted", result)
	}
	if deletes != 1 {
		t.Fatalf("%d DELETE requests, want exactly the refused one", deletes)
	}
	if _, err := s.ObjectDetail(ctx, "fixtures", plainName); err != nil {
		t.Fatalf("the object is gone after a refused DELETE: %v", err)
	}
}

// A token the store refuses on the comparison HEAD restarts the whole
// sequence under fresh credentials, and those may name another storage URL:
// the comparison, the DELETE and the verifying read all go where the new
// credentials point, and the namespace the refused HEAD was aimed at sees no
// DELETE.
func TestDeletePlainObjectRestartsUnderFreshCredentialsAfterARefusedHead(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	var second *httptest.Server
	secondSeen := map[string]int{}
	second = gatedServer(t, mock, func(w http.ResponseWriter, r *http.Request, forward func()) {
		switch {
		case isComparisonHead(r, plainName):
			secondSeen["head"]++
		case isDelete(r, plainName):
			secondSeen["delete"]++
		case isLiteralHead(r, plainName):
			secondSeen["literal"]++
		}
		forward()
	})
	firstSeen := map[string]int{}
	auths := 0
	first := gatedServer(t, mock, func(w http.ResponseWriter, r *http.Request, forward func()) {
		switch {
		case r.URL.Path == "/auth/v1.0":
			auths++
			if auths == 1 {
				forward()
				return
			}
			// The re-authentication names the other storage URL.
			rec := httptest.NewRecorder()
			mock.ServeHTTP(rec, r)
			for k, values := range rec.Header() {
				w.Header()[k] = append([]string(nil), values...)
			}
			if rec.Header().Get("X-Storage-Url") != "" {
				w.Header().Set("X-Storage-Url", second.URL+"/v1/AUTH_test")
			}
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
		case isComparisonHead(r, plainName):
			firstSeen["head"]++
			if firstSeen["head"] == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			forward()
		case isDelete(r, plainName):
			firstSeen["delete"]++
			forward()
		default:
			forward()
		}
	})
	s := sessionThrough(t, first)
	ctx := context.Background()
	shown, err := s.LiteralObjectHead(ctx, "fixtures", plainName)
	if err != nil {
		t.Fatal(err)
	}

	result, err := s.DeletePlainObject(ctx, "fixtures", plainName, shown.ContentFingerprint())
	if err != nil || result.Outcome != UpdateApplied {
		t.Fatalf("result = %+v, err = %v, want applied under the fresh credentials", result, err)
	}
	if firstSeen["head"] != 1 || firstSeen["delete"] != 0 {
		t.Fatalf("the refused namespace saw %+v, want one refused HEAD and no DELETE", firstSeen)
	}
	if secondSeen["head"] != 1 || secondSeen["delete"] != 1 || secondSeen["literal"] != 1 {
		t.Fatalf("the fresh namespace saw %+v, want one comparison, one DELETE and one literal read", secondSeen)
	}
}

// Only a success status accepts the DELETE: a redirect arrives as a plain
// status because the session's transport drops redirects, and it must not be
// read as the store having applied the request.
func TestDeletePlainObjectDoesNotAcceptARedirect(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	server := gatedServer(t, mock, func(w http.ResponseWriter, r *http.Request, forward func()) {
		if isDelete(r, plainName) {
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		forward()
	})
	s := sessionThrough(t, server)
	ctx := context.Background()
	shown := shownIdentity(t, s, "fixtures", plainName)

	result, err := s.DeletePlainObject(ctx, "fixtures", plainName, shown)
	var se *SwiftError
	if !errors.As(err, &se) || se.HTTPStatus != http.StatusTemporaryRedirect || errors.Is(err, ErrUnverifiedDelete) {
		t.Fatalf("err = %v, want the redirect reported as not accepted", err)
	}
	if result == nil || result.Accepted || result.Outcome != "" {
		t.Fatalf("result = %+v, want nothing accepted", result)
	}
	if _, err := s.ObjectDetail(ctx, "fixtures", plainName); err != nil {
		t.Fatalf("the object is gone after a redirected DELETE: %v", err)
	}
}

// A found plain object whose literal response lacks the fields the identity
// is compared by is not compared: the zero values would make an unchanged
// object look different, so the outcome stays unverified.
func TestDeletePlainObjectKeepsAnIncomparableSuccessorUnverified(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	server := gatedServer(t, mock, func(w http.ResponseWriter, r *http.Request, forward func()) {
		if isLiteralHead(r, plainName) {
			rec := httptest.NewRecorder()
			mock.ServeHTTP(rec, r)
			for k, values := range rec.Header() {
				if k == "Etag" {
					continue
				}
				w.Header()[k] = append([]string(nil), values...)
			}
			w.WriteHeader(rec.Code)
			return
		}
		forward()
	})
	s := sessionThrough(t, server)
	ctx := context.Background()
	shown := shownIdentity(t, s, "fixtures", plainName)
	placed := false
	mock.AfterRequest = func(method, path string) {
		if method == "DELETE" && strings.HasSuffix(path, "/fixtures/"+plainName) && !placed {
			placed = true
			// The exact shown object written back: a comparable read would call it
			// the same identity; an incomparable one must not call it different.
			mock.PutObject("fixtures", plainName, swifttest.Object{Bytes: 12, ContentType: "text/plain", ETag: "5eb63bbbe01eeed093cb22bb8f5acdc3"})
		}
	}

	result, err := s.DeletePlainObject(ctx, "fixtures", plainName, shown)
	if !errors.Is(err, ErrUnverifiedDelete) || !strings.Contains(err.Error(), "comparable identity") {
		t.Fatalf("err = %v, want unverified for an incomparable identity", err)
	}
	if result == nil || result.Outcome != UpdateUnverified || !result.Accepted || result.After == nil || result.AfterComparable {
		t.Fatalf("result = %+v, want unverified with an incomparable successor", result)
	}
}

func TestIdentityComparableNeedsBothFields(t *testing.T) {
	complete := http.Header{"Content-Length": {"12"}, "Etag": {"abc"}}
	if !identityComparable(complete) {
		t.Fatal("a length and an entity tag are comparable")
	}
	for name, headers := range map[string]http.Header{
		"no length":        {"Etag": {"abc"}},
		"unparseable":      {"Content-Length": {"x"}, "Etag": {"abc"}},
		"no entity tag":    {"Content-Length": {"12"}},
		"empty entity tag": {"Content-Length": {"12"}, "Etag": {""}},
	} {
		if identityComparable(headers) {
			t.Errorf("%s: reported comparable", name)
		}
	}
}

// A lost reply followed by a literal read that fails is unverified, and the
// result says the read failed rather than that the name was empty.
func TestDeletePlainObjectKeepsAFailedLiteralReadApart(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	server := gatedServer(t, mock, func(w http.ResponseWriter, r *http.Request, forward func()) {
		if isLiteralHead(r, plainName) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		forward()
	})
	s := sessionThrough(t, server)
	ctx := context.Background()
	shown := shownIdentity(t, s, "fixtures", plainName)
	mock.DropNextObjectDELETE()

	result, err := s.DeletePlainObject(ctx, "fixtures", plainName, shown)
	if !errors.Is(err, ErrUnverifiedDelete) {
		t.Fatalf("err = %v, want unverified", err)
	}
	if result == nil || result.Accepted || result.After != nil || result.AfterRead == nil {
		t.Fatalf("result = %+v, want the literal read's failure kept apart from an empty name", result)
	}
}

// Between the comparison HEAD's reply and the DELETE nothing of ours runs,
// and in particular no authentication: a writer who lands during one would be
// deleted unseen. The gate watches for an authentication inside that window
// and, as the reviewer's probe did, places a manifest at the name during it.
func TestDeletePlainObjectIssuesNoAuthenticationBetweenComparisonAndDelete(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	// armed once the inspector's own read is done, so the HEAD the gate counts
	// as the comparison is the one DeletePlainObject makes.
	armed, compared, deleted := false, false, false
	authsBetween := 0
	server := gatedServer(t, mock, func(w http.ResponseWriter, r *http.Request, forward func()) {
		switch {
		case !armed:
		case isComparisonHead(r, plainName):
			forward()
			compared = true
			return
		case isDelete(r, plainName):
			deleted = true
		case r.URL.Path == "/auth/v1.0" && compared && !deleted:
			authsBetween++
			mock.PutObject("fixtures", plainName, swifttest.Object{Manifest: "external-dlo-segments/wedding-video/", ContentType: "video/mp4"})
		}
		forward()
	})
	s := sessionThrough(t, server)
	ctx := context.Background()
	shown := shownIdentity(t, s, "fixtures", plainName)
	armed = true

	result, err := s.DeletePlainObject(ctx, "fixtures", plainName, shown)
	if err != nil || result.Outcome != UpdateApplied {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if authsBetween != 0 {
		t.Fatalf("%d authentication requests ran between the comparison HEAD and the DELETE", authsBetween)
	}
	if !compared || !deleted {
		t.Fatalf("the sequence did not run: compared=%v deleted=%v", compared, deleted)
	}
}

// A refused token is invalidated where it is observed, whether or not the
// caller retries: after a terminal refusal the next Credentials call
// authenticates again instead of handing the rejected token on.
func TestRefusedTokensAreInvalidatedEvenWhenNoRetryRemains(t *testing.T) {
	ctx := context.Background()
	for name, call := range map[string]func(s *Session) error{
		"comparison HEAD": func(s *Session) error {
			_, err := s.DeletePlainObject(ctx, "fixtures", plainName, "any")
			return err
		},
		"symlink read": func(s *Session) error {
			_, err := s.SymlinkGet(ctx, "fixtures", plainName)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.LoadCanonicalFixtures()
			auths, refused := 0, 0
			server := gatedServer(t, mock, func(w http.ResponseWriter, r *http.Request, forward func()) {
				switch {
				case r.URL.Path == "/auth/v1.0":
					auths++
				case r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/fixtures/"+plainName):
					refused++
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				forward()
			})
			s := sessionThrough(t, server)
			err := call(s)
			var se *SwiftError
			if !errors.As(err, &se) || se.Kind != KindUnauthorized {
				t.Fatalf("err = %v, want the token refusal after the one retry", err)
			}
			if refused != 2 {
				t.Fatalf("%d refused HEADs, want the first and its one retry", refused)
			}
			before := auths
			if _, err := s.Credentials(ctx, 0); err != nil {
				t.Fatalf("credentials after the terminal refusal: %v", err)
			}
			if auths != before+1 {
				t.Fatalf("Credentials after a terminal refusal authenticated %d times, want exactly one fresh authentication", auths-before)
			}
		})
	}
}

// The same holds for a refused token on the DELETE itself.
func TestRefusedDeleteTokenIsInvalidated(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	auths := 0
	server := gatedServer(t, mock, func(w http.ResponseWriter, r *http.Request, forward func()) {
		switch {
		case r.URL.Path == "/auth/v1.0":
			auths++
		case isDelete(r, plainName):
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		forward()
	})
	s := sessionThrough(t, server)
	ctx := context.Background()
	shown := shownIdentity(t, s, "fixtures", plainName)
	if _, err := s.DeletePlainObject(ctx, "fixtures", plainName, shown); err == nil {
		t.Fatal("a refused DELETE token was not reported")
	}
	before := auths
	if _, err := s.Credentials(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if auths != before+1 {
		t.Fatalf("Credentials after a refused DELETE authenticated %d times, want one", auths-before)
	}
}

// An empty object name is the container's own path: refused before any request
// at the session and at both raw boundaries, so no caller can turn an object
// delete into a container delete.
func TestDeletePlainObjectRefusesAnEmptyTargetPart(t *testing.T) {
	s, mock := newFixtureSessionWithMock(t)
	ctx := context.Background()
	shown := shownIdentity(t, s, "fixtures", plainName)
	before := mock.Requests()
	for name, parts := range map[string][2]string{"empty name": {"fixtures", ""}, "empty container": {"", plainName}} {
		result, err := s.DeletePlainObject(ctx, parts[0], parts[1], shown)
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
	if _, _, status, err := s.rawHead(ctx, credentials, "fixtures", "", "", "head"); err == nil || status != 0 {
		t.Fatalf("raw HEAD of an empty name: status %d, err %v", status, err)
	}
	if got := mock.Requests(); got != before {
		t.Fatalf("%d requests reached the store for empty target parts", got-before)
	}
}
