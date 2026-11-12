package evidence

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func newStore(t *testing.T) (*Store, *db.DB, string) {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	// segment_evidence is bookmark-scoped (FK); create a real bookmark.
	bm, err := bookmarks.NewStore(d, secrets.NewSessionStore()).Save(context.Background(), bookmarks.Bookmark{
		DisplayName: "Test", AuthURL: "https://swift.example/auth/v1.0", Username: "a:u",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(d), d, bm.ID
}

func countRows(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.Reader().QueryRow(`SELECT COUNT(*) FROM segment_evidence`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecordUpsertsInsteadOfPiling(t *testing.T) {
	s, d, bookmark := newStore(t)
	ctx := context.Background()
	obs := Observation{
		Container: "media_segments", Prefix: "video/",
		Confidence: Confirmed, Reason: ObservedDLO, SourceObject: "media/big.mp4",
	}
	if err := s.Record(ctx, bookmark, obs); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(ctx, bookmark, obs); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, d); n != 1 {
		t.Errorf("rows = %d, want 1 (repeat observation must update, not insert)", n)
	}

	var first, last string
	err := d.Reader().QueryRow(`SELECT first_seen, last_seen FROM segment_evidence`).Scan(&first, &last)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, first); err != nil {
		t.Errorf("first_seen %q not RFC3339", first)
	}
	if last < first {
		t.Errorf("last_seen %q before first_seen %q", last, first)
	}

	// A different source is separate evidence.
	obs.SourceObject = "media/other.mp4"
	if err := s.Record(ctx, bookmark, obs); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, d); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
}

func TestFromObjectDetail(t *testing.T) {
	dlo := &swiftcontrol.ObjectDetail{
		Name:     "media/big.mp4",
		Class:    swiftcontrol.ClassDLO,
		Manifest: "media_segments/video/",
	}
	obs := FromObjectDetail(dlo)
	if len(obs) != 1 || obs[0].Container != "media_segments" || obs[0].Prefix != "video/" ||
		obs[0].Confidence != Confirmed || obs[0].Reason != ObservedDLO ||
		obs[0].SourceObject != "media/big.mp4" {
		t.Errorf("obs = %+v", obs)
	}

	if got := FromObjectDetail(&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassPlain}); got != nil {
		t.Errorf("plain object produced evidence: %+v", got)
	}
	// The header is percent-encoded; the prefix confirmed is the decoded
	// one the store lists (F37).
	if got := FromObjectDetail(&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassDLO, Manifest: "media_segments/my%20video/"}); len(got) != 1 || got[0].Prefix != "my video/" {
		t.Errorf("encoded manifest = %+v, want the decoded prefix", got)
	}
	if got := FromObjectDetail(&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassDLO, Manifest: "nosplit"}); got != nil {
		t.Errorf("unsplittable manifest produced evidence: %+v", got)
	}
}

func TestFromSLOManifest(t *testing.T) {
	etag := "e"
	m := &swiftcontrol.SLOManifest{Entries: []swiftcontrol.SLOEntry{
		swiftcontrol.ObjectSegment{Path: swiftcontrol.ObjectRef{Container: "parts", Object: "backup/p1"}, ETag: &etag},
		swiftcontrol.ObjectSegment{Path: swiftcontrol.ObjectRef{Container: "parts", Object: "backup/p2"}},
		swiftcontrol.ObjectSegment{Path: swiftcontrol.ObjectRef{Container: "parts", Object: "loose"}},
		swiftcontrol.InlineDataSegment{Data: []byte("x")},
		swiftcontrol.RawEntry{Fields: map[string]any{"mystery": true}},
	}}
	obs := FromSLOManifest("media/archive.tar", m)
	if len(obs) != 2 {
		t.Fatalf("obs = %+v, want 2 distinct (container, prefix) pairs", obs)
	}
	if obs[0].Container != "parts" || obs[0].Prefix != "backup/" || obs[0].Reason != ObservedSLO {
		t.Errorf("obs[0] = %+v", obs[0])
	}
	if obs[1].Prefix != "" {
		t.Errorf("obs[1] = %+v, want container-root prefix", obs[1])
	}
}

func TestClassifyContainers(t *testing.T) {
	s, _, bookmark := newStore(t)
	ctx := context.Background()

	// Observed evidence for one store; a declared pin for another.
	if err := s.Record(ctx, bookmark, Observation{
		Container: "external-dlo-segments", Prefix: "wedding-video/",
		Confidence: Confirmed, Reason: ObservedDLO, SourceObject: "fixtures/external-dlo",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(ctx, bookmark, Observation{
		Container: "pinned_store", Confidence: Declared, Reason: UserPin,
	}); err != nil {
		t.Fatal(err)
	}

	classes, err := s.ClassifyContainers(ctx, bookmark, []string{
		"fixtures",
		"external-dlo-segments",
		"pinned_store",
		"photos_segments", // the trap: named like a store, no evidence
		".file-segments",
		"segments-of-society",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Class{
		"fixtures":              ClassNormal,
		"external-dlo-segments": ClassConfirmed,
		"pinned_store":          ClassDeclared,
		"photos_segments":       ClassSuspected,
		".file-segments":        ClassSuspected,
		"segments-of-society":   ClassNormal,
	}
	for name, wantClass := range want {
		if classes[name] != wantClass {
			t.Errorf("%s = %q, want %q", name, classes[name], wantClass)
		}
	}

	// The heuristic never persists anything (§24.3: query-time only).
	var heuristicRows int
	d := s.db.Reader()
	if err := d.QueryRow(`SELECT COUNT(*) FROM segment_evidence WHERE reason='naming_heuristic'`).Scan(&heuristicRows); err != nil {
		t.Fatal(err)
	}
	if heuristicRows != 0 {
		t.Errorf("naming heuristic wrote %d rows; it must stay query-time", heuristicRows)
	}

	// Evidence scoping: a different bookmark sees none of it.
	classesOther, err := s.ClassifyContainers(ctx, "no-such-bookmark", []string{"external-dlo-segments"})
	if err != nil {
		t.Fatal(err)
	}
	if classesOther["external-dlo-segments"] != ClassSuspected {
		t.Errorf("other bookmark = %q, want heuristic-only suspected", classesOther["external-dlo-segments"])
	}
}

func TestOwnershipCanonicalization(t *testing.T) {
	// F-03's normalization half: rows store slash-terminated; a query in
	// rclone's unslashed header form must still match, and vice versa.
	s, _, bookmark := newStore(t)
	ctx := context.Background()
	if err := s.RecordOwnership(ctx, bookmark, "job-1", "parts", "video.bin/gen"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"video.bin/gen", "video.bin/gen/"} {
		proven, err := s.HasExclusiveOwnership(ctx, bookmark, "parts", q)
		if err != nil || !proven {
			t.Fatalf("query %q = %v %v, want proven", q, proven, err)
		}
	}
	owned, err := s.Ownerships(ctx, bookmark, "parts")
	if err != nil || len(owned) != 1 || owned[0].Prefix != "video.bin/gen/" {
		t.Fatalf("stored form = %+v %v, want canonical slash-terminated", owned, err)
	}
}

func TestExclusiveOwnershipRequiresSingleJob(t *testing.T) {
	// The second clause (recorded half): a prefix two jobs both recorded
	// is shared between Aeronautes jobs and is NOT exclusive.
	s, _, bookmark := newStore(t)
	ctx := context.Background()
	if err := s.RecordOwnership(ctx, bookmark, "job-a", "parts", "shared/gen/"); err != nil {
		t.Fatal(err)
	}
	proven, err := s.HasExclusiveOwnership(ctx, bookmark, "parts", "shared/gen/")
	if err != nil || !proven {
		t.Fatalf("single-job proof = %v %v, want proven", proven, err)
	}
	// A second job records the SAME prefix → no longer exclusive.
	if err := s.RecordOwnership(ctx, bookmark, "job-b", "parts", "shared/gen/"); err != nil {
		t.Fatal(err)
	}
	proven, err = s.HasExclusiveOwnership(ctx, bookmark, "parts", "shared/gen/")
	if err != nil {
		t.Fatal(err)
	}
	if proven {
		t.Fatal("a prefix recorded by two jobs was still reported exclusive")
	}
}
