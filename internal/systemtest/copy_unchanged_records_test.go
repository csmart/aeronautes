package systemtest

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

func copyUnchangedRecords(t *testing.T, h *Harness) {
	t.Helper()
	src, dst := h.Container(h.Primary, "unchanged-src"), h.Container(h.Secondary, "unchanged-dst")
	const count = 201
	for i := range count {
		key := fmt.Sprintf("scope/same-%04d", i)
		h.Seed(h.Primary, src, key, []byte("same bytes"), nil)
		h.Seed(h.Secondary, dst, key, []byte("same bytes"), nil)
	}
	h.Seed(h.Primary, src, "unselected", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, dst, "unselected", []byte("destination sentinel"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	srcTrace, dstTrace := copyCompletionProxy(t, h, h.Primary, ""), copyCompletionProxy(t, h, h.Secondary, "")
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/"})
	if err != nil {
		t.Fatal(err)
	}
	copyCompletionAwaitPins(t, h)
	elapsed := time.Since(started)
	saved := copyCompletionRead(t, h.database, id)
	if saved.Job.State != "complete" || saved.Job.Counters != (jobs.Counters{ObjectsVerified: count, ObjectsSame: count}) || !saved.ItemsCaptured ||
		saved.ItemTotals != (jobs.ItemTotals{Items: count}) || len(saved.Items) != count {
		t.Fatalf("unchanged Copy invented a transfer or lost comparison records: %+v", saved)
	}
	for i, item := range saved.Items {
		key := fmt.Sprintf("same-%04d", i)
		if item.Key != key || item.Source.Object != "scope/"+key || item.Source.BookmarkID != h.Primary.BookmarkID || item.Source.Container != src ||
			item.Destination.Object != "scope/"+key || item.Destination.BookmarkID != h.Secondary.BookmarkID || item.Destination.Container != dst ||
			item.Planned || item.Admitted || item.Staging != nil || item.Execution == nil || item.Verification == nil ||
			item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemSame, Effect: jobs.EffectNotAttempted}) ||
			item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
			t.Fatalf("unchanged Copy misrepresented an exact identity or outcome: %+v", item)
		}
	}
	wantSummary := "job " + id + " · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems"
	if len(saved.Rows) != 0 || len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.copy.complete" ||
		saved.Audit[0].Result != audit.ResultApplied || saved.Audit[0].Summary != wantSummary || saved.Audit[0].BookmarkID != h.Primary.BookmarkID ||
		saved.Audit[0].Resource != src+"/scope/ → "+dst+"/scope/" {
		t.Fatalf("unchanged Copy audit or recovery authority differs: %+v", saved)
	}
	if len(srcTrace.writeSnapshot()) != 0 || len(dstTrace.writeSnapshot()) != 0 {
		t.Fatalf("unchanged Copy invoked a mutation: source=%v destination=%v", srcTrace.writeSnapshot(), dstTrace.writeSnapshot())
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	requests := srcTrace.requestCount() + dstTrace.requestCount()
	// A name the two listings show as the same is skipped with nothing sent
	// for it (ADR 0122 decision 4): the listings and the start's own reads
	// are all the two accounts answered, far fewer than one request a name.
	if requests >= count {
		t.Fatalf("an unchanged copy of %d names sent %d requests, so something was sent for the names", count, requests)
	}
	copyCompletionReopen(t, h, id, saved)
	if requests != srcTrace.requestCount()+dstTrace.requestCount() {
		t.Fatal("unchanged record reopening contacted Swift")
	}
	encoded, err := json.Marshal(map[string]any{"names": count, "requests": requests, "acknowledged_item_writes": count * 3, "actual_copy_ms": float64(elapsed) / float64(time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("unchanged_copy_cost=%s", encoded)
}

func TestCopyUnchangedRecordsMock(t *testing.T) {
	t.Parallel()
	copyUnchangedRecords(t, findMockHarness(t))
}

func TestCopyUnchangedRecordsRealSwift(t *testing.T) {
	t.Parallel()
	copyUnchangedRecords(t, New(t))
}
