package swiftcontrol

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// listedMember is the row the working list carries for one object, which is
// what a selected-set deletion hands the session (ADR 0093 decision 2).
func listedMember(t *testing.T, s *Session, container, name string) MemberRow {
	t.Helper()
	for entry, err := range s.Objects(context.Background(), container, ObjectListOptions{Deep: true}) {
		if err != nil {
			t.Fatal(err)
		}
		if o, ok := entry.(ObjectEntry); ok && o.Name == name {
			return MemberRow{Name: o.Name, Bytes: o.Bytes, ETag: o.ETag, SLOListed: o.SLOListed}
		}
	}
	t.Fatalf("%s is not listed in %s", name, container)
	return MemberRow{}
}

// ADR 0085 and ADR 0093 decision 3: an ordinary row is its own baseline and
// is deleted by one comparison HEAD, one DELETE and the literal read.
func TestDeleteMemberUsesTheListingRowAsTheBaseline(t *testing.T) {
	s := newFixtureSession(t)
	ctx := context.Background()
	row := listedMember(t, s, "fixtures", "normal/hello.txt")
	if row.Bytes == 0 || row.ETag == "" || row.SLOListed {
		t.Fatalf("the fixture row is not an ordinary row: %+v", row)
	}
	result, err := s.DeleteMember(ctx, "fixtures", row)
	if err != nil || result.Outcome != UpdateApplied || !result.Accepted || result.After != nil {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if _, err := s.ObjectDetail(ctx, "fixtures", "normal/hello.txt"); !IsNotFound(err) {
		t.Fatalf("the member is still readable: %v", err)
	}
}

// A row listed as zero bytes or as a static large object may be a plain
// object, a marker or a manifest of either kind; a HEAD of its own fixes the
// baseline, and a manifest is deleted manifest-only.
func TestDeleteMemberTakesALaterBaselineWhereTheRowCannotSay(t *testing.T) {
	cases := []struct{ name, segments string }{
		{"empty-object", ""}, {"folder-marker/", ""}, {"external-dlo", "external-dlo-segments"}, {"external-slo", "slo-parts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newFixtureSession(t)
			ctx := context.Background()
			row := listedMember(t, s, "fixtures", tc.name)
			if row.Bytes != 0 && !row.SLOListed {
				t.Fatalf("the fixture row would take the listing baseline: %+v", row)
			}
			segmentsBefore := 0
			if tc.segments != "" {
				segmentsBefore = countObjects(t, s, tc.segments)
			}
			result, err := s.DeleteMember(ctx, "fixtures", row)
			if err != nil || result.Outcome != UpdateApplied || result.After != nil {
				t.Fatalf("result = %+v, %v", result, err)
			}
			if _, err := s.ObjectDetail(ctx, "fixtures", tc.name); !IsNotFound(err) {
				t.Fatalf("the member is still readable: %v", err)
			}
			if tc.segments != "" {
				if got := countObjects(t, s, tc.segments); got != segmentsBefore {
					t.Fatalf("segments = %d, want untouched %d", got, segmentsBefore)
				}
			}
		})
	}
}

// An ordinary row keeps its listing baseline whatever the comparison finds:
// a manifest now at the name is a change of class, a conflict with both
// identities, never a replacement baseline.
func TestDeleteMemberRefusesAChangedClassAsAConflict(t *testing.T) {
	s, mock := newFixtureSessionWithMock(t)
	ctx := context.Background()
	row := listedMember(t, s, "fixtures", "normal/hello.txt")
	mock.PutObject("fixtures", "normal/hello.txt", swifttest.Object{Bytes: 0, ContentType: "video/mp4", Manifest: "external-dlo-segments/wedding-video/"})
	result, err := s.DeleteMember(ctx, "fixtures", row)
	var conflict *IdentityConflictError
	if !errors.As(err, &conflict) || conflict.FoundClass != string(ClassDLO) || result == nil || result.Outcome != "" {
		t.Fatalf("err = %v result = %+v, want a conflict naming the manifest found", err, result)
	}
	if after, err := s.ObjectDetail(ctx, "fixtures", "normal/hello.txt"); err != nil || after.Class != ClassDLO {
		t.Fatalf("the manifest at the name was touched: %+v, %v", after, err)
	}
}

// A link is refused: at the later baseline for a zero-byte row, before any
// comparison, because the identity listed is its target's.
func TestDeleteMemberRefusesALinkAtTheLaterBaseline(t *testing.T) {
	s, mock := newFixtureSessionWithMock(t)
	ctx := context.Background()
	mock.PutObject("fixtures", "link.txt", swifttest.Object{Bytes: 0, ContentType: "text/plain", SymlinkTarget: "fixtures/normal/hello.txt"})
	row := listedMember(t, s, "fixtures", "link.txt")
	if row.Bytes != 0 {
		t.Fatalf("a link row lists bytes: %+v", row)
	}
	if _, err := s.DeleteMember(ctx, "fixtures", row); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("the link was not refused: %v", err)
	}
	if _, err := s.LiteralObjectHead(ctx, "fixtures", "link.txt"); err != nil {
		t.Fatalf("the link was deleted: %v", err)
	}
	if _, err := s.ObjectDetail(ctx, "fixtures", "normal/hello.txt"); err != nil {
		t.Fatalf("the target was deleted: %v", err)
	}
}

// The outcome words follow the baseline's kind: a plain member whose
// identity is written back is unverified, and so is a manifest member whose
// name holds a manifest of the same kind afterwards.
func TestDeleteMemberJudgesTheLiteralReadByTheBaselineKind(t *testing.T) {
	t.Run("plain identity written back", func(t *testing.T) {
		s, mock := newFixtureSessionWithMock(t)
		row := listedMember(t, s, "fixtures", "normal/hello.txt")
		placed := false
		mock.AfterRequest = func(method, path string) {
			if method == "DELETE" && strings.HasSuffix(path, "/fixtures/normal/hello.txt") && !placed {
				placed = true
				mock.PutObject("fixtures", "normal/hello.txt", swifttest.Object{Bytes: 12, ContentType: "text/plain", ETag: "5eb63bbbe01eeed093cb22bb8f5acdc3"})
			}
		}
		result, err := s.DeleteMember(context.Background(), "fixtures", row)
		if !errors.Is(err, ErrUnverifiedDelete) || !placed || !result.SameIdentityObserved() {
			t.Fatalf("err = %v placed = %v result = %+v", err, placed, result)
		}
	})
	t.Run("same-kind manifest written back", func(t *testing.T) {
		s, mock := newFixtureSessionWithMock(t)
		row := listedMember(t, s, "fixtures", "external-dlo")
		placed := false
		mock.AfterRequest = func(method, path string) {
			if method == "DELETE" && strings.HasSuffix(path, "/fixtures/external-dlo") && !placed {
				placed = true
				mock.PutObject("fixtures", "external-dlo", swifttest.Object{Bytes: 0, ContentType: "video/mp4", Manifest: "external-dlo-segments/wedding-video/"})
			}
		}
		result, err := s.DeleteMember(context.Background(), "fixtures", row)
		if !errors.Is(err, ErrUnverifiedDelete) || !placed || !result.SameKindManifestObserved() {
			t.Fatalf("err = %v placed = %v result = %+v", err, placed, result)
		}
	})
}

func TestDeleteMemberRefusesAnEmptyName(t *testing.T) {
	s := newFixtureSession(t)
	if _, err := s.DeleteMember(context.Background(), "fixtures", MemberRow{Bytes: 3, ETag: "x"}); err == nil {
		t.Fatal("an empty name was accepted")
	}
}
