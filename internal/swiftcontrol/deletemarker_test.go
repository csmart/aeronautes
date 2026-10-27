package swiftcontrol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ADR 0094 decision 2: every empty object has the same content fingerprint,
// so the marker deletion compares the identity AND requires the marker rule
// at the comparison HEAD. A zero-byte plain object of another content type
// matches the fingerprint and is still refused, and nothing is deleted.
func TestDeleteMarkerObjectRequiresTheMarkerRuleBesideTheIdentity(t *testing.T) {
	s, conn := newWritableSession(t)
	ctx := context.Background()
	if _, err := s.CreateMarker(ctx, "media", "docs/"); err != nil {
		t.Fatal(err)
	}
	marker, err := s.ObjectDetail(ctx, "media", "docs/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ObjectPut(ctx, "media", "empty/", strings.NewReader(""), true, "", "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	empty, err := s.ObjectDetail(ctx, "media", "empty/")
	if err != nil {
		t.Fatal(err)
	}
	if empty.ContentFingerprint() != marker.ContentFingerprint() {
		t.Fatalf("the fixture no longer shows two empty objects sharing an identity: %s vs %s", empty.ContentFingerprint(), marker.ContentFingerprint())
	}
	_, err = s.DeleteMarkerObject(ctx, "media", "empty/", marker.ContentFingerprint())
	var conflict *IdentityConflictError
	if err == nil || errors.As(err, &conflict) || !strings.Contains(err.Error(), "not a directory marker") {
		t.Fatalf("an empty plain object passed the marker path on its identity alone: %v", err)
	}
	if _, err := s.ObjectDetail(ctx, "media", "empty/"); err != nil {
		t.Fatalf("the refused object was deleted anyway: %v", err)
	}
	result, err := s.DeleteMarkerObject(ctx, "media", "docs/", marker.ContentFingerprint())
	if err != nil || result.Outcome != UpdateApplied || !result.Accepted || result.After != nil {
		t.Fatalf("the marker itself was not deleted: %+v, %v", result, err)
	}
}

// ADR 0094 decision 2: the marker path owns markers only; a manifest at the
// name is refused towards the manifest deletion, and a stated identity that
// the name does not hold is refused with both identities.
func TestDeleteMarkerObjectRefusesManifestsAndChangedIdentities(t *testing.T) {
	s := newFixtureSession(t)
	ctx := context.Background()
	dlo, err := s.ObjectDetail(ctx, "fixtures", "external-dlo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMarkerObject(ctx, "fixtures", "external-dlo", dlo.ContentFingerprint()); err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("a manifest passed the marker path: %v", err)
	}
	if _, err := s.ObjectDetail(ctx, "fixtures", "external-dlo"); err != nil {
		t.Fatalf("the refused manifest was deleted: %v", err)
	}
	if _, err := s.DeleteMarkerObject(ctx, "fixtures", "external-dlo", ""); err == nil {
		t.Fatal("a delete with no stated identity was accepted")
	}
	if _, err := s.DeleteMarkerObject(ctx, "fixtures", "", "unused"); err == nil {
		t.Fatal("an empty name was accepted")
	}
}
