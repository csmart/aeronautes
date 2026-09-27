package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
)

// ADR 0081 decision 5: a replaced manifest's segment names are durable
// before the replacement is published, because afterwards the manifest that
// held them is gone and a count cannot tell an explicit cleanup which
// objects it would be clearing.

// newStoreAt is newStore with the database path kept, so a record can be
// read back through a store that never saw it written.
func newStoreAt(t *testing.T) (*Store, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	bm, err := bookmarks.NewStore(d, secrets.NewSessionStore()).Save(context.Background(), bookmarks.Bookmark{
		DisplayName: "Test", AuthURL: "https://swift.example/auth/v1.0", Username: "a:u",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(d), bm.ID, path
}

func uploadIntent(bookmark, key string) ItemIntent {
	return ItemIntent{ItemIdentity: ItemIdentity{
		Key:         key,
		Source:      ItemLocation{Kind: ItemLocal, LocalPath: filepath.Join("/tmp", "upload", key)},
		Destination: ItemLocation{Kind: ItemSwift, BookmarkID: bookmark, Container: "destination", Object: key},
	}, Planned: true, Admitted: true, ListedBytes: 10}
}

func replacedManifest(names ...ItemFragment) ItemPredecessor {
	return ItemPredecessor{Present: true, ETag: "ffff", Length: 64,
		LastModified: time.Date(2026, 9, 27, 8, 30, 0, 0, time.UTC), Class: "slo",
		Fragments: names, Counted: len(names), Ranged: 1, Nested: 1, Exact: true}
}

func TestPredecessorNamesAreDurableAndReadBack(t *testing.T) {
	s, bookmark, path := newStoreAt(t)
	job := recordedJob(t, s, TypeUpload)
	intent := uploadIntent(bookmark, "live/taken")
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
		t.Fatal(err)
	}
	// The order is the manifest's order, and a segment name with a slash in
	// it is why a fragment keeps its container and object apart.
	observed := replacedManifest(
		ItemFragment{Container: "destination_segments", Object: "live/taken/0000002"},
		ItemFragment{Container: "destination_segments", Object: "live/taken/0000001"},
		ItemFragment{Container: "elsewhere", Object: "shared/part"})
	if err := s.RecordPredecessor(t.Context(), job.ID, intent.ItemIdentity, observed); err != nil {
		t.Fatal(err)
	}
	read, ok, err := s.ReadPredecessor(t.Context(), job.ID, intent.Key)
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v %v", ok, err)
	}
	if !reflect.DeepEqual(read.Fragments, observed.Fragments) {
		t.Fatalf("the names did not survive: %+v, wrote %+v", read.Fragments, observed.Fragments)
	}
	if read.Named != 3 || read.Counted != 3 || read.Truncated() || !read.Exact ||
		read.ETag != "ffff" || read.Length != 64 || read.Class != "slo" ||
		!read.LastModified.Equal(observed.LastModified) || read.Ranged != 1 || read.Nested != 1 {
		t.Fatalf("the facts did not survive: %+v", read)
	}
	if read.CreatedAt.IsZero() {
		t.Error("the record does not say when it was taken")
	}

	// The same names through a store that never wrote them, on the same file
	// opened again: what the cleanup would read is on disk, not in memory.
	reopened, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	again, ok, err := NewStore(reopened).ReadPredecessor(t.Context(), job.ID, intent.Key)
	if err != nil || !ok || !reflect.DeepEqual(again.Fragments, observed.Fragments) {
		t.Fatalf("reopened read = %+v, ok=%v, %v", again.Fragments, ok, err)
	}

	// A page of items states the same record without carrying its names,
	// because a page is bounded by its item limit and an item is not.
	page, err := s.ReadItems(t.Context(), job.ID, 0, 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].Predecessor == nil {
		t.Fatalf("page = %+v, %v", page, err)
	}
	header := page.Items[0].Predecessor
	if header.Named != 3 || header.Counted != 3 || len(header.Fragments) != 0 ||
		header.Class != "slo" || header.ETag != "ffff" {
		t.Fatalf("the page's header is not the record: %+v", header)
	}
}

func TestPredecessorRecordIsWrittenOnce(t *testing.T) {
	s, bookmark, _ := newStoreAt(t)
	job := recordedJob(t, s, TypeUpload)
	intent := uploadIntent(bookmark, "live/taken")
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
		t.Fatal(err)
	}
	observed := replacedManifest(ItemFragment{Container: "segments", Object: "part"})
	if err := s.RecordPredecessor(t.Context(), job.ID, intent.ItemIdentity, observed); err != nil {
		t.Fatal(err)
	}
	// The identical reading again is the same fact, not a second one.
	if err := s.RecordPredecessor(t.Context(), job.ID, intent.ItemIdentity, observed); err != nil {
		t.Fatalf("an identical reading was refused: %v", err)
	}
	read, ok, err := s.ReadPredecessor(t.Context(), job.ID, intent.Key)
	if err != nil || !ok || read.ETag != "ffff" || read.Named != 1 {
		t.Fatalf("the reading did not stand: %+v ok=%v %v", read, ok, err)
	}
	// A different reading is a conflict: an observation of what was there
	// cannot be revised, because a later reading would be of something else.
	// It latches the store like any other refused item write, so what the
	// row still holds is read directly afterwards.
	different := observed
	different.ETag = "0000"
	if err := s.RecordPredecessor(t.Context(), job.ID, intent.ItemIdentity, different); !errors.Is(err, ErrItemRecordConflict) {
		t.Fatalf("a revised observation was accepted: %v", err)
	}
	var etag string
	if err := s.db.Reader().QueryRowContext(t.Context(),
		`SELECT etag FROM job_item_predecessors`).Scan(&etag); err != nil || etag != "ffff" {
		t.Fatalf("the first reading did not stand: %q %v", etag, err)
	}
	// And the row itself cannot be edited underneath the store.
	err = s.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE job_item_predecessors SET etag='0000'`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Fatalf("an observed predecessor was editable: %v", err)
	}
	err = s.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE job_item_predecessor_fragments SET object='other'`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Fatalf("a recorded fragment name was editable: %v", err)
	}
}

func TestPredecessorRecordRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		admit    bool
		observed func() ItemPredecessor
		want     error
	}{
		{name: "unadmitted item", observed: func() ItemPredecessor { return replacedManifest() }, want: ErrItemNotFound},
		{name: "more names than the cap", admit: true, want: ErrInvalidItemRecord, observed: func() ItemPredecessor {
			names := make([]ItemFragment, MaxRecordedFragments+1)
			for i := range names {
				names[i] = ItemFragment{Container: "segments", Object: fmt.Sprintf("part/%06d", i)}
			}
			return replacedManifest(names...)
		}},
		{name: "a name with no container", admit: true, want: ErrInvalidItemRecord, observed: func() ItemPredecessor {
			return replacedManifest(ItemFragment{Object: "part"})
		}},
		{name: "a container that is really a path", admit: true, want: ErrInvalidItemRecord, observed: func() ItemPredecessor {
			return replacedManifest(ItemFragment{Container: "segments/nested", Object: "part"})
		}},
		{name: "nothing observed, described anyway", admit: true, want: ErrInvalidItemRecord, observed: func() ItemPredecessor {
			return ItemPredecessor{Present: false, ETag: "ffff"}
		}},
		{name: "fewer counted than named", admit: true, want: ErrInvalidItemRecord, observed: func() ItemPredecessor {
			observed := replacedManifest(ItemFragment{Container: "segments", Object: "part"})
			observed.Counted = 0
			return observed
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, bookmark, _ := newStoreAt(t)
			job := recordedJob(t, s, TypeUpload)
			intent := uploadIntent(bookmark, "live/taken")
			if tc.admit {
				if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
					t.Fatal(err)
				}
			}
			// A refused record is also a refused write, which latches the
			// store as every other item write does, so the absence of the
			// row is read directly rather than through the latched store.
			if err := s.RecordPredecessor(t.Context(), job.ID, intent.ItemIdentity, tc.observed()); !errors.Is(err, tc.want) {
				t.Fatalf("refusal = %v, want %v", err, tc.want)
			}
			for _, table := range []string{"job_item_predecessors", "job_item_predecessor_fragments"} {
				var rows int
				if err := s.db.Reader().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&rows); err != nil || rows != 0 {
					t.Errorf("a refused record left %s rows: %d %v", table, rows, err)
				}
			}
			// The same refusal is available before the write, so a caller can
			// decline without latching anything.
			if tc.want == ErrInvalidItemRecord && tc.observed().Validate() == nil {
				t.Error("the record validates although the store refused it")
			}
		})
	}
}

// A record whose count exceeds its names is what the cap produces, and it
// says so rather than reporting the shorter list as the whole of it.
func TestPredecessorRecordCarriesACountBeyondItsNames(t *testing.T) {
	s, bookmark, _ := newStoreAt(t)
	job := recordedJob(t, s, TypeUpload)
	intent := uploadIntent(bookmark, "live/taken")
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
		t.Fatal(err)
	}
	names := make([]ItemFragment, MaxRecordedFragments)
	for i := range names {
		names[i] = ItemFragment{Container: "segments", Object: fmt.Sprintf("part/%06d", i)}
	}
	observed := replacedManifest(names...)
	observed.Counted = MaxRecordedFragments + 4096
	observed.Prefix = "segments/part/"
	observed.Exact = false
	if err := s.RecordPredecessor(t.Context(), job.ID, intent.ItemIdentity, observed); err != nil {
		t.Fatal(err)
	}
	read, ok, err := s.ReadPredecessor(t.Context(), job.ID, intent.Key)
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v %v", ok, err)
	}
	if read.Named != MaxRecordedFragments || read.Counted != MaxRecordedFragments+4096 || !read.Truncated() {
		t.Fatalf("the record does not say what it is missing: named=%d counted=%d", read.Named, read.Counted)
	}
	// The prefix is what makes the truncated case recoverable: it stays
	// listable after the replacement.
	if read.Prefix != "segments/part/" || read.Exact {
		t.Fatalf("the truncated record lost its prefix or claimed to be exact: %+v", read)
	}
	if len(read.Fragments) != MaxRecordedFragments || read.Fragments[0].Object != "part/000000" {
		t.Fatalf("the names it does hold are not the ones it read: %d", len(read.Fragments))
	}
}
