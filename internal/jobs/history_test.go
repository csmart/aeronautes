package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestHistoryReachesOlderJobsWithoutShiftingPages(t *testing.T) {
	for _, filtered := range []bool{false, true} {
		t.Run(fmt.Sprint(filtered), func(t *testing.T) {
			s, bookmark := newStore(t)
			ctx := t.Context()
			var want []Job
			for i := range 237 {
				job := Job{Type: TypeCopy}
				if i%3 == 0 {
					job.SourceBookmarkID = bookmark
				} else if i%3 == 1 {
					job.DestBookmarkID = bookmark
				}
				job, err := s.Create(ctx, job)
				if err != nil {
					t.Fatal(err)
				}
				// Each timestamp group crosses a page boundary, including filtered pages.
				job.CreatedAt = time.Date(2000, 1, 1+i/120, 0, 0, 0, 0, time.UTC)
				if err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `UPDATE jobs SET created_at=? WHERE id=?`, job.CreatedAt.Format(time.RFC3339), job.ID)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if !filtered || i%3 != 2 {
					want = append(want, job)
				}
			}
			sort.Slice(want, func(i, j int) bool {
				if want[i].CreatedAt.Equal(want[j].CreatedAt) {
					return want[i].ID > want[j].ID
				}
				return want[i].CreatedAt.After(want[j].CreatedAt)
			})
			filter := ""
			if filtered {
				filter = bookmark
			}
			var got []Job
			cursor := ""
			for pageNumber := 0; ; pageNumber++ {
				if pageNumber > 10 {
					t.Fatal("history did not advance")
				}
				page, err := s.History(ctx, filter, cursor, 0)
				if err != nil || len(page.Jobs) > 50 {
					t.Fatalf("history page = %+v, %v", page, err)
				}
				got = append(got, page.Jobs...)
				if page.NextCursor == "" {
					break
				}
				if len(page.Jobs) != 50 || page.NextCursor != page.Jobs[49].ID {
					t.Fatalf("invalid continuation: %+v", page)
				}
				cursor = page.NextCursor
				if pageNumber == 0 {
					if _, err := s.Create(ctx, Job{Type: TypeCopy, SourceBookmarkID: bookmark}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("older jobs were reordered, duplicated, lost or shifted: got %d jobs, want %d", len(got), len(want))
			}
			first, err := s.History(ctx, filter, "", 100)
			if err != nil || len(first.Jobs) != 100 || !first.Jobs[0].CreatedAt.After(want[0].CreatedAt) {
				t.Fatalf("fresh first page did not show new job: %+v, %v", first, err)
			}
		})
	}
}

func TestHistoryRefusesInvalidCursorsAndUnavailableReads(t *testing.T) {
	s, bookmark := newStore(t)
	ctx := t.Context()
	job, err := s.Create(ctx, Job{Type: TypeCopy})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		filter, cursor string
		limit          int
	}{
		{"", "", -1}, {"", "", 101}, {"", "missing", 1},
		{bookmark, job.ID, 1}, {"", "' OR 1=1 --", 1},
	} {
		page, err := s.History(ctx, test.filter, test.cursor, test.limit)
		if !errors.Is(err, ErrInvalidHistoryPage) || page.Jobs != nil || page.NextCursor != "" {
			t.Fatalf("invalid history request returned data: %+v, %v", page, err)
		}
	}
	page, err := s.History(ctx, bookmark, "", 1)
	if err != nil || page.Jobs == nil || len(page.Jobs) != 0 || page.NextCursor != "" {
		t.Fatalf("empty account history = %+v, %v", page, err)
	}
	if err := s.Transition(ctx, job.ID, StateCancelled); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneTerminal(ctx, time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("prune = %d, %v", n, err)
	}
	if page, err := s.History(ctx, "", job.ID, 1); !errors.Is(err, ErrInvalidHistoryPage) || page.Jobs != nil {
		t.Fatalf("pruned cursor claimed an empty page: %+v, %v", page, err)
	}
	for range 3 {
		if _, err := s.Create(ctx, Job{Type: TypeCopy}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET counters_json='private-provider-payload' WHERE id=(SELECT MIN(id) FROM jobs)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if page, err := s.History(ctx, "", "", 50); err != ErrHistoryUnavailable || page.Jobs != nil || page.NextCursor != "" {
		t.Fatalf("decode failure returned a partial page or provider error: %+v, %v", page, err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if page, err := s.History(ctx, "", "", 50); err != ErrHistoryUnavailable || page.Jobs != nil {
		t.Fatalf("closed database returned history: %+v, %v", page, err)
	}
}

func TestHistoryRefusesRecordingLatch(t *testing.T) {
	s, _ := newStore(t)
	job := recordedJob(t, s, TypeCopy)
	if err := s.AdmitItems(t.Context(), job.ID, nil); err == nil {
		t.Fatal("invalid required write did not trip the recording latch")
	}
	if page, err := s.History(t.Context(), "", "", 1); err != ErrHistoryUnavailable || page.Jobs != nil {
		t.Fatalf("latched store returned history: %+v, %v", page, err)
	}
}

func TestHistoryRefusesCorruptCursorTimestamp(t *testing.T) {
	s, _ := newStore(t)
	job, err := s.Create(t.Context(), Job{Type: TypeCopy})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE jobs SET created_at='not-a-time' WHERE id=?`, job.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if page, err := s.History(t.Context(), "", job.ID, 1); err != ErrHistoryUnavailable || page.Jobs != nil {
		t.Fatalf("corrupt cursor timestamp returned data: %+v, %v", page, err)
	}
}
