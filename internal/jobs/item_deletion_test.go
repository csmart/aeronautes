package jobs

import (
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
)

func auditTrail(t *testing.T, store *Store) *audit.Store {
	t.Helper()
	return audit.NewStore(store.db)
}

func memberOf(bookmark, name string) ItemIntent {
	return ItemIntent{ItemIdentity: ItemIdentity{Key: name,
		Source: ItemLocation{Kind: ItemSwift, BookmarkID: bookmark, Container: "c", Object: name},
	}, Planned: true, Admitted: true, ListedBytes: 3}
}

// deletionJob is a running selected-set deletion with its members admitted.
func deletionJob(t *testing.T, store *Store, bookmark string, names ...string) Job {
	t.Helper()
	ctx := t.Context()
	job, err := store.Create(ctx, Job{Type: TypeDeleteSelected, ItemRecordsVersion: 1, SourceBookmarkID: bookmark, Source: "bookmark:c"})
	if err != nil {
		t.Fatal(err)
	}
	members := make([]ItemIntent, 0, len(names))
	for _, name := range names {
		members = append(members, memberOf(bookmark, name))
	}
	if err := store.AdmitItems(ctx, job.ID, members); err != nil {
		t.Fatal(err)
	}
	for _, state := range []State{StatePlanning, StateRunning} {
		if err := store.Transition(ctx, job.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	return job
}

func itemsOf(t *testing.T, store *Store, jobID string) map[string]ItemRecord {
	t.Helper()
	page, err := store.ReadItems(t.Context(), jobID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	byKey := make(map[string]ItemRecord, len(page.Items))
	for _, item := range page.Items {
		byKey[item.Key] = item
	}
	return byKey
}

// ADR 0093 decision 4: a deletion member is its source alone, and only a
// selected-set deletion keeps such members; the transfer phases are not its.
func TestDeletionMembersAreTheirSourceAlone(t *testing.T) {
	t.Run("a deletion refuses a member with a destination", func(t *testing.T) {
		store, bookmark := newStore(t)
		job := deletionJob(t, store, bookmark, "a.txt")
		transfer := memberOf(bookmark, "b.txt")
		transfer.Destination = ItemLocation{Kind: ItemSwift, BookmarkID: bookmark, Container: "d", Object: "b.txt"}
		if err := store.AdmitItems(t.Context(), job.ID, []ItemIntent{transfer}); !errors.Is(err, ErrInvalidItemRecord) {
			t.Fatalf("admitted a transfer item into a deletion: %v", err)
		}
	})
	t.Run("a transfer refuses a source-only item", func(t *testing.T) {
		store, bookmark := newStore(t)
		job, err := store.Create(t.Context(), Job{Type: TypeCopy, ItemRecordsVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AdmitItems(t.Context(), job.ID, []ItemIntent{memberOf(bookmark, "a.txt")}); !errors.Is(err, ErrInvalidItemRecord) {
			t.Fatalf("admitted a source-only item into a copy: %v", err)
		}
	})
	t.Run("the other deletion jobs keep no items", func(t *testing.T) {
		store, _ := newStore(t)
		if _, err := store.Create(t.Context(), Job{Type: TypeDeletePrefix, ItemRecordsVersion: 1}); !errors.Is(err, ErrInvalidItemRecord) {
			t.Fatalf("a prefix deletion was given item records: %v", err)
		}
	})
	t.Run("the transfer phases refuse a deletion", func(t *testing.T) {
		store, bookmark := newStore(t)
		job := deletionJob(t, store, bookmark, "a.txt")
		deleted := ItemObservation{Result: ItemDeleted, Effect: EffectReportedApplied}
		if err := store.RecordExecution(t.Context(), job.ID, "a.txt", deleted); !errors.Is(err, ErrInvalidItemRecord) {
			t.Fatalf("an execution record was accepted for a deletion member: %v", err)
		}
	})
	t.Run("a comparison cannot discover a deletion member", func(t *testing.T) {
		store, bookmark := newStore(t)
		job := deletionJob(t, store, bookmark, "a.txt")
		seen := ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}
		if err := store.RecordComparison(t.Context(), job.ID, memberOf(bookmark, "z.txt").ItemIdentity, seen); !errors.Is(err, ErrInvalidItemRecord) {
			t.Fatalf("a comparison record was accepted for a deletion: %v", err)
		}
	})
}

// ADR 0039 and ADR 0093 decision 4: the issued mark is written once, before
// the member's final HEAD, is the same fact when written again, and cannot
// follow the member's outcome.
func TestIssuedMarkIsWrittenOnceAndNeverAfterAnOutcome(t *testing.T) {
	store, bookmark := newStore(t)
	ctx := t.Context()
	job := deletionJob(t, store, bookmark, "a.txt", "b.txt")
	if err := store.MarkIssued(ctx, job.ID, "a.txt"); err != nil {
		t.Fatal(err)
	}
	first := itemsOf(t, store, job.ID)["a.txt"].IssuedAt
	if first.IsZero() {
		t.Fatal("the issued mark was not recorded")
	}
	if err := store.MarkIssued(ctx, job.ID, "a.txt"); err != nil {
		t.Fatal(err)
	}
	items := itemsOf(t, store, job.ID)
	if !items["a.txt"].IssuedAt.Equal(first) || !items["b.txt"].IssuedAt.IsZero() {
		t.Fatalf("issued marks moved or spread: %+v", items)
	}
	if err := store.RecordDeletion(ctx, job.ID, "a.txt", ItemObservation{Result: ItemDeleted, Effect: EffectReportedApplied}, &ItemSuccessor{}); err != nil {
		t.Fatal(err)
	}
	// An issued member that settled was issued: saying so again is the same fact.
	if err := store.MarkIssued(ctx, job.ID, "a.txt"); err != nil {
		t.Fatalf("the issued mark of a settled member is not the same fact: %v", err)
	}
	// A member that settled without ever being issued, as a cancellation
	// leaves the members after it, cannot be issued afterwards.
	if err := store.RecordDeletion(ctx, job.ID, "b.txt", ItemObservation{Result: ItemNotAttempted, Effect: EffectNotAttempted}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkIssued(ctx, job.ID, "b.txt"); !errors.Is(err, ErrItemRecordConflict) {
		t.Fatalf("a member not attempted was issued after its outcome: %v", err)
	}
}

// ADR 0093 decisions 3 and 4: the outcome and what the verifying read
// observed are one record, the same reading again is idempotent and another
// is a conflict, and only a member whose DELETE was sent can have a successor.
func TestDeletionOutcomeAndSuccessorAreOneRecord(t *testing.T) {
	plain := &ItemSuccessor{Present: true, Class: "plain", Length: 5, ETag: "1111"}
	t.Run("a deleted member with a successor", func(t *testing.T) {
		store, bookmark := newStore(t)
		ctx := t.Context()
		job := deletionJob(t, store, bookmark, "a.txt")
		deleted := ItemObservation{Result: ItemDeleted, Effect: EffectReportedApplied}
		for range 2 {
			if err := store.RecordDeletion(ctx, job.ID, "a.txt", deleted, plain); err != nil {
				t.Fatal(err)
			}
		}
		item := itemsOf(t, store, job.ID)["a.txt"]
		if item.Execution == nil || item.Execution.Result != ItemDeleted || item.Successor == nil || !item.Successor.sameAs(*plain) || item.Successor.CreatedAt.IsZero() {
			t.Fatalf("the record does not carry the outcome and the successor: %+v", item)
		}
		other := *plain
		other.ETag = "2222"
		if err := store.RecordDeletion(ctx, job.ID, "a.txt", deleted, &other); !errors.Is(err, ErrItemRecordConflict) {
			t.Fatalf("a second, different successor was accepted: %v", err)
		}
	})
	t.Run("an unverified member keeps what it still observed", func(t *testing.T) {
		store, bookmark := newStore(t)
		ctx := t.Context()
		job := deletionJob(t, store, bookmark, "m.bin")
		unverified := ItemObservation{Result: ItemUnverified, Effect: EffectUnresolved, Diagnostic: ItemDiagnostic{Stage: StageVerification, Code: CodeUnverified}}
		manifest := &ItemSuccessor{Present: true, Class: "slo", Length: 0, ETag: "3333"}
		if err := store.RecordDeletion(ctx, job.ID, "m.bin", unverified, manifest); err != nil {
			t.Fatal(err)
		}
		if item := itemsOf(t, store, job.ID)["m.bin"]; item.Execution.Effect != EffectUnresolved || item.Successor == nil || item.Successor.Class != "slo" {
			t.Fatalf("the surviving manifest was not kept with the uncertainty: %+v", item)
		}
		if err := store.RecordDeletion(ctx, job.ID, "m.bin", unverified, nil); !errors.Is(err, ErrItemRecordConflict) {
			t.Fatalf("the observation was dropped on a repeat: %v", err)
		}
	})
	t.Run("absence is an observation too", func(t *testing.T) {
		store, bookmark := newStore(t)
		job := deletionJob(t, store, bookmark, "a.txt")
		if err := store.RecordDeletion(t.Context(), job.ID, "a.txt", ItemObservation{Result: ItemDeleted, Effect: EffectReportedApplied}, &ItemSuccessor{}); err != nil {
			t.Fatal(err)
		}
		if item := itemsOf(t, store, job.ID)["a.txt"]; item.Successor == nil || item.Successor.Present {
			t.Fatalf("absence was not recorded: %+v", item)
		}
	})
	t.Run("a refused member observed nothing after an effect", func(t *testing.T) {
		store, bookmark := newStore(t)
		job := deletionJob(t, store, bookmark, "a.txt")
		refused := ItemObservation{Result: ItemRefused, Effect: EffectNotAttempted, Diagnostic: ItemDiagnostic{Stage: StageSourceRead, Code: CodeUnsupported}}
		if err := store.RecordDeletion(t.Context(), job.ID, "a.txt", refused, plain); !errors.Is(err, ErrInvalidItemRecord) {
			t.Fatalf("a refusal was given a successor: %v", err)
		}
	})
	t.Run("a deleted member reports its effect", func(t *testing.T) {
		store, bookmark := newStore(t)
		job := deletionJob(t, store, bookmark, "a.txt")
		if err := store.RecordDeletion(t.Context(), job.ID, "a.txt", ItemObservation{Result: ItemDeleted, Effect: EffectNotAttempted}, nil); !errors.Is(err, ErrInvalidItemRecord) {
			t.Fatalf("a deletion with no effect was accepted: %v", err)
		}
	})
}

func settleFour(t *testing.T, store *Store, bookmark string) Job {
	t.Helper()
	ctx := t.Context()
	job := deletionJob(t, store, bookmark, "a.txt", "b.txt", "c.txt", "d.txt")
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := store.MarkIssued(ctx, job.ID, name); err != nil {
			t.Fatal(err)
		}
	}
	outcomes := map[string]ItemObservation{
		"a.txt": {Result: ItemDeleted, Effect: EffectReportedApplied},
		"b.txt": {Result: ItemUnverified, Effect: EffectUnresolved, Diagnostic: ItemDiagnostic{Stage: StageCommit, Code: CodeUnverified}},
		"c.txt": {Result: ItemConflict, Effect: EffectNotAttempted, Diagnostic: ItemDiagnostic{Stage: StageSourceRead, Code: CodeConflict}},
		"d.txt": {Result: ItemNotAttempted, Effect: EffectNotAttempted},
	}
	for name, outcome := range outcomes {
		var successor *ItemSuccessor
		if name == "a.txt" {
			successor = &ItemSuccessor{}
		}
		if err := store.RecordDeletion(ctx, job.ID, name, outcome, successor); err != nil {
			t.Fatal(name, err)
		}
	}
	return job
}

// ADR 0093 decision 4: the totals count what the members became, an
// unverified or conflicting member refuses completion, and the terminal
// summary carries them.
func TestDeletionTotalsDeriveTheEnd(t *testing.T) {
	t.Run("totals", func(t *testing.T) {
		store, bookmark := newStore(t)
		job := settleFour(t, store, bookmark)
		totals, err := store.ReadItemTotals(t.Context(), job.ID)
		want := ItemTotals{Items: 4, Planned: 4, Admitted: 4, Conflicts: 1, Unresolved: 1, Unverified: 1, Problems: 2, Issued: 3, Deleted: 1}
		if err != nil || totals != want {
			t.Fatalf("totals = %+v, %v; want %+v", totals, err, want)
		}
	})
	t.Run("complete is refused", func(t *testing.T) {
		store, bookmark := newStore(t)
		job := settleFour(t, store, bookmark)
		if err := store.FinishFromItems(t.Context(), job.ID, itemCompletion(job, StateComplete), auditTrail(t, store)); !errors.Is(err, ErrIncompleteItemRecords) {
			t.Fatalf("a job with an unverified and a conflicting member completed: %v", err)
		}
	})
	t.Run("partial is recorded", func(t *testing.T) {
		store, bookmark := newStore(t)
		job := settleFour(t, store, bookmark)
		if err := store.FinishFromItems(t.Context(), job.ID, itemCompletion(job, StatePartial), auditTrail(t, store)); err != nil {
			t.Fatal(err)
		}
		if got, err := store.Get(t.Context(), job.ID); err != nil || got.State != StatePartial {
			t.Fatalf("job = %+v, %v", got, err)
		}
	})
	t.Run("every member deleted completes", func(t *testing.T) {
		store, bookmark := newStore(t)
		ctx := t.Context()
		job := deletionJob(t, store, bookmark, "a.txt", "b.txt")
		for _, name := range []string{"a.txt", "b.txt"} {
			if err := store.MarkIssued(ctx, job.ID, name); err != nil {
				t.Fatal(err)
			}
			if err := store.RecordDeletion(ctx, job.ID, name, ItemObservation{Result: ItemDeleted, Effect: EffectReportedApplied}, &ItemSuccessor{}); err != nil {
				t.Fatal(err)
			}
		}
		// The machine never lets a job skip Verifying on its way to complete;
		// a deletion's verifying reads are per member, and the step is kept.
		if err := store.Transition(ctx, job.ID, StateVerifying); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishFromItems(ctx, job.ID, itemCompletion(job, StateComplete), auditTrail(t, store)); err != nil {
			t.Fatal(err)
		}
		if totals, err := store.ReadItemTotals(ctx, job.ID); err != nil || totals.Deleted != 2 || totals.Issued != 2 || !totals.permitsComplete() {
			t.Fatalf("totals = %+v, %v", totals, err)
		}
	})
}

// ADR 0093 decision 4: a deletion interrupted by a shutdown is failed on
// reopen with its facts kept: an outcome stays, an issued member with none is
// unresolved because its DELETE may have been sent, and a member never
// issued was not attempted.
func TestInterruptedDeletionKeepsItsMembersHonest(t *testing.T) {
	store, bookmark := newStore(t)
	ctx := t.Context()
	job := deletionJob(t, store, bookmark, "a.txt", "b.txt", "c.txt")
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := store.MarkIssued(ctx, job.ID, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordDeletion(ctx, job.ID, "a.txt", ItemObservation{Result: ItemDeleted, Effect: EffectReportedApplied}, &ItemSuccessor{}); err != nil {
		t.Fatal(err)
	}
	if n, err := store.FailInterrupted(ctx); err != nil || n != 1 {
		t.Fatalf("interrupted = %d, %v", n, err)
	}
	got, err := store.Get(ctx, job.ID)
	if err != nil || got.State != StateFailed || got.Counters.ObjectsDeleted != 1 || got.Counters.ObjectsFailed != 0 {
		t.Fatalf("job = %+v, %v", got, err)
	}
	items := itemsOf(t, store, job.ID)
	if items["a.txt"].Execution == nil || items["a.txt"].Execution.Result != ItemDeleted {
		t.Fatalf("the deleted member lost its outcome: %+v", items["a.txt"])
	}
	if items["b.txt"].Execution != nil || items["b.txt"].IssuedAt.IsZero() {
		t.Fatalf("the issued member was given an outcome it never had: %+v", items["b.txt"])
	}
	if items["c.txt"].Execution == nil || items["c.txt"].Execution.Result != ItemNotAttempted || !items["c.txt"].IssuedAt.IsZero() {
		t.Fatalf("the member never issued is not shown as not attempted: %+v", items["c.txt"])
	}
	totals, err := store.ReadItemTotals(ctx, job.ID)
	if err != nil || totals.Unresolved != 1 || totals.Deleted != 1 || totals.Issued != 2 {
		t.Fatalf("totals = %+v, %v", totals, err)
	}
}
