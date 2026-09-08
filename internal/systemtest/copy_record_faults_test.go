package systemtest

import (
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

func copyRecordFault(t *testing.T, h *Harness, mode string) {
	t.Helper()
	src := h.Container(h.Primary, "record-fault-src")
	dst := h.Container(h.Secondary, "record-fault-dst")
	segments := h.Container(h.Secondary, "record-fault-segments")
	aliases := h.Container(h.Secondary, "record-fault-aliases")
	first := "a\nnative"
	second := "b\nnative"
	if mode == "admission" || mode == "staged_execution" {
		first = "a-engine"
	}
	if mode == "staged_execution" {
		second = "b-engine"
	}
	for _, key := range []string{first, second, "z-engine"} {
		h.Seed(h.Primary, src, "scope/"+key, []byte("copy bytes"), nil)
	}
	h.Seed(h.Primary, src, "unselected", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, dst, "scope/.aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	shared := []byte("shared bytes")
	h.Seed(h.Secondary, segments, "shared/0", shared, nil)
	h.Seed(h.Secondary, aliases, "dlo", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	h.SeedSLO(h.Secondary, aliases, "slo", fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(shared), len(shared)))
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	srcTrace := copyCompletionProxy(t, h, h.Primary, "")
	var barrier *swifttest.HTTPResponseBarrier
	var dstTrace *uploadDeleteTrace
	if mode != "admission" {
		storage, err := url.Parse(h.Secondary.storageURL)
		if err != nil {
			t.Fatal(err)
		}
		selected := &url.URL{Path: storage.Path + "/" + dst + "/scope/" + first}
		barrier, err = swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
			Operation: t.Name(), Method: http.MethodPut, EscapedPath: selected.EscapedPath(), Occurrence: 1}, false, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		dstTrace = copyCompletionProxy(t, h, h.Secondary, "", barrier)
	} else {
		dstTrace = copyCompletionProxy(t, h, h.Secondary, "")
	}
	if mode == "admission" {
		if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TRIGGER refuse_copy_item BEFORE INSERT ON job_items WHEN NEW.item_key='b'||char(10)||'native' BEGIN SELECT RAISE(ABORT,'fixture item admission refusal'); END`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	req := app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/"}
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	id, err := h.Transfer.StartCopy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if barrier != nil {
		event, err := barrier.Wait(t.Context())
		if err != nil || event.StatusCode != http.StatusCreated {
			t.Fatalf("native PUT response boundary: %+v %v", event, err)
		}
		witness := h.SnapshotNamespace(h.Secondary)
		object := witness.Containers[dst].Objects["scope/"+first]
		if object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("copy bytes"))) || object.Size != 10 {
			t.Fatalf("native effect not independently observed before SQL refusal: %+v", object)
		}
		if mode == "staged_execution" {
			page, err := h.jobStore.ReadItems(t.Context(), id, 0, 100)
			if err != nil || len(page.Items) != 3 {
				t.Fatalf("engine batch lacks acknowledged admissions: %+v %v", page, err)
			}
			for _, item := range page.Items {
				if !item.Admitted || item.Execution != nil || item.Staging == nil ||
					witness.Containers[dst].Objects[item.Staging.Object].Size != 10 {
					t.Fatalf("engine-started name lost its admission or staging before the fault: %+v", item)
				}
			}
		}
		if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error {
			query := `CREATE TRIGGER refuse_copy_item BEFORE INSERT ON job_item_observations WHEN NEW.phase='execution' BEGIN SELECT RAISE(ABORT,'fixture execution record refusal'); END`
			if mode == "verification" {
				query = `CREATE TRIGGER refuse_copy_item BEFORE INSERT ON job_item_observations WHEN NEW.phase='verification' AND EXISTS (SELECT 1 FROM job_items WHERE id=NEW.item_id AND item_key='b'||char(10)||'native') BEGIN SELECT RAISE(ABORT,'fixture verification record refusal'); END`
			}
			_, err := tx.Exec(query)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := barrier.Release(); err != nil {
			t.Fatal(err)
		}
	}
	copyCompletionAwaitPins(t, h)
	assertCopyRecordFault(t, h, mode, id, first, src, dst, beforeA, beforeB, srcTrace, dstTrace)
}

func assertCopyRecordFault(t *testing.T, h *Harness, mode, id, first, src, dst string, beforeA, beforeB *oracle.AccountSnapshot, srcTrace, dstTrace *uploadDeleteTrace) {
	t.Helper()
	saved := copyCompletionRead(t, h.database, id)
	wantState := "failed"
	wantTotals := jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1, Problems: 1}
	wantCounters := jobs.Counters{ObjectsPlanned: 2, BytesPlanned: 20}
	var allowed []string
	wantWrites := 0
	if mode != "admission" {
		wantState = "partial"
		wantTotals.Problems, wantTotals.Unresolved = 0, 1
		wantCounters = jobs.Counters{ObjectsPlanned: 1, BytesPlanned: 10, ObjectsCopied: 1, BytesCopied: 10, NativeRouted: 1}
		allowed = []string{dst + "/scope/" + first}
		wantWrites = 1
	}
	if mode == "staged_execution" {
		wantTotals.Items, wantTotals.Planned, wantTotals.Admitted, wantTotals.Unresolved = 3, 3, 3, 3
		wantCounters.ObjectsPlanned, wantCounters.BytesPlanned, wantCounters.NativeRouted = 3, 30, 0
		allowed = append(allowed, dst+"/scope/.aeronautes/stage/"+id+"/batch/b-engine", dst+"/scope/.aeronautes/stage/"+id+"/batch/z-engine")
		wantWrites = 5
	}
	if mode == "verification" {
		wantTotals = jobs.ItemTotals{Items: 3, Planned: 3, Admitted: 3, Unverified: 2}
		wantCounters = jobs.Counters{ObjectsPlanned: 3, BytesPlanned: 30, ObjectsCopied: 3, BytesCopied: 30, NativeRouted: 2, ObjectsVerified: 2}
		allowed = append(allowed, dst+"/scope/b\nnative", dst+"/scope/z-engine")
		wantWrites = 5
	}
	if saved.Job.State != wantState || saved.Job.Counters != wantCounters || saved.Job.FinishedAt == "" ||
		!saved.ItemsCaptured || saved.ItemTotals != wantTotals || int64(len(saved.Items)) != wantTotals.Items || saved.Items[0].Key != first {
		t.Fatalf("record refusal lost acknowledged facts or reported effects: %+v want=%s %+v %+v", saved, wantState, wantCounters, wantTotals)
	}
	item := saved.Items[0]
	if item.Source.Object != "scope/"+first || item.Destination.Object != "scope/"+first || !item.Admitted || (mode != "verification" && item.Verification != nil) {
		t.Fatalf("record refusal fabricated or changed identity: %+v", item)
	}
	if mode == "admission" {
		if item.Staging == nil || item.Staging.Object != "scope/.aeronautes/stage/"+id+"/batch/"+first || item.Execution == nil ||
			item.Execution.Result != jobs.ItemNotAttempted || item.Execution.Effect != jobs.EffectNotAttempted || item.Execution.Diagnostic.Code != jobs.CodeIncomplete {
			t.Fatalf("failed admission lost the acknowledged unstarted batch: %+v", item)
		}
	} else if mode == "execution" && (item.Staging != nil || item.Execution != nil) {
		t.Fatalf("refused post-effect observation was invented: %+v", item)
	}
	if mode == "staged_execution" {
		for i, key := range []string{first, "b-engine", "z-engine"} {
			item := saved.Items[i]
			if item.Key != key || item.Execution != nil || item.Verification != nil || !item.Admitted || item.Staging == nil ||
				item.Staging.Object != "scope/.aeronautes/stage/"+id+"/batch/"+key {
				t.Fatalf("stopped staged commit lost intent or invented final facts: %+v", item)
			}
		}
	}
	if mode == "verification" {
		for i, key := range []string{first, "b\nnative", "z-engine"} {
			item := saved.Items[i]
			if item.Key != key || !item.Admitted || item.Execution == nil ||
				item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: 10}) ||
				(i == 0 && (item.Verification == nil || item.Verification.Result != jobs.ItemVerified)) || (i > 0 && item.Verification != nil) {
				t.Fatalf("verification refusal lost earlier facts or invented later verification: %+v", item)
			}
		}
	}
	wantSummary := fmt.Sprintf("job %s · item outcomes: 0 failed names, 0 conflicts, 0 refusals, %d unresolved, %d unverified, %d with problems", id, wantTotals.Unresolved, wantTotals.Unverified, wantTotals.Problems)
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.copy."+wantState || saved.Audit[0].Summary != wantSummary ||
		saved.Audit[0].BookmarkID != h.Primary.BookmarkID || saved.Audit[0].Resource != src+"/scope/ → "+dst+"/scope/" {
		t.Fatalf("record refusal audit lost facets or source authority: %+v", saved.Audit)
	}
	wantRows := []jobs.RecoveryRecord{{JobID: id, BookmarkID: h.Secondary.BookmarkID, Kind: jobs.RecoveryStaging,
		Container: dst, Prefix: "scope/.aeronautes/stage/" + id + "/"}}
	if !reflect.DeepEqual(saved.Rows, wantRows) {
		t.Fatalf("record refusal changed retained recovery facts: %+v", saved.Rows)
	}
	if visible, err := h.Jobs.Get(t.Context(), id); err == nil || visible != nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("record refusal exposed stale job status: %+v %v", visible, err)
	}
	if items, err := h.Jobs.Items(t.Context(), id, "", 100); err == nil || items.Items != nil {
		t.Fatalf("record refusal exposed a partial public ledger: %+v %v", items, err)
	}
	if totals, err := h.Jobs.ItemTotals(t.Context(), id); err == nil || totals != (app.JobItemTotalsDTO{}) {
		t.Fatalf("record refusal invented public totals: %+v %v", totals, err)
	}
	for _, kind := range []jobs.Type{jobs.TypeCopy, jobs.TypeUpload, jobs.TypeDownload, jobs.TypeMirror, jobs.TypeMirrorPreview, jobs.TypeDeletePrefix, jobs.TypeEmpty, jobs.TypeDeleteCont} {
		if job, err := h.jobStore.Create(t.Context(), jobs.Job{Type: kind}); !errors.Is(err, jobs.ErrItemRecordsUnavailable) || job.ID != "" {
			t.Fatalf("actual recording failure admitted new %s work: %+v %v", kind, job, err)
		}
	}
	requests := srcTrace.requestCount() + dstTrace.requestCount()
	if rerun, err := h.Transfer.RerunJob(t.Context(), id); err == nil || rerun != "" || requests != srcTrace.requestCount()+dstTrace.requestCount() {
		t.Fatalf("unavailable records allowed a rerun or connection traffic: %q %v", rerun, err)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeA, afterA)
	if changed := collateral(beforeB, afterB, allowed); len(changed) != 0 || len(srcTrace.writeSnapshot()) != 0 || len(dstTrace.writeSnapshot()) != wantWrites {
		t.Fatalf("record failure crossed a dependent effect: collateral=%v source=%v destination=%v", changed, srcTrace.writeSnapshot(), dstTrace.writeSnapshot())
	}
	if mode != "admission" {
		object := afterB.Containers[dst].Objects["scope/"+first]
		if object.Size != 10 || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("copy bytes"))) || object.Class != "plain" {
			t.Fatalf("record refusal lost independently witnessed bytes: %+v", object)
		}
	}
	if mode == "staged_execution" {
		for _, key := range []string{"b-engine", "z-engine"} {
			if _, exists := afterB.Containers[dst].Objects["scope/"+key]; exists {
				t.Fatalf("later live commit crossed the recording fault: %q", key)
			}
			object := afterB.Containers[dst].Objects["scope/.aeronautes/stage/"+id+"/batch/"+key]
			if object.Size != 10 || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("copy bytes"))) {
				t.Fatalf("unobserved engine-started bytes were not retained: %+v", object)
			}
		}
	}
	if mode == "verification" {
		for _, key := range []string{first, "b\nnative", "z-engine"} {
			object := afterB.Containers[dst].Objects["scope/"+key]
			if object.Size != 10 || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("copy bytes"))) {
				t.Fatalf("verification refusal lost independently readable copied bytes: %+v", object)
			}
		}
	}
	if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec("DROP TRIGGER refuse_copy_item")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if h.jobStore.Available() == nil {
		t.Fatal("repairing storage silently cleared the process latch")
	}
	requests = srcTrace.requestCount() + dstTrace.requestCount()
	copyCompletionReopen(t, h, id, saved)
	if requests != srcTrace.requestCount()+dstTrace.requestCount() {
		t.Fatal("orderly record reopening contacted Swift")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
}

func TestCopyRecordRefusalMock(t *testing.T) {
	for _, mode := range []string{"admission", "execution", "staged_execution", "verification"} {
		t.Run(mode, func(t *testing.T) { copyRecordFault(t, findMockHarness(t), mode) })
	}
}

func TestCopyRecordRefusalRealSwift(t *testing.T) {
	for _, mode := range []string{"admission", "execution", "staged_execution", "verification"} {
		t.Run(mode, func(t *testing.T) { copyRecordFault(t, New(t), mode) })
	}
}
