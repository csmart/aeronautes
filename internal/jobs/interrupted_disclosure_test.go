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

	// A repeat write on the already disclosed row is held to the same rule.
	// This row holds no item records (version 0), which is exactly the row
	// migration 0010's terminal guard does not cover, so only this trigger
	// stands between such a write and a revised outcome. Review found the
	// first version of the guard let it through.
	if v := rowCount(t, s, `SELECT item_records_version FROM jobs WHERE id=?`, job.ID); v != 0 {
		t.Fatalf("this case is meant to exercise a job without item records, version = %d", v)
	}
	for _, tc := range []struct {
		name, set string
	}{
		{"state", `state='complete'`},
		{"counters", `counters_json='{"objectsCopied":99}'`},
		{"error", `error='all fine actually'`},
	} {
		t.Run("repeat "+tc.name, func(t *testing.T) {
			err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx,
					`UPDATE jobs SET interrupt_disclosed_at=?, `+tc.set+` WHERE id=?`,
					time.Now().UTC().Format(time.RFC3339), job.ID)
				return err
			})
			if err == nil {
				t.Fatalf("a repeat disclosure revised the %s and was accepted", tc.name)
			}
		})
	}
	// And the disclosed row is still what the first disclosure left it.
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailed || got.Error == "" {
		t.Fatalf("the refused writes changed the job: state=%s error=%q", got.State, got.Error)
	}
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

// ADR 0088 decision 4: the read returns a bounded, newest-first page of only
// the interruptions the operator has NOT been told about, with a total count
// that stands apart from the page limit. A job that reached a terminal state
// on its own is not an interruption and must not appear.
func TestInterruptedDisclosuresSelectsUndisclosedNewestFirst(t *testing.T) {
	ctx := context.Background()
	s, bm := newStore(t)

	ids := make([]string, 3)
	for i, ts := range []string{
		"2026-10-19T00:00:01Z", "2026-10-19T00:00:02Z", "2026-10-19T00:00:03Z",
	} {
		job, err := s.Create(ctx, Job{Type: TypeDownload, SourceBookmarkID: bm})
		if err != nil {
			t.Fatal(err)
		}
		setCreatedAt(t, s, job.ID, ts)
		ids[i] = job.ID
	}
	ordinary, err := s.Create(ctx, Job{Type: TypeDownload, SourceBookmarkID: bm})
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []State{StatePlanning, StateRunning, StateVerifying, StateComplete} {
		if err := s.Transition(ctx, ordinary.ID, to); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.FailInterrupted(ctx); err != nil {
		t.Fatal(err)
	}

	page, err := s.InterruptedDisclosures(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 {
		t.Fatalf("Total = %d, want 3; the completed job must not count as an interruption", page.Total)
	}
	wantOrder := []string{ids[2], ids[1], ids[0]}
	if got := entryIDs(page); !equalIDs(got, wantOrder) {
		t.Fatalf("page order = %v, want newest-first %v", got, wantOrder)
	}

	// A disclosed interruption leaves the set.
	discloseForTest(t, s, ids[1])
	page, err = s.InterruptedDisclosures(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || !equalIDs(entryIDs(page), []string{ids[2], ids[0]}) {
		t.Fatalf("after disclosing one: Total=%d order=%v, want 2 and [%s %s]", page.Total, entryIDs(page), ids[2], ids[0])
	}

	// The page is bounded while Total keeps counting everything undisclosed.
	page, err = s.InterruptedDisclosures(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("Total = %d, want 2 even under a page limit of 1", page.Total)
	}
	if !equalIDs(entryIDs(page), []string{ids[2]}) {
		t.Fatalf("limited page = %v, want just the newest (%s)", entryIDs(page), ids[2])
	}
}

// ADR 0088 decision 5: each entry reports what the retained facts prove. An
// admitted job carries its item ledger and its recovery residue. A job that
// never left the queue proves nothing was attempted and has no ledger at all.
func TestInterruptedDisclosuresReportsPerEntryFacts(t *testing.T) {
	ctx := context.Background()
	s, bm := newStore(t)

	admitted, err := s.Create(ctx, Job{Type: TypeDownload, SourceBookmarkID: bm, ItemRecordsVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []State{StatePlanning, StateRunning} {
		if err := s.Transition(ctx, admitted.ID, to); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AdmitItems(ctx, admitted.ID, []ItemIntent{downloadItem(bm, "one"), downloadItem(bm, "two")}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []RecoveryRecord{
		{JobID: admitted.ID, BookmarkID: bm, Kind: RecoveryCaptured, Container: "c", Prefix: "p1"},
		{JobID: admitted.ID, BookmarkID: bm, Kind: RecoveryCaptured, Container: "c", Prefix: "p2"},
	} {
		if err := s.RecoveryAdd(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	queued, err := s.Create(ctx, Job{Type: TypeDownload, SourceBookmarkID: bm})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.FailInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := s.InterruptedDisclosures(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}

	a := findEntry(t, page, admitted.ID)
	if a.InterruptedState != StateRunning {
		t.Fatalf("admitted job interrupted_state = %s, want running", a.InterruptedState)
	}
	if !a.ItemsCaptured || a.Items.Items != 2 || a.Items.Admitted != 2 {
		t.Fatalf("admitted item facts = captured:%v items:%d admitted:%d, want true/2/2",
			a.ItemsCaptured, a.Items.Items, a.Items.Admitted)
	}
	if a.RecoveryRecords != 2 {
		t.Fatalf("admitted RecoveryRecords = %d, want 2", a.RecoveryRecords)
	}
	// No execution was recorded, so nothing was attempted yet and the positive
	// facts are zero — a distinct fact from the recovery residue above.
	if a.Attempted || a.Copied != 0 || a.Verified != 0 || a.CopiedBytes != 0 {
		t.Fatalf("admitted-but-unexecuted facts = attempted:%v copied:%d verified:%d bytes:%d, want false/0/0/0",
			a.Attempted, a.Copied, a.Verified, a.CopiedBytes)
	}

	q := findEntry(t, page, queued.ID)
	if q.InterruptedState != StateQueued {
		t.Fatalf("queued job interrupted_state = %s, want queued", q.InterruptedState)
	}
	if q.ItemsCaptured || q.Items.Items != 0 {
		t.Fatalf("queued job must carry no item ledger: captured:%v items:%d", q.ItemsCaptured, q.Items.Items)
	}
	if q.RecoveryRecords != 0 {
		t.Fatalf("queued job residue = recovery:%d, want 0", q.RecoveryRecords)
	}
	if q.Attempted || q.Copied != 0 {
		t.Fatalf("queued job with no ledger must read as not attempted: attempted:%v copied:%d", q.Attempted, q.Copied)
	}
}

// ADR 0088 decision 5: a recorded execution outcome is an
// attempt whatever state the sweep overwrote. A job can record a copied and
// verified member while still queued — a crash before the state change leaves
// interrupted_state queued — and the notice must read the ledger, not the
// state, so it does not claim nothing was attempted beside recorded work. The
// acknowledged copied/verified members and their bytes are carried too.
func TestInterruptedDisclosuresReadsAttemptFromTheLedger(t *testing.T) {
	ctx := context.Background()
	s, bm := newStore(t)

	job, err := s.Create(ctx, Job{Type: TypeDownload, SourceBookmarkID: bm, ItemRecordsVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	item := downloadItem(bm, "obj")
	if err := s.AdmitItems(ctx, job.ID, []ItemIntent{item}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordExecution(ctx, job.ID, item.Key, ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied, CopiedBytes: 2048}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordComparison(ctx, job.ID, item.ItemIdentity, ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FailInterrupted(ctx); err != nil {
		t.Fatal(err)
	}

	page, err := s.InterruptedDisclosures(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(t, page, job.ID)
	if e.InterruptedState != StateQueued {
		t.Fatalf("interrupted_state = %s, want queued (the crash preceded the state change)", e.InterruptedState)
	}
	if !e.Attempted {
		t.Fatal("a queued job with a recorded execution must read as attempted, not nothing-attempted")
	}
	if e.Copied != 1 || e.Verified != 1 || e.CopiedBytes != 2048 {
		t.Fatalf("acknowledged facts = copied:%d verified:%d bytes:%d, want 1/1/2048", e.Copied, e.Verified, e.CopiedBytes)
	}
}

func setCreatedAt(t *testing.T, s *Store, id, ts string) {
	t.Helper()
	ctx := context.Background()
	if err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET created_at=? WHERE id=?`, ts, id)
		return err
	}); err != nil {
		t.Fatalf("set created_at: %v", err)
	}
}

func downloadItem(bm, key string) ItemIntent {
	return ItemIntent{
		ItemIdentity: ItemIdentity{
			Key:         key,
			Source:      ItemLocation{Kind: ItemSwift, BookmarkID: bm, Container: "src", Object: key},
			Destination: ItemLocation{Kind: ItemLocal, LocalPath: "/tmp/aeronautes-test/" + key},
		},
		Planned: true, Admitted: true, ListedBytes: 10,
	}
}

func findEntry(t *testing.T, page InterruptedDisclosurePage, id string) InterruptedDisclosure {
	t.Helper()
	for _, e := range page.Entries {
		if e.JobID == id {
			return e
		}
	}
	t.Fatalf("job %s not in the disclosure page", id)
	return InterruptedDisclosure{}
}

func entryIDs(page InterruptedDisclosurePage) []string {
	ids := make([]string, len(page.Entries))
	for i, e := range page.Entries {
		ids[i] = e.JobID
	}
	return ids
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func deletionItem(bm, key string) ItemIntent {
	return ItemIntent{
		ItemIdentity: ItemIdentity{
			Key:    key,
			Source: ItemLocation{Kind: ItemSwift, BookmarkID: bm, Container: "c", Object: key},
		},
		Planned: true, Admitted: true, ListedBytes: 1,
	}
}

// ADR 0088 decision 5: the attempt signal must exclude the
// synthetic not_attempted rows the sweep writes for never-issued deletion
// members, and must include an issued member with no recorded outcome, whose
// DELETE may have been sent (ADR 0093). A raw count of execution rows gets both
// wrong.
func TestInterruptedDisclosuresAttemptExcludesSyntheticNotAttempted(t *testing.T) {
	ctx := context.Background()

	t.Run("issued member with no outcome counts as attempted", func(t *testing.T) {
		s, bm := newStore(t)
		job, err := s.Create(ctx, Job{Type: TypeDeleteSelected, SourceBookmarkID: bm, ItemRecordsVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AdmitItems(ctx, job.ID, []ItemIntent{deletionItem(bm, "k")}); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkIssued(ctx, job.ID, "k"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.FailInterrupted(ctx); err != nil {
			t.Fatal(err)
		}
		page, err := s.InterruptedDisclosures(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if e := findEntry(t, page, job.ID); !e.Attempted {
			t.Fatal("an issued deletion member with no outcome must read as attempted")
		}
	})

	t.Run("only synthetic not_attempted rows do not count as attempted", func(t *testing.T) {
		s, bm := newStore(t)
		job, err := s.Create(ctx, Job{Type: TypeDeleteSelected, SourceBookmarkID: bm, ItemRecordsVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AdmitItems(ctx, job.ID, []ItemIntent{deletionItem(bm, "a"), deletionItem(bm, "b")}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.FailInterrupted(ctx); err != nil {
			t.Fatal(err)
		}
		// The sweep wrote the synthetic rows, so a naive count would read these
		// as attempts; this test fails for that mistake.
		if n := rowCount(t, s, `SELECT COUNT(*) FROM job_item_observations o JOIN job_items i ON o.item_id=i.id
			WHERE i.job_id=? AND o.phase='execution' AND o.result='not_attempted'`, job.ID); n != 2 {
			t.Fatalf("expected 2 synthetic not_attempted rows, got %d", n)
		}
		page, err := s.InterruptedDisclosures(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if e := findEntry(t, page, job.ID); e.Attempted {
			t.Fatal("never-issued members carrying only synthetic not_attempted rows must not read as attempted")
		}
	})
}
