package swiftcontrol

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

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

func TestDeleteManifestObjectDLO(t *testing.T) {
	s := newFixtureSession(t)
	ctx := context.Background()

	segmentsBefore := countObjects(t, s, "external-dlo-segments")

	shown, err := s.ObjectDetail(ctx, "fixtures", "external-dlo")
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.DeleteManifestObject(ctx, "fixtures", "external-dlo", shown.ContentFingerprint())
	if err != nil {
		t.Fatal(err)
	}
	if before.Class != ClassDLO {
		t.Errorf("preflight class = %q", before.Class)
	}

	var se *SwiftError
	if _, err := s.ObjectDetail(ctx, "fixtures", "external-dlo"); !errors.As(err, &se) || se.Kind != KindNotFound {
		t.Errorf("manifest still readable after delete: %v", err)
	}
	// THE point: every segment survives.
	if got := countObjects(t, s, "external-dlo-segments"); got != segmentsBefore {
		t.Errorf("segments = %d, want untouched %d", got, segmentsBefore)
	}
}

func TestDeleteManifestObjectSLO(t *testing.T) {
	s := newFixtureSession(t)
	ctx := context.Background()

	segmentsBefore := countObjects(t, s, "slo-parts")
	shown, err := s.ObjectDetail(ctx, "fixtures", "external-slo")
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.DeleteManifestObject(ctx, "fixtures", "external-slo", shown.ContentFingerprint())
	if err != nil {
		t.Fatal(err)
	}
	if before.Class != ClassSLO {
		t.Errorf("preflight class = %q", before.Class)
	}
	if got := countObjects(t, s, "slo-parts"); got != segmentsBefore {
		t.Errorf("segments = %d, want untouched %d", got, segmentsBefore)
	}
}

func TestDeleteManifestObjectRefusesPlain(t *testing.T) {
	s := newFixtureSession(t)
	ctx := context.Background()

	plain, err := s.ObjectDetail(ctx, "fixtures", "empty-object")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteManifestObject(ctx, "fixtures", "empty-object", plain.ContentFingerprint()); err == nil {
		t.Fatal("plain object accepted by the manifest-only delete")
	}
	// And nothing was deleted.
	if _, err := s.ObjectDetail(ctx, "fixtures", "empty-object"); err != nil {
		t.Errorf("refused object was deleted anyway: %v", err)
	}

	var se *SwiftError
	// A name that is not there cannot supply an identity, so a plausible one
	// is passed and the not-found HEAD is what must surface.
	if _, err := s.DeleteManifestObject(ctx, "fixtures", "missing", "unused"); !errors.As(err, &se) || se.Kind != KindNotFound {
		t.Errorf("missing object = %v, want not_found", err)
	}
}

// ADR 0085: the HEAD this delete already makes becomes the comparison point, so
// what is destroyed is what the caller was shown rather than whatever the name
// holds when the DELETE arrives. These are the refusals; the cases above are the
// controls that an identity which still matches still deletes.
func TestDeleteManifestObjectRefusesWhatItWasNotShown(t *testing.T) {
	ctx := context.Background()

	t.Run("a changed object is refused and left alone", func(t *testing.T) {
		s := newFixtureSession(t)
		shown, err := s.ObjectDetail(ctx, "fixtures", "external-dlo")
		if err != nil {
			t.Fatal(err)
		}
		// An identity from a different object of a different class and length:
		// what a replacement between the display and the request would leave.
		other, err := s.ObjectDetail(ctx, "fixtures", "external-slo")
		if err != nil {
			t.Fatal(err)
		}
		if other.ContentFingerprint() == shown.ContentFingerprint() {
			t.Fatal("the fixture no longer distinguishes these two objects")
		}
		before, err := s.DeleteManifestObject(ctx, "fixtures", "external-dlo", other.ContentFingerprint())
		if err == nil {
			t.Fatal("a delete was accepted for an identity the name does not hold")
		}
		if before == nil || before.Class != ClassDLO {
			t.Errorf("the refusal did not report what was actually there: %+v", before)
		}
		// Refused BEFORE the DELETE: the object is still readable.
		if _, err := s.ObjectDetail(ctx, "fixtures", "external-dlo"); err != nil {
			t.Errorf("a refused delete removed the object anyway: %v", err)
		}
	})

	t.Run("an absent identity is refused before any request", func(t *testing.T) {
		s := newFixtureSession(t)
		if _, err := s.DeleteManifestObject(ctx, "fixtures", "external-dlo", ""); err == nil {
			t.Fatal("a delete with no stated identity was accepted")
		}
		if _, err := s.ObjectDetail(ctx, "fixtures", "external-dlo"); err != nil {
			t.Errorf("a refused delete removed the object anyway: %v", err)
		}
	})

	t.Run("a same-class replacement of different length is refused", func(t *testing.T) {
		s, mock := newFixtureSessionWithMock(t)
		shown, err := s.ObjectDetail(ctx, "fixtures", "external-dlo")
		if err != nil {
			t.Fatal(err)
		}
		// The identity the operator was handed, from a real HEAD. The earlier
		// version of this test compared hand-built copies against a stored
		// value that was itself computed before the length and tag were
		// populated, so it passed while the guard did nothing.
		was := shown.ContentFingerprint()
		lengthenDLO(t, mock)
		after, err := s.ObjectDetail(ctx, "fixtures", "external-dlo")
		if err != nil {
			t.Fatal(err)
		}
		if after.Class != shown.Class {
			t.Fatalf("the replacement changed class, so this case is not about length: %s", after.Class)
		}
		if after.ContentFingerprint() == was {
			t.Fatal("a different logical length left the identity unchanged")
		}
		if _, err := s.DeleteManifestObject(ctx, "fixtures", "external-dlo", was); err == nil {
			t.Fatal("a same-class replacement of different length was deleted")
		}
		if _, err := s.ObjectDetail(ctx, "fixtures", "external-dlo"); err != nil {
			t.Errorf("a refused delete removed the object anyway: %v", err)
		}
	})

	t.Run("a same-class replacement of different content is refused", func(t *testing.T) {
		s, mock := newFixtureSessionWithMock(t)
		shown, err := s.ObjectDetail(ctx, "fixtures", "external-dlo")
		if err != nil {
			t.Fatal(err)
		}
		was := shown.ContentFingerprint()
		// Same class, same logical length, different aggregate entity tag:
		// the substitution a length check alone cannot see.
		retagDLO(t, mock)
		after, err := s.ObjectDetail(ctx, "fixtures", "external-dlo")
		if err != nil {
			t.Fatal(err)
		}
		if after.Class != shown.Class || after.ContentLength != shown.ContentLength {
			t.Fatalf("the replacement changed class or length, so this case is not about the tag: %+v", after)
		}
		if after.ContentFingerprint() == was {
			t.Fatal("a different entity tag left the identity unchanged")
		}
		if _, err := s.DeleteManifestObject(ctx, "fixtures", "external-dlo", was); err == nil {
			t.Fatal("a same-class same-length replacement of different content was deleted")
		}
		if _, err := s.ObjectDetail(ctx, "fixtures", "external-dlo"); err != nil {
			t.Errorf("a refused delete removed the object anyway: %v", err)
		}
	})

	t.Run("an unchanged object still matches its own identity", func(t *testing.T) {
		s := newFixtureSession(t)
		first, err := s.ObjectDetail(ctx, "fixtures", "external-dlo")
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.ObjectDetail(ctx, "fixtures", "external-dlo")
		if err != nil {
			t.Fatal(err)
		}
		// Two HEADs of an untouched object agree, which is what makes the
		// refusals above mean a change rather than noise.
		if first.ContentFingerprint() != second.ContentFingerprint() {
			t.Fatalf("two reads of one object disagreed: %s vs %s", first.ContentFingerprint(), second.ContentFingerprint())
		}
		if first.ContentLength == 0 || etagValue(first.ETag) == "" {
			t.Fatalf("the identity was built from an unpopulated detail: length %d tag %q", first.ContentLength, first.ETag)
		}
	})
}

// The identity is derived from a populated detail rather than stored, so the
// fields it must notice can be varied one at a time — which a real replacement
// cannot do. Appending bytes to a DLO moves the assembled length AND the
// aggregate entity tag together, so the end-to-end cases above can only show
// that pair moving. This is where the length alone is shown to matter.
func TestContentFingerprintNoticesEachFieldAlone(t *testing.T) {
	base := ObjectDetail{Class: ClassDLO, ContentLength: 3_000_000_000, ETag: "ac9c542684f271e7215c00369bbb793e"}
	was := base.ContentFingerprint()

	for _, c := range []struct {
		name  string
		alter func(*ObjectDetail)
	}{
		{"class alone", func(d *ObjectDetail) { d.Class = ClassSLO }},
		{"logical length alone", func(d *ObjectDetail) { d.ContentLength = base.ContentLength + 1 }},
		{"entity tag alone", func(d *ObjectDetail) { d.ETag = "0d1ee1c5cba1c9a3e8fdcd0a0a0f5ec4" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			altered := base
			c.alter(&altered)
			if altered.ContentFingerprint() == was {
				t.Fatalf("%s left the identity unchanged, so a replacement differing only there would be deleted", c.name)
			}
		})
	}

	t.Run("no two objects can spell the same input", func(t *testing.T) {
		// Length-framed rather than concatenated. These class values are not
		// real; the point is the encoding, and without the framing both of
		// these hash "dlo12ab".
		a := ObjectDetail{Class: "dlo", ContentLength: 12, ETag: "ab"}
		b := ObjectDetail{Class: "dlo1", ContentLength: 2, ETag: "ab"}
		if a.ContentFingerprint() == b.ContentFingerprint() {
			t.Fatal("two different objects share an identity, so one could be deleted for the other")
		}
	})
}

// lengthenDLO adds a segment under the canonical DLO's prefix, so the manifest's
// assembled length and its aggregate entity tag both move while its class does
// not. This is a replacement an operator could not see from the name.
func lengthenDLO(t *testing.T, mock *swifttest.Server) {
	t.Helper()
	mock.PutObject("external-dlo-segments", "wedding-video/0000000000000004",
		swifttest.Object{Bytes: 1_000_000, ETag: md5hex("appended")})
}

// retagDLO replaces one segment's content and entity tag while keeping its
// length, so the manifest's assembled length is unchanged and only the aggregate
// moves — the substitution a length comparison alone cannot see.
func retagDLO(t *testing.T, mock *swifttest.Server) {
	t.Helper()
	mock.PutObject("external-dlo-segments", "wedding-video/0000000000000001",
		swifttest.Object{Bytes: 1_000_000_000, ETag: md5hex("substituted")})
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
