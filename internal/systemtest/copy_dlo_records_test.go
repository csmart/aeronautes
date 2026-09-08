package systemtest

import (
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

func copyDLORecordRefusal(t *testing.T, h *Harness) {
	t.Helper()
	src, dst := h.Container(h.Primary, "dlo-record-src"), h.Container(h.Secondary, "dlo-record-dst")
	aliases := h.Container(h.Secondary, "dlo-record-aliases")
	segments := dst + "_segments"
	h.rawPut(h.Secondary, "/"+segments, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, segments) })
	h.Seed(h.Primary, src, "unselected", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, dst, "scope/.aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
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
		_, err := tx.Exec(`CREATE TRIGGER refuse_dlo_committed BEFORE INSERT ON job_recovery WHEN NEW.kind='committed' BEGIN SELECT RAISE(ABORT,'fixture committed record refusal'); END`)
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
	wantTotals := jobs.ItemTotals{Items: 2, Planned: 2, Admitted: 2, Unresolved: 2, Unverified: 1, Problems: 1}
	wantCounters := jobs.Counters{ObjectsPlanned: 2, BytesPlanned: int64(2 * len(body)), ObjectsCopied: 1, BytesCopied: int64(len(body))}
	if saved.Job.State != "partial" || saved.Job.Counters != wantCounters || !saved.ItemsCaptured || saved.ItemTotals != wantTotals || len(saved.Items) != 2 {
		t.Fatalf("post-DLO record refusal lost copied bytes or uncertainty: %+v", saved)
	}
	stage := "scope/.aeronautes/stage/" + id + "/"
	for i, key := range []string{"a", "b"} {
		item := saved.Items[i]
		if item.Key != key || !item.Admitted || item.Source.Object != "scope/"+key || item.Destination.Object != "scope/"+key || item.Staging == nil ||
			item.Staging.Object != stage+"batch/"+key || item.Verification != nil {
			t.Fatalf("post-DLO record refusal changed retained intent: %+v", item)
		}
	}
	wantObservation := jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectUnresolved, CopiedBytes: int64(len(body)),
		Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageRecording, Code: jobs.CodeUnknown}}
	if saved.Items[0].Execution == nil || saved.Items[0].Execution.ItemObservation != wantObservation || saved.Items[1].Execution != nil {
		t.Fatalf("post-DLO record refusal invented failure or a later outcome: %+v", saved.Items)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	allowed := []string{dst + "/scope/a", dst + "/" + stage + "batch/a", dst + "/" + stage + "batch/b"}
	wantRows := map[jobs.RecoveryRecord]bool{{JobID: id, BookmarkID: h.Secondary.BookmarkID, Kind: jobs.RecoveryStaging, Container: dst, Prefix: stage}: true}
	for _, key := range []string{"a", "b"} {
		staged := afterB.Containers[dst].Objects[stage+"batch/"+key]
		prefix, ok := strings.CutPrefix(staged.Manifest, segments+"/")
		if !ok || !strings.HasPrefix(prefix, stage+"batch/"+key+"/") || staged.Class != "dlo" || staged.Size != int64(len(body)) ||
			staged.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) || len(staged.Segments) != 2 {
			t.Fatalf("uncertain staged manifest or backing bytes were not retained: %+v", staged)
		}
		prefix = strings.TrimSuffix(prefix, "/") + "/"
		for i, segment := range staged.Segments {
			start, end := i*(1<<20), min((i+1)*(1<<20), len(body))
			if segment.Name != segments+"/"+prefix+fmt.Sprintf("%08d", i) || segment.Size != int64(end-start) || segment.Digest != fmt.Sprintf("%x", sha256.Sum256(body[start:end])) {
				t.Fatalf("retained segment differs from the exact expected bytes: %+v", segment)
			}
			allowed = append(allowed, segment.Name)
		}
		wantRows[jobs.RecoveryRecord{JobID: id, BookmarkID: h.Secondary.BookmarkID, Kind: jobs.RecoveryCaptured, Container: segments, Prefix: prefix}] = true
		if key == "a" {
			live := afterB.Containers[dst].Objects["scope/a"]
			if live.Class != "dlo" || live.Manifest != staged.Manifest || live.Size != staged.Size || live.Digest != staged.Digest || !reflect.DeepEqual(live.Segments, staged.Segments) {
				t.Fatalf("committed-record fault has no independently readable live DLO: %+v", live)
			}
			wantRows[jobs.RecoveryRecord{JobID: id, BookmarkID: h.Secondary.BookmarkID, Kind: jobs.RecoveryCommitting, Container: segments, Prefix: prefix, Object: dst + "/scope/a"}] = true
		} else if _, exists := afterB.Containers[dst].Objects["scope/b"]; exists {
			t.Fatal("later live commit crossed the recovery-record failure")
		}
	}
	if len(saved.Rows) != len(wantRows) {
		t.Fatalf("committed-record failure lost earlier recovery facts: %+v want=%+v", saved.Rows, wantRows)
	}
	for _, row := range saved.Rows {
		if !wantRows[row] {
			t.Fatalf("committed-record failure invented or changed recovery authority: %+v", row)
		}
	}
	wantSummary := "job " + id + " · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 2 unresolved, 1 unverified, 1 with problems"
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.copy.partial" || saved.Audit[0].Summary != wantSummary ||
		saved.Audit[0].BookmarkID != h.Primary.BookmarkID || saved.Audit[0].Resource != src+"/scope/ → "+dst+"/scope/" {
		t.Fatalf("post-DLO record refusal audit lost source authority or item facets: %+v", saved.Audit)
	}
	if len(srcTrace.writeSnapshot()) != 0 {
		t.Fatal("post-DLO record refusal changed the source")
	}
	for _, write := range dstTrace.writeSnapshot() {
		if strings.HasPrefix(write, "DELETE ") || strings.HasPrefix(write, "POST ") {
			t.Fatalf("post-DLO record refusal allowed cleanup or an unrelated mutation: %q", write)
		}
	}
	h.AssertNoCollateral(beforeA, afterA)
	h.AssertNoCollateral(beforeB, afterB, allowed...)
	requests := srcTrace.requestCount() + dstTrace.requestCount()
	if rerun, err := h.Transfer.RerunJob(t.Context(), id); err == nil || rerun != "" || requests != srcTrace.requestCount()+dstTrace.requestCount() {
		t.Fatalf("post-DLO uncertainty permitted a rerun or remote traffic: %q %v", rerun, err)
	}
	copyCompletionReopen(t, h, id, saved)
	if requests != srcTrace.requestCount()+dstTrace.requestCount() {
		t.Fatal("post-DLO record reopening contacted Swift")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
}

func TestCopyDLORecordRefusalMock(t *testing.T) { copyDLORecordRefusal(t, findMockHarness(t)) }

func TestCopyDLORecordRefusalRealSwift(t *testing.T) { copyDLORecordRefusal(t, New(t)) }
