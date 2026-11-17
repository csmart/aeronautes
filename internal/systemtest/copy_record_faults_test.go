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

// copyRecordFault copies three plain objects while one of the job's item
// records cannot be written. The application sends a name the engine cannot
// carry itself, one at a time, and stops after the record it could not keep;
// the engine is given its names together, so it has written all of them
// before any outcome is recorded.
func copyRecordFault(t *testing.T, h *Harness, mode string) {
	t.Helper()
	src := h.Container(h.Primary, "record-fault-src")
	dst := h.Container(h.Secondary, "record-fault-dst")
	segments := h.Container(h.Secondary, "record-fault-segments")
	aliases := h.Container(h.Secondary, "record-fault-aliases")
	first := "a\nnative"
	second := "b\nnative"
	if mode == "admission" || mode == "engine_execution" {
		first = "a-engine"
	}
	if mode == "engine_execution" {
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
			t.Fatalf("the first PUT's response boundary: %+v %v", event, err)
		}
		witness := h.SnapshotNamespace(h.Secondary)
		object := witness.Containers[dst].Objects["scope/"+first]
		if object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("copy bytes"))) || object.Size != 10 {
			t.Fatalf("the first write was not independently observed before the SQL refusal: %+v", object)
		}
		if mode == "engine_execution" {
			page, err := h.jobStore.ReadItems(t.Context(), id, 0, 100)
			if err != nil || len(page.Items) != 3 {
				t.Fatalf("engine batch lacks acknowledged admissions: %+v %v", page, err)
			}
			for _, item := range page.Items {
				if !item.Admitted || item.Execution != nil {
					t.Fatalf("a name the engine was given lost its admission, or has an outcome before the engine answered: %+v", item)
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
	// written are the names the destination holds afterwards, and outcomes
	// each recorded item's first record, in name order; a nil outcome is an
	// admitted name with no record.
	copied := &jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: 10}
	var (
		wantState    string
		wantTotals   jobs.ItemTotals
		wantCounters jobs.Counters
		written      []string
		outcomes     []*jobs.ItemObservation
		verified     int
	)
	switch mode {
	case "admission":
		// The second name could not be admitted, so the first, which was
		// waiting for the engine, was never sent.
		wantState = "failed"
		wantTotals = jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1, Problems: 1}
		wantCounters = jobs.Counters{ObjectsPlanned: 2, BytesPlanned: 20}
		outcomes = []*jobs.ItemObservation{{Result: jobs.ItemNotAttempted, Effect: jobs.EffectNotAttempted,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeIncomplete}}}
	case "execution":
		// The application wrote the first name and could not record it, and
		// sent nothing after that.
		wantState = "partial"
		wantTotals = jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1, Unresolved: 1}
		wantCounters = jobs.Counters{ObjectsPlanned: 1, BytesPlanned: 10, ObjectsCopied: 1, BytesCopied: 10, NativeRouted: 1, ObjectsVerified: 1}
		written, outcomes = []string{first}, []*jobs.ItemObservation{nil}
	case "engine_execution":
		// The engine had all three names and wrote them. The first record
		// could not be kept, so none of the three has one: the rest have
		// nowhere to go.
		wantState = "partial"
		wantTotals = jobs.ItemTotals{Items: 3, Planned: 3, Admitted: 3, Unresolved: 3}
		wantCounters = jobs.Counters{ObjectsPlanned: 3, BytesPlanned: 30, ObjectsCopied: 1, BytesCopied: 10, ObjectsVerified: 1}
		written, outcomes = []string{first, "b-engine", "z-engine"}, []*jobs.ItemObservation{nil, nil, nil}
	case "verification":
		// The second name was written and its first record kept; its second
		// record could not be, and the third name was never reached.
		wantState = "partial"
		wantTotals = jobs.ItemTotals{Items: 2, Planned: 2, Admitted: 2, Unverified: 1}
		wantCounters = jobs.Counters{ObjectsPlanned: 2, BytesPlanned: 20, ObjectsCopied: 2, BytesCopied: 20, NativeRouted: 2, ObjectsVerified: 2}
		written, outcomes, verified = []string{first, "b\nnative"}, []*jobs.ItemObservation{copied, copied}, 1
	}
	if saved.Job.State != wantState || saved.Job.Counters != wantCounters || saved.Job.FinishedAt == "" ||
		!saved.ItemsCaptured || saved.ItemTotals != wantTotals || len(saved.Items) != len(outcomes) || saved.Items[0].Key != first {
		t.Fatalf("record refusal lost acknowledged facts or reported effects: %+v want=%s %+v %+v", saved, wantState, wantCounters, wantTotals)
	}
	for i, item := range saved.Items {
		if item.Source.Object != "scope/"+item.Key || item.Destination.Object != "scope/"+item.Key || !item.Admitted {
			t.Fatalf("record refusal fabricated or changed an identity, or recorded a stage name: %+v", item)
		}
		if (item.Execution == nil) != (outcomes[i] == nil) || (item.Execution != nil && item.Execution.ItemObservation != *outcomes[i]) {
			t.Fatalf("record refusal invented or lost an outcome: %+v want=%+v", item, outcomes[i])
		}
		if (item.Verification != nil) != (i < verified) {
			t.Fatalf("record refusal invented or lost a second record: %+v", item)
		}
	}
	wantSummary := fmt.Sprintf("job %s · item outcomes: 0 failed names, 0 conflicts, 0 refusals, %d unresolved, %d unverified, %d with problems", id, wantTotals.Unresolved, wantTotals.Unverified, wantTotals.Problems)
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.copy."+wantState || saved.Audit[0].Summary != wantSummary ||
		saved.Audit[0].BookmarkID != h.Primary.BookmarkID || saved.Audit[0].Resource != src+"/scope/ → "+dst+"/scope/" {
		t.Fatalf("record refusal audit lost facets or source authority: %+v", saved.Audit)
	}
	if len(saved.Rows) != 0 {
		t.Fatalf("a copy of plain objects left recovery records: %+v", saved.Rows)
	}
	if visible, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id); err == nil || visible != nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("record refusal exposed stale job status: %+v %v", visible, err)
	}
	if items, err := h.Jobs.Items(t.Context(), h.Primary.BookmarkID, id, "", 100); err == nil || items.Items != nil {
		t.Fatalf("record refusal exposed a partial public ledger: %+v %v", items, err)
	}
	if totals, err := h.Jobs.ItemTotals(t.Context(), h.Primary.BookmarkID, id); err == nil || totals != (app.JobItemTotalsDTO{}) {
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
	// Each written name was written at that name and nothing else was
	// touched. The application sends its own PUT once; the engine may send
	// one again within its retry budget, so only its names are exact.
	storage, err := url.Parse(h.Secondary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	var allowed []string
	wantWrites := map[string]bool{}
	for _, key := range written {
		allowed = append(allowed, dst+"/scope/"+key)
		wantWrites["PUT "+storage.Path+"/"+dst+"/scope/"+key] = true
	}
	gotWrites := map[string]bool{}
	for _, write := range dstTrace.writeSnapshot() {
		gotWrites[write] = true
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeA, afterA)
	if changed := collateral(beforeB, afterB, allowed); len(changed) != 0 || len(srcTrace.writeSnapshot()) != 0 || !reflect.DeepEqual(gotWrites, wantWrites) ||
		(mode != "engine_execution" && len(dstTrace.writeSnapshot()) != len(written)) {
		t.Fatalf("record failure crossed a dependent effect: collateral=%v source=%v destination=%v", changed, srcTrace.writeSnapshot(), dstTrace.writeSnapshot())
	}
	for _, key := range written {
		object := afterB.Containers[dst].Objects["scope/"+key]
		if object.Size != 10 || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("copy bytes"))) || object.Class != "plain" {
			t.Fatalf("a written name does not hold the source's bytes: %q %+v", key, object)
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
	for _, mode := range []string{"admission", "execution", "engine_execution", "verification"} {
		t.Run(mode, func(t *testing.T) { copyRecordFault(t, findMockHarness(t), mode) })
	}
}

func TestCopyRecordRefusalRealSwift(t *testing.T) {
	for _, mode := range []string{"admission", "execution", "engine_execution", "verification"} {
		t.Run(mode, func(t *testing.T) { copyRecordFault(t, New(t), mode) })
	}
}
