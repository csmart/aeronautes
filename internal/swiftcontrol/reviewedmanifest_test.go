package swiftcontrol

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// reviewedSLO reads the live static manifest and returns the reviewed graph a
// segment job would carry: the manifest's assembled identity and its
// whole-object entries as resolved.
func reviewedSLO(t *testing.T, s *Session, container, name string) ReviewedManifest {
	t.Helper()
	detail, err := s.ObjectDetail(context.Background(), container, name)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := s.SLOManifest(context.Background(), container, name)
	if err != nil {
		t.Fatal(err)
	}
	entries, whole := manifest.WholeObjectEntries()
	if !whole {
		t.Fatalf("fixture %s/%s is not a whole-object static manifest", container, name)
	}
	return ReviewedManifest{Class: ClassSLO, Fingerprint: detail.ContentFingerprint(), Entries: entries}
}

// ADR 0095 decision 8: a reviewed static manifest whose identity and graph
// still match is deleted manifest-only and the deletion is applied; the DELETE
// is a plain one, never the segment-expanding form.
func TestDeleteReviewedManifestAppliesWhenTheGraphMatches(t *testing.T) {
	s, mock := newFixtureSessionWithMock(t)
	ctx := context.Background()
	reviewed := reviewedSLO(t, s, "fixtures", "external-slo")
	result, err := s.DeleteReviewedManifest(ctx, "fixtures", "external-slo", reviewed)
	if err != nil {
		t.Fatalf("delete = %v", err)
	}
	if result.Outcome != UpdateApplied {
		t.Fatalf("outcome = %q, want applied", result.Outcome)
	}
	for _, line := range mock.RequestLog() {
		if strings.Contains(line, "multipart-manifest=delete") {
			t.Fatalf("a segment-expanding DELETE was sent: %s", line)
		}
	}
	// The segments live on: the manifest delete is manifest-only.
	if _, err := s.ObjectDetail(ctx, "slo-parts", "backup/part-000001"); err != nil {
		t.Fatalf("a segment was removed by a manifest-only delete: %v", err)
	}
}

// ADR 0095 decision 8: a static manifest whose graph was swapped for one of
// equal assembled identity between the review and the execution is refused as
// changed since reviewed, with nothing deleted — the equal-tag swap defence.
func TestDeleteReviewedManifestRefusesAnEqualTagGraphSwap(t *testing.T) {
	s, mock := newFixtureSessionWithMock(t)
	ctx := context.Background()
	reviewed := reviewedSLO(t, s, "fixtures", "external-slo")
	// Swap the graph for different segments while keeping the manifest's
	// assembled identity (the SLO ETag the listing reports) unchanged.
	mock.AddContainer("slo-parts", "standard")
	swapped := `[
		{"name": "/slo-parts/other/seg-1", "hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01", "bytes": 1000000000, "content_type": "application/octet-stream"},
		{"name": "/slo-parts/other/seg-2", "hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02", "bytes": 2100000000, "content_type": "application/octet-stream"}
	]`
	mock.PutObject("fixtures", "external-slo", swifttest.Object{
		Bytes: 1024, ContentType: "application/x-tar", SLO: true,
		SLOEtag: "9f2b8c6e4d2a1f2b7e9d3c8a4b1f0e21", SLOManifestJSON: swapped,
	})
	result, err := s.DeleteReviewedManifest(ctx, "fixtures", "external-slo", reviewed)
	var conflict *IdentityConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want an identity conflict for the swapped graph", err)
	}
	if result == nil || result.Requested || result.Outcome != "" {
		t.Fatalf("result = %+v, want nothing requested", result)
	}
	if _, err := s.ObjectDetail(ctx, "fixtures", "external-slo"); err != nil {
		t.Fatalf("the manifest was deleted despite the graph swap: %v", err)
	}
}

// ADR 0095 decision 8: a dynamic manifest whose namespace moved between the
// review and the execution is refused, with nothing deleted.
func TestDeleteReviewedManifestRefusesAMovedDLONamespace(t *testing.T) {
	s, mock := newFixtureSessionWithMock(t)
	ctx := context.Background()
	detail, err := s.ObjectDetail(ctx, "fixtures", "external-dlo")
	if err != nil {
		t.Fatal(err)
	}
	reviewed := ReviewedManifest{Class: ClassDLO, Fingerprint: detail.ContentFingerprint(), Namespace: "external-dlo-segments/wedding-video/"}
	// The manifest now points at a different namespace, same assembled identity.
	mock.PutObject("fixtures", "external-dlo", swifttest.Object{
		Bytes: 0, ContentType: "video/mp4", ETag: detail.ETag, Manifest: "external-dlo-segments/honeymoon-video/",
	})
	result, err := s.DeleteReviewedManifest(ctx, "fixtures", "external-dlo", reviewed)
	var conflict *IdentityConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want a conflict for the moved namespace", err)
	}
	if result == nil || result.Requested {
		t.Fatalf("result = %+v, want nothing requested", result)
	}
	if _, err := s.ObjectDetail(ctx, "fixtures", "external-dlo"); err != nil {
		t.Fatalf("the manifest was deleted despite the namespace move: %v", err)
	}
}
