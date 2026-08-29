package swiftcontrol

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

func newFixtureSession(t *testing.T) *Session {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDLOSegmentPreview(t *testing.T) {
	s := newFixtureSession(t)
	ctx := context.Background()

	// The canonical DLO has three 1 GB segments.
	full, err := s.DLOSegmentPreview(ctx, "external-dlo-segments/wedding-video/", 10)
	if err != nil {
		t.Fatal(err)
	}
	if full.Container != "external-dlo-segments" || full.Prefix != "wedding-video/" {
		t.Errorf("split = %q / %q", full.Container, full.Prefix)
	}
	if len(full.Segments) != 3 || full.HasMore {
		t.Fatalf("segments = %d hasMore=%v, want all 3", len(full.Segments), full.HasMore)
	}
	if full.Segments[0].Name != "wedding-video/0000000000000001" {
		t.Errorf("first segment = %q (name order matters: it is assembly order)", full.Segments[0].Name)
	}
	if full.PreviewBytes != 3_000_000_000 {
		t.Errorf("PreviewBytes = %d", full.PreviewBytes)
	}

	// Bounded: a smaller limit truncates and reports more.
	bounded, err := s.DLOSegmentPreview(ctx, "external-dlo-segments/wedding-video/", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded.Segments) != 2 || !bounded.HasMore {
		t.Errorf("bounded = %d hasMore=%v, want 2 + more", len(bounded.Segments), bounded.HasMore)
	}

	// A dangling manifest previews as empty, not as an error — the HEAD
	// 404 story (§14.1 note) is the caller's context.
	empty, err := s.DLOSegmentPreview(ctx, "external-dlo-segments/no-such-prefix/", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Segments) != 0 || empty.HasMore {
		t.Errorf("empty prefix = %+v", empty)
	}

	if _, err := s.DLOSegmentPreview(ctx, "nosplit", 10); err == nil {
		t.Error("manifest without container/prefix split accepted")
	}
}
