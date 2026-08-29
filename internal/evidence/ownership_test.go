package evidence

import (
	"context"
	"testing"
)

func TestOwnershipRecordAndQuery(t *testing.T) {
	s, d, bookmark := newStore(t)
	ctx := context.Background()

	if err := s.RecordOwnership(ctx, bookmark, "job-1", "dest_segments", "video/1700000000.123/"); err != nil {
		t.Fatal(err)
	}

	owned, err := s.HasExclusiveOwnership(ctx, bookmark, "dest_segments", "video/1700000000.123/")
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		t.Error("recorded prefix must be exclusively owned")
	}

	// Exact match only (§28.6b): the proof is the unique prefix as
	// written — never a parent, never a child.
	for _, prefix := range []string{"video/", "video/1700000000.123/000001", "video/1700000000.1234/"} {
		owned, err := s.HasExclusiveOwnership(ctx, bookmark, "dest_segments", prefix)
		if err != nil {
			t.Fatal(err)
		}
		if owned {
			t.Errorf("prefix %q must not inherit ownership", prefix)
		}
	}

	// Bookmark scoping: another endpoint owns nothing here.
	owned, err = s.HasExclusiveOwnership(ctx, "other-bookmark", "dest_segments", "video/1700000000.123/")
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Error("ownership leaked across bookmarks")
	}

	// Re-recording is a no-op, not a pile-up.
	if err := s.RecordOwnership(ctx, bookmark, "job-1", "dest_segments", "video/1700000000.123/"); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := d.Reader().QueryRow(`SELECT COUNT(*) FROM segment_ownership`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("rows = %d, want 1", rows)
	}
}

// TestConfirmedNeverPromotesToOwnership pins the §24.1/§28.6b safety
// asymmetry: manifest-reference evidence proves referenced, not
// unshared, and must never look like ownership.
func TestConfirmedNeverPromotesToOwnership(t *testing.T) {
	s, _, bookmark := newStore(t)
	ctx := context.Background()

	if err := s.Record(ctx, bookmark, Observation{
		Container: "ext_segments", Prefix: "video/",
		Confidence: Confirmed, Reason: ObservedDLO, SourceObject: "media/video",
	}); err != nil {
		t.Fatal(err)
	}

	owned, err := s.HasExclusiveOwnership(ctx, bookmark, "ext_segments", "video/")
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Fatal("Confirmed evidence promoted to exclusive ownership — forbidden")
	}
}

func TestOwnershipsListing(t *testing.T) {
	s, _, bookmark := newStore(t)
	ctx := context.Background()

	for i, prefix := range []string{"a/1/", "a/2/", "b/1/"} {
		container := "dest_segments"
		if i == 2 {
			container = "other_segments"
		}
		if err := s.RecordOwnership(ctx, bookmark, "job-1", container, prefix); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.Ownerships(ctx, bookmark, "dest_segments")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Prefix != "a/2/" || list[1].Prefix != "a/1/" {
		t.Errorf("Ownerships = %+v, want the two dest_segments proofs newest first", list)
	}
	if list[0].CreatedAt.IsZero() || list[0].JobID != "job-1" {
		t.Errorf("row = %+v", list[0])
	}
}

func TestOwnershipValidation(t *testing.T) {
	s, _, bookmark := newStore(t)
	ctx := context.Background()
	cases := [][4]string{
		{"", "job", "c", "p/"},
		{bookmark, "", "c", "p/"},
		{bookmark, "job", "", "p/"},
		{bookmark, "job", "c", ""},
	}
	for i, c := range cases {
		if err := s.RecordOwnership(ctx, c[0], c[1], c[2], c[3]); err == nil {
			t.Errorf("case %d: incomplete proof accepted", i)
		}
	}
}
