package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// ADR 0088's first claim: the sweep retains the state it overwrote. The four
// non-terminal states are asserted individually rather than as a set, because
// the decision rests on one distinction in particular — 'planning' against
// 'queued' — which a set assertion would not notice losing.
func TestFailInterruptedRetainsTheStateItOverwrote(t *testing.T) {
	ctx := context.Background()
	for _, want := range []State{StateQueued, StatePlanning, StateRunning, StateVerifying} {
		t.Run(string(want), func(t *testing.T) {
			s, bm := newStore(t)
			job, err := s.Create(ctx, Job{Type: TypeDownload, SourceBookmarkID: bm})
			if err != nil {
				t.Fatal(err)
			}
			for _, to := range pathTo(want) {
				if err := s.Transition(ctx, job.ID, to); err != nil {
					t.Fatalf("transition to %s: %v", to, err)
				}
			}
			if n, err := s.FailInterrupted(ctx); err != nil || n != 1 {
				t.Fatalf("FailInterrupted = %d, %v", n, err)
			}
			at, state := interruptionFacts(t, s, job.ID)
			if state != string(want) {
				t.Fatalf("interrupted_state = %q, want %q — the sweep did not retain what it overwrote", state, want)
			}
			if at == "" {
				t.Fatal("interrupted_at is empty, so nothing identifies this job as interrupted by a restart")
			}
			got, err := s.Get(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != StateFailed {
				t.Fatalf("state = %s, want failed; the classification itself must not change", got.State)
			}
		})
	}
}

// ADR 0088 decision 2: the notice returns until it is answered, which rests on
// the retention exemption and not on the timestamps. The control is the same
// job with the disclosure recorded, which MUST prune — otherwise this test
// would pass for a prune that never deletes anything.
func TestPruningKeepsAnUndisclosedInterruptionAndReleasesADisclosedOne(t *testing.T) {
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	s, bm := newStore(t)
	job, err := s.Create(ctx, Job{Type: TypeDownload, SourceBookmarkID: bm})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FailInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	// A queued download holds neither recovery nor item rows, which is why
	// today's exclusions do not already protect it.
	if n := rowCount(t, s, `SELECT COUNT(*) FROM job_recovery WHERE job_id=?`, job.ID); n != 0 {
		t.Fatalf("this case is meant to hold no recovery rows, holds %d", n)
	}
	if n := rowCount(t, s, `SELECT COUNT(*) FROM job_items WHERE job_id=?`, job.ID); n != 0 {
		t.Fatalf("this case is meant to hold no item rows, holds %d", n)
	}
	if n, err := s.PruneTerminal(ctx, future); err != nil || n != 0 {
		t.Fatalf("prune deleted %d undisclosed interruptions (%v); the notice could not return", n, err)
	}
	if _, err := s.Get(ctx, job.ID); err != nil {
		t.Fatalf("the job is gone, so its notice is gone: %v", err)
	}

	discloseForTest(t, s, job.ID)
	if n, err := s.PruneTerminal(ctx, future); err != nil || n != 1 {
		t.Fatalf("prune = %d, %v; a disclosed interruption must return to ordinary retention", n, err)
	}
}

// ADR 0088 decision 3: a write that records the telling must not revise the
// outcome being told about. Enforced in the schema, so this asserts the schema
// and not the caller's good manners.
func TestDisclosureCannotReviseTheOutcome(t *testing.T) {
	ctx := context.Background()
	s, bm := newStore(t)
	job, err := s.Create(ctx, Job{Type: TypeDownload, SourceBookmarkID: bm})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FailInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, set string
	}{
		{"state", `state='complete'`},
		{"counters", `counters_json='{"objectsCopied":99}'`},
		{"error", `error='all fine actually'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx,
					`UPDATE jobs SET interrupt_disclosed_at=?, `+tc.set+` WHERE id=?`,
					time.Now().UTC().Format(time.RFC3339), job.ID)
				return err
			})
			if err == nil {
				t.Fatalf("a disclosure revised the %s and was accepted", tc.name)
			}
		})
	}
	// The control: the same write WITHOUT revising anything must succeed, or
	// the test above would pass for a trigger that refuses every disclosure.
	discloseForTest(t, s, job.ID)
}

// An interruption is a time AND the state it overwrote; neither alone is a
// fact the notice can use.
func TestAnInterruptionCannotBeHalfRecorded(t *testing.T) {
	ctx := context.Background()
	s, bm := newStore(t)
	job, err := s.Create(ctx, Job{Type: TypeDownload, SourceBookmarkID: bm})
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range []string{
		`interrupted_at='2026-10-19T00:00:00Z'`,
		`interrupted_state='queued'`,
		`interrupt_disclosed_at='2026-10-19T00:00:00Z'`,
	} {
		if err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE jobs SET `+set+` WHERE id=?`, job.ID)
			return err
		}); err == nil {
			t.Fatalf("accepted a half-recorded interruption: %s", set)
		}
	}
}

func pathTo(target State) []State {
	order := []State{StatePlanning, StateRunning, StateVerifying}
	if target == StateQueued {
		return nil
	}
	var path []State
	for _, s := range order {
		path = append(path, s)
		if s == target {
			break
		}
	}
	return path
}

func interruptionFacts(t *testing.T, s *Store, id string) (at, state string) {
	t.Helper()
	var a, st sql.NullString
	if err := s.db.Reader().QueryRow(
		`SELECT interrupted_at, interrupted_state FROM jobs WHERE id=?`, id).Scan(&a, &st); err != nil {
		t.Fatal(err)
	}
	return a.String, st.String
}

func rowCount(t *testing.T, s *Store, query, id string) int {
	t.Helper()
	var n int
	if err := s.db.Reader().QueryRow(query, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func discloseForTest(t *testing.T, s *Store, id string) {
	t.Helper()
	n, err := s.DiscloseInterrupted(context.Background(), []string{id})
	if err != nil || n != 1 {
		t.Fatalf("an ordinary disclosure was refused: %d %v", n, err)
	}
}

// DiscloseInterrupted's own contract. The count is of rows it actually
// disclosed, so a dismissal racing another is idempotent rather than an error,
// and an id that was never interrupted is skipped rather than refused — a
// notice the operator answers may have been pruned or never have existed.
func TestDiscloseInterruptedCountsOnlyWhatItDisclosed(t *testing.T) {
	ctx := context.Background()
	s, bm := newStore(t)
	interrupted, err := s.Create(ctx, Job{Type: TypeDownload, SourceBookmarkID: bm})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FailInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	// A job that reached a terminal state on its own was never interrupted.
	ordinary, err := s.Create(ctx, Job{Type: TypeDownload, SourceBookmarkID: bm})
	if err != nil {
		t.Fatal(err)
	}
	// running -> complete is not a legal exit (§46): verifying sits between.
	for _, to := range []State{StatePlanning, StateRunning, StateVerifying, StateComplete} {
		if err := s.Transition(ctx, ordinary.ID, to); err != nil {
			t.Fatal(err)
		}
	}

	if n, err := s.DiscloseInterrupted(ctx, nil); err != nil || n != 0 {
		t.Fatalf("empty dismissal = %d, %v; want 0 and no error", n, err)
	}
	if n, err := s.DiscloseInterrupted(ctx, []string{ordinary.ID, "no-such-job"}); err != nil || n != 0 {
		t.Fatalf("disclosing a never-interrupted id = %d, %v; want 0 and no error", n, err)
	}
	if n, err := s.DiscloseInterrupted(ctx, []string{interrupted.ID, ordinary.ID}); err != nil || n != 1 {
		t.Fatalf("mixed dismissal = %d, %v; want exactly the interrupted one", n, err)
	}
	if n, err := s.DiscloseInterrupted(ctx, []string{interrupted.ID}); err != nil || n != 0 {
		t.Fatalf("repeat dismissal = %d, %v; want 0 and no error, not a second disclosure", n, err)
	}
	// And the disclosure did not touch the job itself.
	got, err := s.Get(ctx, interrupted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailed || got.Error == "" {
		t.Fatalf("the disclosure revised the job: state=%s error=%q", got.State, got.Error)
	}
}
