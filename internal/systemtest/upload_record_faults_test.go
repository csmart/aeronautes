package systemtest

import (
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

// uploadRecordFault uploads three plain files from one folder while one of
// the job's item records cannot be written. A native mode sends each name
// itself, one at a time; an engine mode gives the engine the whole folder.
func uploadRecordFault(t *testing.T, h *Harness, mode string) {
	t.Helper()
	dst := h.Container(h.Primary, "upload-record-fault")
	segments := h.Container(h.Primary, "shared-segments")
	aliases := h.Container(h.Primary, "shared-aliases")
	other := h.Container(h.Secondary, "untouched")
	h.Seed(h.Primary, dst, "keep", []byte("destination sentinel"), nil)
	h.Seed(h.Primary, dst, ".aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
	shared := []byte("shared bytes")
	h.Seed(h.Primary, segments, "shared/0", shared, nil)
	h.Seed(h.Primary, aliases, "dlo", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	h.SeedSLO(h.Primary, aliases, "slo", fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(shared), len(shared)))
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root := t.TempDir()
	var paths []string
	for _, name := range []string{"first", "second", "third"} {
		paths = append(paths, seedListingLocal(t, root, name, []byte("bytes")))
	}
	seedListingLocal(t, root, "unselected", []byte("local sentinel"))
	localBefore := snapshotListingLocal(t, root)
	native := strings.HasPrefix(mode, "native_")
	prefix := "scope/"
	if native {
		prefix = "scope\n/"
	}
	var barrier *swifttest.HTTPResponseBarrier
	if strings.HasSuffix(mode, "execution") || strings.HasSuffix(mode, "verification") {
		storage, err := url.Parse(h.Primary.storageURL)
		if err != nil {
			t.Fatal(err)
		}
		path := &url.URL{Path: storage.Path + "/" + dst + "/" + prefix + "first"}
		barrier, err = swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
			Operation: t.Name(), Method: http.MethodPut, EscapedPath: path.EscapedPath(), Occurrence: 1}, false, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
	}
	var trace *uploadDeleteTrace
	if barrier != nil {
		trace = copyCompletionProxy(t, h, h.Primary, "", barrier)
	} else {
		trace = copyCompletionProxy(t, h, h.Primary, "")
	}
	if strings.HasSuffix(mode, "admission") {
		uploadRecordRefusal(t, h, mode)
	}
	app.WireFilePicker(h.Transfer, listingFilePicker{files: paths})
	picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: dst, Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: dst, Prefix: prefix, SelectionToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	if barrier != nil {
		event, err := barrier.Wait(t.Context())
		if err != nil || event.StatusCode != http.StatusCreated {
			t.Fatalf("upload PUT response boundary: %+v %v", event, err)
		}
		witness := h.SnapshotNamespace(h.Primary)
		object := witness.Containers[dst].Objects[prefix+"first"]
		if object.Size != 5 || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("bytes"))) {
			t.Fatalf("upload effect not independently observed before SQL refusal: %+v", object)
		}
		page, err := h.jobStore.ReadItems(t.Context(), id, 0, 100)
		wantAdmissions := 3
		if native {
			wantAdmissions = 1
		}
		if err != nil || len(page.Items) != wantAdmissions {
			t.Fatalf("invoked upload lacks acknowledged admissions: %+v %v", page, err)
		}
		for _, item := range page.Items {
			if !item.Admitted || item.Execution != nil || item.Verification != nil || item.Staging != nil {
				t.Fatalf("upload lost admission before response: %+v", item)
			}
		}
		uploadRecordRefusal(t, h, mode)
		if err := barrier.Release(); err != nil {
			t.Fatal(err)
		}
	}
	copyCompletionAwaitPins(t, h, h.Primary)
	assertUploadRecordFault(t, h, mode, id, dst, prefix, native, paths, beforeA, beforeB, trace)
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), nil)
}

func uploadRecordRefusal(t *testing.T, h *Harness, mode string) {
	t.Helper()
	query := `CREATE TRIGGER refuse_upload_item BEFORE INSERT ON job_item_observations WHEN NEW.phase='execution' BEGIN SELECT RAISE(ABORT,'fixture execution record refusal'); END`
	if strings.HasSuffix(mode, "verification") {
		query = `CREATE TRIGGER refuse_upload_item BEFORE INSERT ON job_item_observations WHEN NEW.phase='verification' AND EXISTS (SELECT 1 FROM job_item_observations WHERE item_id=NEW.item_id AND phase='execution') BEGIN SELECT RAISE(ABORT,'fixture verification record refusal'); END`
	} else if strings.HasSuffix(mode, "admission") {
		key := `'scope/second'`
		if strings.HasPrefix(mode, "native_") {
			key = `'scope'||char(10)||'/second'`
		}
		query = `CREATE TRIGGER refuse_upload_item BEFORE INSERT ON job_items WHEN NEW.item_key=` + key + ` BEGIN SELECT RAISE(ABORT,'fixture admission refusal'); END`
	}
	if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
		t.Fatal(err)
	}
}

func assertUploadRecordFault(t *testing.T, h *Harness, mode, id, dst, prefix string, native bool, paths []string,
	beforeA, beforeB *oracle.AccountSnapshot, trace *uploadDeleteTrace) {
	t.Helper()
	control := strings.HasSuffix(mode, "control")
	saved := uploadRecoveryRead(t, h.database, id)
	wantState := "partial"
	want := jobs.ItemTotals{Items: 3, Planned: 3, Admitted: 3}
	done := int64(1)
	if native && !control || mode == "engine_admission" {
		want.Items, want.Planned, want.Admitted = 1, 1, 1
	}
	switch mode {
	case "engine_admission":
		wantState, done, want.Problems = "failed", 0, 1
	case "native_execution", "engine_execution":
		want.Unresolved = want.Items
	case "native_verification", "engine_verification":
		want.Unresolved, want.Unverified = want.Items-1, 1
	case "native_control", "engine_control":
		wantState, done = "complete", 3
	}
	counters := jobs.Counters{ObjectsPlanned: 3, BytesPlanned: 15, ObjectsCopied: done, ObjectsVerified: done, BytesCopied: done * 5}
	if native {
		counters.NativeRouted = done
	}
	if saved.Job.State != wantState || saved.Job.Counters != counters || saved.Job.FinishedAt == "" ||
		!saved.ItemsCaptured || saved.ItemTotals != want || int64(len(saved.Items)) != want.Items {
		t.Fatalf("upload recording refusal changed retained outcome: job=%+v totals=%+v rows=%d want=%s %+v %+v", saved.Job, saved.ItemTotals, len(saved.Items), wantState, counters, want)
	}
	for i, item := range saved.Items {
		key := prefix + filepath.Base(paths[i])
		if item.Key != key || item.Source != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: paths[i]}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: dst, Object: key}) ||
			!item.Planned || !item.Admitted || item.ListedBytes != 5 {
			t.Fatalf("upload fault lost exact selection: %+v", item)
		}
		if item.Staging != nil {
			t.Fatalf("an upload recorded a stage name: %+v", item)
		}
		assertUploadFaultObservation(t, item, mode, i)
	}
	summary := fmt.Sprintf(" · item outcomes: 0 failed names, 0 conflicts, 0 refusals, %d unresolved, %d unverified, %d with problems", want.Unresolved, want.Unverified, want.Problems)
	result := "verify_mismatch"
	if control {
		result = "applied"
	} else if wantState == "failed" {
		result = "failed"
	}
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.local."+wantState || saved.Audit[0].Resource != "job "+id || saved.Audit[0].Summary != summary || saved.Audit[0].Result != result {
		t.Fatalf("upload fault audit lost terminal facets: %+v", saved.Audit)
	}
	var bookmark string
	if err := h.database.Reader().QueryRow("SELECT bookmark_id FROM audit_events WHERE id=?", saved.Audit[0].ID).Scan(&bookmark); err != nil || bookmark != h.Primary.BookmarkID {
		t.Fatalf("upload fault changed audit authority: %q %v", bookmark, err)
	}
	if len(saved.Rows) != 0 {
		t.Fatalf("an upload of plain files left recovery records: %+v", saved.Rows)
	}
	assertUploadFaultAvailability(t, h, id, control)
	assertUploadFaultEffects(t, h, mode, dst, prefix, native, paths, beforeA, beforeB, trace)
	if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec("DROP TRIGGER IF EXISTS refuse_upload_item"); return err }); err != nil {
		t.Fatal(err)
	}
	if !control && h.jobStore.Available() == nil {
		t.Fatal("repairing SQL silently cleared the item-store latch")
	}
	requests := trace.requestCount()
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	uploadRecoveryReopen(t, h, id, saved)
	if trace.requestCount() != requests {
		t.Fatal("upload record reopening contacted Swift")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
}

func assertUploadFaultObservation(t *testing.T, item jobs.ItemRecord, mode string, index int) {
	t.Helper()
	if mode == "engine_admission" {
		if item.Execution == nil || item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemNotAttempted, Effect: jobs.EffectNotAttempted,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeIncomplete}}) || item.Verification != nil {
			t.Fatalf("undispatched upload lost its not-attempted fact: %+v", item)
		}
		return
	}
	copied := strings.HasSuffix(mode, "control") || mode == "native_admission" || strings.HasSuffix(mode, "verification") && index == 0
	verified := strings.HasSuffix(mode, "control") || mode == "native_admission"
	if (item.Execution != nil) != copied || (item.Verification != nil) != verified {
		t.Fatalf("upload recording refusal invented or lost an observation: %+v", item)
	}
	if copied && item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: 5}) ||
		verified && item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
		t.Fatalf("upload refusal erased an acknowledged copied or verified fact: %+v", item)
	}
}

func assertUploadFaultAvailability(t *testing.T, h *Harness, id string, control bool) {
	t.Helper()
	visible, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
	if control {
		if err != nil || visible == nil || h.jobStore.Available() != nil {
			t.Fatalf("writable upload control acquired an unavailable latch: %+v %v", visible, err)
		}
		return
	}
	if err == nil || visible != nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("upload recording failure exposed stale status: %+v %v", visible, err)
	}
	if items, err := h.Jobs.Items(t.Context(), h.Primary.BookmarkID, id, "", 100); err == nil || items.Items != nil {
		t.Fatalf("upload recording failure exposed partial public history: %+v %v", items, err)
	}
	if totals, err := h.Jobs.ItemTotals(t.Context(), h.Primary.BookmarkID, id); err == nil || totals != (app.JobItemTotalsDTO{}) {
		t.Fatalf("upload recording failure invented public totals: %+v %v", totals, err)
	}
	for _, kind := range []jobs.Type{jobs.TypeCopy, jobs.TypeUpload, jobs.TypeDownload, jobs.TypeMirror, jobs.TypeMirrorPreview, jobs.TypeDeletePrefix, jobs.TypeEmpty, jobs.TypeDeleteCont} {
		if job, err := h.jobStore.Create(t.Context(), jobs.Job{Type: kind}); !errors.Is(err, jobs.ErrItemRecordsUnavailable) || job.ID != "" {
			t.Fatalf("upload record failure admitted new %s work: %+v %v", kind, job, err)
		}
	}
	if next, err := h.Transfer.RerunJob(t.Context(), id); next != "" || err == nil {
		t.Fatalf("unavailable upload records allowed rerun: %q %v", next, err)
	}
}

func assertUploadFaultEffects(t *testing.T, h *Harness, mode, dst, prefix string, native bool, paths []string,
	beforeA, beforeB *oracle.AccountSnapshot, trace *uploadDeleteTrace) {
	t.Helper()
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	control := strings.HasSuffix(mode, "control")
	var allowed []string
	writes := map[string]int{}
	for i, path := range paths {
		if mode == "engine_admission" {
			break
		}
		// The engine has the whole folder before any outcome is recorded,
		// so all three names are written by the time a record is refused.
		// The application sends a native name itself and stops after the
		// first.
		if control || !native || i == 0 {
			live := prefix + filepath.Base(path)
			writes["PUT "+storage.Path+"/"+dst+"/"+live] = 1
			allowed = append(allowed, dst+"/"+live)
		}
	}
	observed := map[string]int{}
	for _, write := range trace.writeSnapshot() {
		observed[write]++
	}
	if !reflect.DeepEqual(observed, writes) {
		t.Fatalf("upload crossed a required recording boundary: got=%v want=%v", observed, writes)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeA, afterA, allowed...)
	h.AssertNoCollateral(beforeB, afterB)
	for _, path := range allowed {
		object := afterA.Containers[dst].Objects[strings.TrimPrefix(path, dst+"/")]
		if object.Class != "plain" || object.Size != 5 || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("bytes"))) {
			t.Fatalf("upload fault lost independently witnessed bytes at %q: %+v", path, object)
		}
	}
}

func uploadRecordFaultCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, mode := range []string{"native_admission", "engine_admission", "native_execution", "engine_execution", "native_verification", "engine_verification", "native_control", "engine_control"} {
		t.Run(mode, func(t *testing.T) { uploadRecordFault(t, makeHarness(t), mode) })
	}
}

func TestUploadRecordRefusalMock(t *testing.T) {
	t.Parallel()
	uploadRecordFaultCases(t, findMockHarness)
}
func TestUploadRecordRefusalRealSwift(t *testing.T) {
	t.Parallel()
	uploadRecordFaultCases(t, New)
}
