package jobs

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
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
	return NewStore(d), bm.ID
}

func TestCreateAndGet(t *testing.T) {
	s, bookmark := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, Job{
		Type:             TypeCopy,
		Source:           "Prod AU:media/photos/",
		Destination:      "DR EU:media/photos/",
		SourceBookmarkID: bookmark,
		DestBookmarkID:   bookmark,
		Engine:           "rclone+native",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.State != StateQueued || created.CreatedAt.IsZero() {
		t.Fatalf("created = %+v", created)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != TypeCopy || got.Source != "Prod AU:media/photos/" || got.SourceBookmarkID != bookmark {
		t.Errorf("got = %+v", got)
	}
	if _, err := s.Create(ctx, Job{}); err == nil {
		t.Error("typeless job accepted")
	}
}

func TestStateMachine(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	j, err := s.Create(ctx, Job{Type: TypeCopy})
	if err != nil {
		t.Fatal(err)
	}

	// The legal happy path stamps timestamps at the right edges.
	for _, to := range []State{StatePlanning, StateRunning, StateVerifying, StateComplete} {
		if err := s.Transition(ctx, j.ID, to); err != nil {
			t.Fatalf("to %s: %v", to, err)
		}
	}
	done, _ := s.Get(ctx, j.ID)
	if done.StartedAt.IsZero() || done.FinishedAt.IsZero() {
		t.Errorf("timestamps missing: %+v", done)
	}

	// Terminal is terminal.
	if err := s.Transition(ctx, j.ID, StateRunning); err == nil {
		t.Error("transition out of a terminal state accepted")
	}

	// Skipping Verifying from Running is illegal (§46: verification is
	// not optional).
	j2, _ := s.Create(ctx, Job{Type: TypeCopy})
	_ = s.Transition(ctx, j2.ID, StatePlanning)
	_ = s.Transition(ctx, j2.ID, StateRunning)
	if err := s.Transition(ctx, j2.ID, StateComplete); err == nil || !strings.Contains(err.Error(), "illegal") {
		t.Errorf("Running → Complete = %v, want illegal", err)
	}

	// Cancellation is legal from any non-terminal state.
	if err := s.Transition(ctx, j2.ID, StateCancelled); err != nil {
		t.Errorf("cancel from running: %v", err)
	}

	// ADR 0104: a running job may pause and run again. Its start is its
	// first start, and a paused job can neither verify nor complete, nor go
	// back to planning, without running again.
	j3, _ := s.Create(ctx, Job{Type: TypeUpload})
	for _, to := range []State{StatePlanning, StateRunning, StatePaused} {
		if err := s.Transition(ctx, j3.ID, to); err != nil {
			t.Fatalf("to %s: %v", to, err)
		}
	}
	for _, illegal := range []State{StateVerifying, StateComplete, StatePlanning, StateQueued} {
		if err := s.Transition(ctx, j3.ID, illegal); err == nil || !strings.Contains(err.Error(), "illegal") {
			t.Errorf("Paused → %s = %v, want illegal", illegal, err)
		}
	}
	// The clock's resolution is a second, so the first start is moved to a
	// time a second stamp could not equal.
	first := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET started_at=? WHERE id=?`, first.Format(time.RFC3339), j3.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, j3.ID, StateRunning); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed, _ := s.Get(ctx, j3.ID); resumed.State != StateRunning || !resumed.StartedAt.Equal(first) {
		t.Errorf("a resumed job lost its first start: %+v", resumed)
	}
	if err := s.Transition(ctx, j3.ID, StatePaused); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, j3.ID, StateCancelled); err != nil {
		t.Errorf("cancel from paused: %v", err)
	}
}

func TestCountersAndError(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	j, _ := s.Create(ctx, Job{Type: TypeUpload})

	c := Counters{ObjectsPlanned: 10, ObjectsCopied: 7, BytesCopied: 7 << 20, NativeRouted: 2, Overwrites: 1}
	if err := s.UpdateCounters(ctx, j.ID, c); err != nil {
		t.Fatal(err)
	}
	if err := s.SetError(ctx, j.ID, "one segment refused"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, j.ID)
	if got.Counters != c || got.Error != "one segment refused" {
		t.Errorf("got = %+v", got)
	}
	if err := s.UpdateCounters(ctx, "missing", c); err == nil {
		t.Error("counters on missing job accepted")
	}
}

func TestListFilters(t *testing.T) {
	s, bookmark := newStore(t)
	ctx := context.Background()

	a, _ := s.Create(ctx, Job{Type: TypeCopy, SourceBookmarkID: bookmark})
	b, _ := s.Create(ctx, Job{Type: TypeDownload, DestBookmarkID: bookmark})
	c, _ := s.Create(ctx, Job{Type: TypeUpload})
	_ = s.Transition(ctx, a.ID, StateCancelled)

	all, err := s.List(ctx, Filter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("all = %d, %v", len(all), err)
	}

	scoped, err := s.List(ctx, Filter{BookmarkID: bookmark})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 2 {
		t.Errorf("bookmark filter = %d jobs, want source-or-dest 2", len(scoped))
	}
	for _, j := range scoped {
		if j.ID == c.ID {
			t.Error("unscoped job leaked through the bookmark filter")
		}
	}

	active, err := s.List(ctx, Filter{ActiveOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Errorf("active = %d, want 2 (cancelled excluded)", len(active))
	}
	for _, j := range active {
		if j.ID == a.ID {
			t.Error("terminal job listed as active")
		}
	}
	_ = b
}

func TestFailInterrupted(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	running, _ := s.Create(ctx, Job{Type: TypeCopy})
	_ = s.Transition(ctx, running.ID, StatePlanning)
	_ = s.Transition(ctx, running.ID, StateRunning)
	queued, _ := s.Create(ctx, Job{Type: TypeUpload})
	// A job left paused by a dead process is interrupted like any other
	// (ADR 0104 decision 4): nothing resumes across a restart.
	paused, _ := s.Create(ctx, Job{Type: TypeDownload})
	for _, to := range []State{StatePlanning, StateRunning, StatePaused} {
		_ = s.Transition(ctx, paused.ID, to)
	}
	done, _ := s.Create(ctx, Job{Type: TypeCopy})
	_ = s.Transition(ctx, done.ID, StateCancelled)

	count, err := s.FailInterrupted(ctx)
	if err != nil || count != 3 {
		t.Fatalf("interrupted = %d, %v", count, err)
	}
	for _, id := range []string{running.ID, queued.ID, paused.ID} {
		j, _ := s.Get(ctx, id)
		if j.State != StateFailed || !strings.Contains(j.Error, "interrupted by shutdown") || j.FinishedAt.IsZero() {
			t.Fatalf("job %s = %+v", id, j)
		}
	}
	// Terminal jobs untouched.
	j, _ := s.Get(ctx, done.ID)
	if j.State != StateCancelled || j.Error != "" {
		t.Fatalf("terminal job touched: %+v", j)
	}
	// A clean database sweeps zero.
	if count, _ := s.FailInterrupted(ctx); count != 0 {
		t.Fatalf("second sweep = %d", count)
	}
}
