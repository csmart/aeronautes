package swiftcontrol

import (
	"context"
	"errors"
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
