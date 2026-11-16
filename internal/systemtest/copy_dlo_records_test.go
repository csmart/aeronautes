package systemtest

import (
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

// copySegmentPathRecordRefusal copies two objects the engine writes in
// segments while the record of where it put them cannot be kept. A copy that
// could leave segments nobody can name does not go on (ADR 0122 decision 8),
// but the engine already had both names: each one it reported writing is
// written and is recorded so, and nothing is removed.
func copySegmentPathRecordRefusal(t *testing.T, h *Harness) {
	t.Helper()
	src, dst := h.Container(h.Primary, "dlo-record-src"), h.Container(h.Secondary, "dlo-record-dst")
	aliases := h.Container(h.Secondary, "dlo-record-aliases")
	segments := dst + "_segments"
	h.rawPut(h.Secondary, "/"+segments, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, segments) })
	h.Seed(h.Primary, src, "unselected", []byte("source sentinel"), nil)
	shared := []byte("shared bytes")
	h.Seed(h.Secondary, segments, "shared/0", shared, nil)
	h.Seed(h.Secondary, aliases, "dlo", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	h.SeedSLO(h.Secondary, aliases, "slo", fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(shared), len(shared)))
	body := []byte(strings.Repeat("abcdefg", 160000))
	for _, key := range []string{"a", "b"} {
		h.Seed(h.Primary, src, "scope/"+key, body, nil)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	srcTrace, dstTrace := copyCompletionProxy(t, h, h.Primary, ""), copyCompletionProxy(t, h, h.Secondary, "")
	if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_segment_path BEFORE INSERT ON job_recovery WHEN NEW.kind='captured' BEGIN SELECT RAISE(ABORT,'fixture segment path record refusal'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/", Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"}})
	if err != nil {
		t.Fatal(err)
	}
	copyCompletionAwaitPins(t, h)
	saved := copyCompletionRead(t, h.database, id)
	// What the store holds, read apart from the job: the engine had both
	// names when the record was refused, and wrote both.
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	var allowed []string
	for _, key := range []string{"a", "b"} {
		live := afterB.Containers[dst].Objects["scope/"+key]
		if live.Class != "dlo" || live.Size != int64(len(body)) || live.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) || len(live.Segments) != 2 ||
			!strings.HasPrefix(live.Manifest, segments+"/scope/"+key+"/") {
			t.Fatalf("scope/%s is not the whole object under its own segment path: %+v", key, live)
		}
		allowed = append(allowed, dst+"/scope/"+key)
		for _, segment := range live.Segments {
			allowed = append(allowed, segment.Name)
		}
	}
	wantTotals := jobs.ItemTotals{Items: 2, Planned: 2, Admitted: 2}
	wantCounters := jobs.Counters{ObjectsPlanned: 2, BytesPlanned: int64(2 * len(body)), ObjectsCopied: 2, BytesCopied: int64(2 * len(body)), ObjectsVerified: 2}
	if saved.Job.State != "partial" || saved.Job.Counters != wantCounters || !saved.ItemsCaptured || saved.ItemTotals != wantTotals || len(saved.Items) != 2 ||
		!strings.Contains(saved.Job.Error, "fixture segment path record refusal") {
		t.Fatalf("a refused segment path record lost an outcome or its reason: job=%+v totals=%+v items=%+v", saved.Job, saved.ItemTotals, saved.Items)
	}
	for i, key := range []string{"a", "b"} {
		item := saved.Items[i]
		if item.Key != key || !item.Admitted || item.Source.Object != "scope/"+key || item.Destination.Object != "scope/"+key || item.Staging != nil ||
			item.Execution == nil || item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(body))}) ||
			item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
			t.Fatalf("an object the engine reported writing was not recorded as written: %+v", item)
		}
	}
	// No segment path could be recorded, and none was invented.
	if len(saved.Rows) != 0 || saved.Job.RecoveryRecords != 0 {
		t.Fatalf("a refused segment path record left recovery rows: %+v", saved.Rows)
	}
	wantSummary := "job " + id + " · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems"
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.copy.partial" || saved.Audit[0].Summary != wantSummary ||
		saved.Audit[0].BookmarkID != h.Primary.BookmarkID || saved.Audit[0].Resource != src+"/scope/ → "+dst+"/scope/" {
		t.Fatalf("the terminal audit lost source authority or item facts: %+v", saved.Audit)
	}
	if len(srcTrace.writeSnapshot()) != 0 {
		t.Fatal("the copy changed the source")
	}
	for _, write := range dstTrace.writeSnapshot() {
		if strings.HasPrefix(write, "DELETE ") {
			t.Fatalf("a stopped copy removed something: %q", write)
		}
	}
	h.AssertNoCollateral(beforeA, afterA)
	h.AssertNoCollateral(beforeB, afterB, allowed...)
	requests := srcTrace.requestCount() + dstTrace.requestCount()
	copyCompletionReopen(t, h, id, saved)
	if requests != srcTrace.requestCount()+dstTrace.requestCount() {
		t.Fatal("reopening the records contacted Swift")
	}
}

func TestCopySegmentPathRecordRefusalMock(t *testing.T) {
	copySegmentPathRecordRefusal(t, findMockHarness(t))
}

func TestCopySegmentPathRecordRefusalRealSwift(t *testing.T) {
	copySegmentPathRecordRefusal(t, New(t))
}
