package systemtest

import (
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

// segmentChangeBody is larger than the admitted one-mebibyte chunk
// setting below, so the engine publishes the destination as a dynamic
// large object through the normal service rather than as a plain copy.
var segmentChangeBody = []byte(strings.Repeat("abcdefg", 160000))

func segmentChangeCopy(t *testing.T, h *Harness, src, dst, name string) (string, *jobs.Counters) {
	t.Helper()
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/",
		Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	copyCompletionAwaitPins(t, h)
	saved := copyCompletionRead(t, h.database, id)
	counters := saved.Job.Counters
	return id, &counters
}

func segmentChangeLive(t *testing.T, h *Harness, dst, name string) oracle.ObjectState {
	t.Helper()
	live, ok := h.SnapshotNamespace(h.Secondary).Containers[dst].Objects[name]
	if !ok {
		t.Fatalf("destination %s/%s is absent", dst, name)
	}
	return live
}

// copySegmentChangeDetection publishes a segmented destination through
// the public service, then proves the skip decision over it: unchanged
// destinations are still reported the same, and a destination whose
// segment bytes were replaced at the same length, or whose publication
// stamp is gone, is refused instead.
func copySegmentChangeDetection(t *testing.T, h *Harness) {
	t.Helper()
	src, dst := h.Container(h.Primary, "segchange-src"), h.Container(h.Secondary, "segchange-dst")
	segments := dst + "_segments"
	h.rawPut(h.Secondary, "/"+segments, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, segments) })
	h.Seed(h.Primary, src, "scope/asset", segmentChangeBody, nil)
	h.Seed(h.Primary, src, "unselected", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, dst, "unselected", []byte("destination sentinel"), nil)
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}

	// Publication: the destination becomes a dynamic large object and
	// carries the aggregate this executor observed on the staged
	// manifest, written by the same conditional request that published
	// the name.
	dstTrace := copyCompletionProxy(t, h, h.Secondary, "")
	if _, counters := segmentChangeCopy(t, h, src, dst, "scope/asset"); counters.ObjectsCopied != 1 {
		t.Fatalf("publication counters = %+v, want one copied", counters)
	}
	live := segmentChangeLive(t, h, dst, "scope/asset")
	if live.Class != "dlo" || len(live.Segments) < 2 || live.Size != int64(len(segmentChangeBody)) {
		t.Fatalf("destination was not published as a segmented object: %+v", live)
	}
	stamp := live.Meta["Aeronautes-Staged-Aggregate"]
	if stamp == "" || !strings.HasPrefix(stamp, "v1:") {
		t.Fatalf("published destination carries no usable publication stamp: %+v", live.Meta)
	}
	liveWrites, metadataWrites := 0, 0
	for _, write := range dstTrace.writeSnapshot() {
		if !strings.HasSuffix(write, "/"+dst+"/scope/asset") {
			continue
		}
		liveWrites++
		if strings.HasPrefix(write, "POST ") {
			metadataWrites++
		}
	}
	if liveWrites != 1 || metadataWrites != 0 {
		t.Fatalf("the live name took %d writes including %d metadata updates: the stamp must ride the single publishing write", liveWrites, metadataWrites)
	}

	// Unchanged: still reported the same, with no mutation at all.
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	unchangedTrace := copyCompletionProxy(t, h, h.Secondary, "")
	id, counters := segmentChangeCopy(t, h, src, dst, "scope/asset")
	if counters.ObjectsVerified != 1 || counters.OverwritesRefused != 0 || counters.ObjectsCopied != 0 {
		t.Fatalf("unchanged run counters = %+v, want one verified and nothing written", counters)
	}
	saved := copyCompletionRead(t, h.database, id)
	if len(saved.Items) != 1 ||
		saved.Items[0].Execution == nil || saved.Items[0].Verification == nil ||
		saved.Items[0].Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemSame, Effect: jobs.EffectNotAttempted}) ||
		saved.Items[0].Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
		t.Fatalf("unchanged run misrepresented the outcome: %+v", saved.Items)
	}
	if writes := unchangedTrace.writeSnapshot(); len(writes) != 0 {
		t.Fatalf("unchanged run mutated the destination: %v", writes)
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))

	// A segment replaced by different bytes of the same length: the
	// aggregate moves, so the destination is refused rather than skipped.
	target := live.Segments[0]
	substitute := []byte(strings.Repeat("z", int(target.Size)))
	segmentName := strings.TrimPrefix(target.Name, segments+"/")
	h.Seed(h.Secondary, segments, segmentName, substitute, nil)
	changed := segmentChangeLive(t, h, dst, "scope/asset")
	if changed.Digest == live.Digest {
		t.Fatal("the substitution did not change the assembled bytes")
	}
	beforeChanged := h.SnapshotNamespace(h.Secondary)
	changedTrace := copyCompletionProxy(t, h, h.Secondary, "")
	id, counters = segmentChangeCopy(t, h, src, dst, "scope/asset")
	if counters.OverwritesRefused != 1 || counters.Overwrites != 0 || counters.ObjectsCopied != 0 || counters.ObjectsVerified != 0 {
		t.Fatalf("substituted run counters = %+v, want exactly one refused overwrite", counters)
	}
	saved = copyCompletionRead(t, h.database, id)
	if saved.Job.State != "partial" || len(saved.Items) != 1 || saved.Items[0].Execution == nil ||
		saved.Items[0].Execution.ItemObservation.Result != jobs.ItemRefused {
		t.Fatalf("substituted run misrepresented the refusal: state=%q items=%+v", saved.Job.State, saved.Items)
	}
	for _, write := range changedTrace.writeSnapshot() {
		if strings.HasSuffix(write, "/"+dst+"/scope/asset") {
			t.Fatalf("the refusal wrote to the live name: %q", write)
		}
	}
	h.AssertNoCollateral(beforeChanged, h.SnapshotNamespace(h.Secondary))

	// A destination whose publication stamp is gone is not evidence
	// either, so it is refused rather than skipped.
	h.Seed(h.Secondary, dst, "scope/asset", nil, map[string]string{"X-Object-Manifest": changed.Manifest})
	if stripped := segmentChangeLive(t, h, dst, "scope/asset"); stripped.Meta["Aeronautes-Staged-Aggregate"] != "" {
		t.Fatalf("the stamp survived the rewrite: %+v", stripped.Meta)
	}
	if _, counters = segmentChangeCopy(t, h, src, dst, "scope/asset"); counters.OverwritesRefused != 1 || counters.ObjectsCopied != 0 {
		t.Fatalf("unstamped run counters = %+v, want exactly one refused overwrite", counters)
	}
}

func TestCopySegmentChangeDetectionMock(t *testing.T) {
	t.Parallel()
	copySegmentChangeDetection(t, findMockHarness(t))
}

func TestCopySegmentChangeDetectionRealSwift(t *testing.T) {
	t.Parallel()
	copySegmentChangeDetection(t, New(t))
}
