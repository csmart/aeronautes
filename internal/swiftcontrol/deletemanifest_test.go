package swiftcontrol

import (
	"context"
	"errors"
	"testing"
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

	before, err := s.DeleteManifestObject(ctx, "fixtures", "external-dlo")
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
	before, err := s.DeleteManifestObject(ctx, "fixtures", "external-slo")
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

	if _, err := s.DeleteManifestObject(ctx, "fixtures", "empty-object"); err == nil {
		t.Fatal("plain object accepted by the manifest-only delete")
	}
	// And nothing was deleted.
	if _, err := s.ObjectDetail(ctx, "fixtures", "empty-object"); err != nil {
		t.Errorf("refused object was deleted anyway: %v", err)
	}

	var se *SwiftError
	if _, err := s.DeleteManifestObject(ctx, "fixtures", "missing"); !errors.As(err, &se) || se.Kind != KindNotFound {
		t.Errorf("missing object = %v, want not_found", err)
	}
}
