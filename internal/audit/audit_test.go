package audit

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/db"
)

func newStore(t *testing.T, keep int) *Store {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if keep > 0 {
		return NewStoreWithCap(d, keep)
	}
	return NewStore(d)
}

func TestAppendAndRecent(t *testing.T) {
	s := newStore(t, 0)
	ctx := context.Background()

	err := s.Append(ctx, Event{
		BookmarkID: "bm1",
		Operation:  "object.metadata.update",
		Resource:   "media/wedding-video.mp4",
		Summary:    "updated 2 metadata keys, kept 3",
		Result:     ResultApplied,
		TransIDs:   []string{"tx-head", "tx-post", "tx-verify"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, Event{
		Operation: "container.create", Resource: "new-container",
		Summary: "created with policy standard", Result: ResultFailed,
	}); err != nil {
		t.Fatal(err)
	}

	events, err := s.List(ctx, Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("Recent = %d events, want 2", len(events))
	}
	// Newest first.
	if events[0].Operation != "container.create" || events[1].Operation != "object.metadata.update" {
		t.Errorf("order = %s, %s", events[0].Operation, events[1].Operation)
	}
	first := events[1]
	if first.BookmarkID != "bm1" || first.Result != ResultApplied {
		t.Errorf("event = %+v", first)
	}
	if len(first.TransIDs) != 3 || first.TransIDs[1] != "tx-post" {
		t.Errorf("TransIDs = %v", first.TransIDs)
	}
	if first.At.IsZero() || time.Since(first.At) > time.Minute {
		t.Errorf("At = %v", first.At)
	}
}

func TestRetentionCap(t *testing.T) {
	s := newStore(t, 5)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		if err := s.Append(ctx, Event{
			Operation: "op", Resource: fmt.Sprintf("r%02d", i),
			Summary: "s", Result: ResultApplied,
		}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.List(ctx, Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("kept %d events, want cap of 5", len(events))
	}
	if events[0].Resource != "r11" || events[4].Resource != "r07" {
		t.Errorf("kept wrong window: %s .. %s", events[0].Resource, events[4].Resource)
	}
}

// A summary that looks credential-bearing is never stored, and the event it
// belongs to is never lost: its operation, resource and result are facts the
// trail owes regardless of what a name happened to contain.
func TestWithholdsSecretShapedSummariesAndKeepsTheEvent(t *testing.T) {
	s := newStore(t, 0)
	ctx := context.Background()
	cases := map[string]string{
		"set X-Auth-Key to hunter2":            "x-auth-key",
		"updated password field":               "password",
		"rotated api_key":                      "api_key",
		"created 1 directory (passwords/2024)": "password",
	}
	for summary, word := range cases {
		if err := s.Append(ctx, Event{Operation: "op", Resource: "r", Summary: summary, Result: ResultApplied}); err != nil {
			t.Errorf("summary %q lost its event: %v", summary, err)
		}
		if got := SummaryRefusal(summary); got != word {
			t.Errorf("SummaryRefusal(%q) = %q, want %q", summary, got, word)
		}
	}
	events, err := s.List(ctx, Query{Limit: 10})
	if err != nil || len(events) != len(cases) {
		t.Fatalf("events = %d, %v; want one per case", len(events), err)
	}
	for _, event := range events {
		if event.Operation != "op" || event.Resource != "r" || event.Result != ResultApplied {
			t.Errorf("the kept event lost its facts: %+v", event)
		}
		if !strings.HasPrefix(event.Summary, "summary withheld: ") {
			t.Errorf("a refused summary was stored: %q", event.Summary)
		}
		for _, leaked := range []string{"hunter2", "2024", "rotated", "updated"} {
			if strings.Contains(event.Summary, leaked) {
				t.Errorf("the withheld summary kept %q: %q", leaked, event.Summary)
			}
		}
	}
}

func TestRejectsIncompleteEvents(t *testing.T) {
	s := newStore(t, 0)
	if err := s.Append(context.Background(), Event{Operation: "op"}); err == nil {
		t.Error("event without resource/result was accepted")
	}
}

func TestRecentRequiresBookmark(t *testing.T) {
	s := newStore(t, 0)
	_, err := s.Recent(t.Context(), 10)
	if !errors.Is(err, ErrBookmarkRequired) {
		t.Fatalf("unscoped Recent = %v, want ErrBookmarkRequired", err)
	}
}

// ExportTo must read one consistent snapshot across the former page
// boundary: with 501 rows at a cap of 501, an append after the 500th row
// is read prunes the oldest (id 1) while the read cursor is still open
// (WAL lets the writer commit concurrently). A paged read would miss id 1
// on its second page; the single snapshot must still yield it.
func TestExportToSnapshotSurvivesConcurrentRetention(t *testing.T) {
	ctx := context.Background()
	const total = 501
	store := newStore(t, total)
	for range total {
		if err := store.Append(ctx, Event{BookmarkID: "bm", Operation: "op", Resource: "r", Summary: "s", Result: ResultApplied}); err != nil {
			t.Fatal(err)
		}
	}
	var seen []int64
	appended := false
	if err := store.ExportTo(ctx, Query{}, func(e Event) error {
		seen = append(seen, e.ID)
		if len(seen) == 500 && !appended {
			appended = true
			// One more row past the cap prunes the oldest (id 1), committed
			// after the former first page but while this cursor is open.
			return store.Append(ctx, Event{BookmarkID: "bm", Operation: "op", Resource: "r", Summary: "s", Result: ResultApplied})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != total {
		t.Fatalf("export saw %d rows, want the full %d-row snapshot", len(seen), total)
	}
	if oldest := seen[len(seen)-1]; oldest != 1 {
		t.Fatalf("export snapshot lost the oldest row to a concurrent prune: oldest seen is id %d, want 1", oldest)
	}
}
