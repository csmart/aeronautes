package systemtest

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/mutationfacts"
)

func TestContainerMetadataConflictReceiptMock(t *testing.T) {
	testContainerMetadataConflictReceipt(t, findMockHarness(t))
}

func TestContainerMetadataConflictReceiptSwift(t *testing.T) {
	testContainerMetadataConflictReceipt(t, New(t))
}

func testContainerMetadataConflictReceipt(t *testing.T, h *Harness) {
	t.Helper()
	if h.Secondary == nil {
		t.Skip("container conflict requires two owned accounts")
	}
	container := h.Container(h.Primary, "conflict")
	collateral := h.Container(h.Primary, "collateral")
	// A same-named secondary target makes an account mix-up observable.
	h.rawPut(h.Secondary, "/"+container, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, container) })
	seeds := []struct {
		account   *Account
		container string
		value     string
	}{
		{h.Primary, container, "primary target"},
		{h.Primary, collateral, "primary collateral"},
		{h.Secondary, container, "secondary target"},
	}
	for _, seed := range seeds {
		h.rawPut(seed.account, "/"+seed.container, nil, map[string]string{"X-Container-Meta-Keep": seed.value})
		h.Seed(seed.account, seed.container, "collateral", []byte(seed.value), map[string]string{"X-Object-Meta-Keep": seed.value})
	}
	before := h.SnapshotNamespace(h.Primary)
	secondaryBefore := h.SnapshotNamespace(h.Secondary)
	if len(before.Containers) != 2 || len(secondaryBefore.Containers) != 1 {
		t.Fatalf("seeded namespaces have %d primary and %d secondary containers; want 2 and 1", len(before.Containers), len(secondaryBefore.Containers))
	}
	for _, seed := range seeds {
		snapshot := before
		if seed.account == h.Secondary {
			snapshot = secondaryBefore
		}
		state := snapshot.Containers[seed.container]
		object := state.Objects["collateral"]
		digest := sha256.Sum256([]byte(seed.value))
		if state.Meta["Keep"] != seed.value || object.Meta["Keep"] != seed.value || object.Digest != hex.EncodeToString(digest[:]) {
			t.Fatalf("seeded %s state = %+v; want its distinct metadata and bytes", seed.value, state)
		}
	}
	trace := installContainerFactProxy(t, h, container, "conflict", nil)
	type reply struct {
		dto     app.MutationResultDTO
		receipt *Receipt
	}
	done := make(chan reply, 1)
	go func() {
		dto, receipt := h.UpdateContainer(t.Context(), container, "stale working copy", app.ContainerEditDTO{Meta: map[string]string{"Recorded": "yes"}})
		done <- reply{dto, receipt}
	}()
	var got reply
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("container update did not return")
	}
	if got.receipt.Err() != nil || got.dto.Outcome != "conflict" || got.receipt.Outcome() != "conflict" {
		t.Fatalf("result = %+v, %v; want conflict", got.dto, got.receipt.Err())
	}
	trace.mu.Lock()
	heads, posts := trace.heads, trace.posts
	observedIDs := append([]string(nil), trace.ids...)
	trace.mu.Unlock()
	if heads != 1 || posts != 0 || len(observedIDs) != 1 || observedIDs[0] == "" {
		t.Fatalf("target route: HEADs=%d POSTs=%d IDs=%v; want one observed HEAD and no POST", heads, posts, observedIDs)
	}
	if !reflect.DeepEqual(got.dto.TransIDs, observedIDs) {
		t.Fatalf("returned IDs = %v; observed %v", got.dto.TransIDs, observedIDs)
	}
	h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(secondaryBefore, h.SnapshotNamespace(h.Secondary))
	facts, err := mutationfacts.NewStore(h.database).All(t.Context())
	if err != nil || len(facts) != 1 {
		t.Fatalf("durable facts = %+v, %v; want one conflict", facts, err)
	}
	fact := facts[0]
	if fact.Identity == "" || fact.CreatedAt.IsZero() || fact.DecidedAt.IsZero() || fact.BookmarkID != h.Primary.BookmarkID || fact.Operation != "container.update" || fact.Resource != container || fact.Outcome != mutationfacts.OutcomeConflict || !reflect.DeepEqual(fact.TransIDs, observedIDs) {
		t.Fatalf("durable conflict = %+v; want primary target and observed IDs %v", fact, observedIDs)
	}
	query := audit.Query{OperationPrefix: "container.update"}
	events, err := audit.NewStore(h.database).List(t.Context(), query)
	if err != nil || len(events) != 1 {
		t.Fatalf("audit events = %+v, %v; want one conflict", events, err)
	}
	event := events[0]
	if event.ID <= 0 || event.At.IsZero() || event.BookmarkID != h.Primary.BookmarkID || event.Operation != "container.update" || event.Resource != container || event.Result != audit.ResultConflict || !reflect.DeepEqual(event.TransIDs, observedIDs) {
		t.Fatalf("audit conflict = %+v; want primary target and observed IDs %v", event, observedIDs)
	}
	secondaryEvents, err := audit.NewStore(h.database).List(t.Context(), audit.Query{OperationPrefix: "container.update", BookmarkID: h.Secondary.BookmarkID})
	if err != nil || len(secondaryEvents) != 0 {
		t.Fatalf("secondary audit = %+v, %v; want no container update", secondaryEvents, err)
	}
	if err := h.database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), h.statePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	store := mutationfacts.NewStore(reopened)
	reopenedFact, err := store.Lookup(t.Context(), fact.Identity)
	if err != nil || !reflect.DeepEqual(reopenedFact, fact) {
		t.Fatalf("reopened identity = %+v, %v; want %+v", reopenedFact, err, fact)
	}
	reopenedFacts, err := store.All(t.Context())
	if err != nil || !reflect.DeepEqual(reopenedFacts, facts) {
		t.Fatalf("reopened facts = %+v, %v; want %+v", reopenedFacts, err, facts)
	}
	reopenedEvents, err := audit.NewStore(reopened).List(t.Context(), query)
	if err != nil || !reflect.DeepEqual(reopenedEvents, events) {
		t.Fatalf("reopened audit = %+v, %v; want %+v", reopenedEvents, err, events)
	}
	// A child skip must not return to a parent emitter: this entire case
	// shares the top-level T, including its harness and receipt.
	if t.Failed() {
		return
	}
	got.receipt.CreditConflict()
}
