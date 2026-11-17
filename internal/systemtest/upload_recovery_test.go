package systemtest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

type uploadRecoveryCase struct {
	fault string
	class string
	count int
}

type uploadRecoveryState struct {
	Job           *app.JobDTO
	Rows          []jobs.RecoveryRecord
	Status        app.RecoveryStatusDTO
	Audit         []app.AuditEventDTO
	ItemsCaptured bool
	Items         []jobs.ItemRecord
	ItemTotals    jobs.ItemTotals
}

func uploadRecoveryRows(t *testing.T, database *db.DB) []jobs.RecoveryRecord {
	t.Helper()
	rows, err := database.Reader().Query(`SELECT job_id, bookmark_id, kind, container, prefix, object FROM job_recovery ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []jobs.RecoveryRecord
	for rows.Next() {
		var row jobs.RecoveryRecord
		if err := rows.Scan(&row.JobID, &row.BookmarkID, &row.Kind, &row.Container, &row.Prefix, &row.Object); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func uploadRecoveryRead(t *testing.T, database *db.DB, jobID string) uploadRecoveryState {
	t.Helper()
	store := jobs.NewStore(database)
	service := app.NewJobService(store)
	job := publicJob(t, store, jobID)
	status, err := service.RecoveryStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events, err := app.NewAuditService(audit.NewStore(database)).Search(t.Context(), app.AuditQueryDTO{OperationPrefix: "transfer.local.", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	state := uploadRecoveryState{Job: job, Rows: uploadRecoveryRows(t, database), Status: status, Audit: events}
	totals, err := store.ReadItemTotals(t.Context(), jobID)
	if errors.Is(err, jobs.ErrItemRecordsNotCaptured) {
		return state
	}
	if err != nil {
		t.Fatal(err)
	}
	state.ItemsCaptured, state.ItemTotals = true, totals
	for cursor := int64(0); ; {
		page, err := store.ReadItems(t.Context(), jobID, cursor, 100)
		if err != nil || len(page.Items) > 100 {
			t.Fatalf("upload retained item page: %+v %v", page, err)
		}
		state.Items = append(state.Items, page.Items...)
		if page.NextCursor == 0 {
			break
		}
		if page.NextCursor <= cursor {
			t.Fatal("upload retained item cursor did not advance")
		}
		cursor = page.NextCursor
	}
	return state
}

func uploadRecoveryChild(t *testing.T) bool {
	input := os.Getenv("AERONAUTES_UPLOAD_REOPEN_INPUT")
	if input == "" {
		return false
	}
	var request struct{ Database, JobID, Output string }
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(t.Context(), request.Database)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := jobs.NewStore(database).FailInterrupted(t.Context()); err != nil || changed != 0 {
		t.Fatalf("terminal recovery changed: %d, %v", changed, err)
	}
	state := uploadRecoveryRead(t, database, request.JobID)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(request.Output, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return true
}

func uploadRecoveryReopen(t *testing.T, h *Harness, jobID string, want uploadRecoveryState) {
	t.Helper()
	h.CloseFind()
	if err := h.database.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	request := struct{ Database, JobID, Output string }{h.statePath, jobID, filepath.Join(dir, "result.json")}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestUploadRecoveryRealSwift$", "-test.timeout=15s")
	child.Env = append(os.Environ(), "AERONAUTES_UPLOAD_REOPEN_INPUT="+input)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("reopen process: %v\n%s", err, output)
	}
	encoded, err := os.ReadFile(request.Output)
	if err != nil {
		t.Fatal(err)
	}
	var got uploadRecoveryState
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("process reopen changed durable result: got=%+v want=%+v", got, want)
	}
}

type uploadDeleteTrace struct {
	abortedPuts int
	putAborted  chan struct{}
	mu          sync.Mutex
	writes      []string
	requests    int
}

// uploadSourceAbort reports whether a proxy error is the engine giving up on
// the body it was sending to the selected-link fixture's name, which is the
// one transport failure that fixture expects.
func uploadSourceAbort(fault, storagePath, dest string, request *http.Request, err error) bool {
	if fault != "source-link" || request.Method != http.MethodPut ||
		request.URL.Path != storagePath+"/"+dest+"/scope/selected-link" {
		return false
	}
	if errors.Is(err, context.Canceled) && errors.Is(request.Context().Err(), context.Canceled) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) && (op.Op == "write" || op.Op == "readfrom") &&
		(errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE))
}

func (trace *uploadDeleteTrace) requestCount() int {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return trace.requests
}

func (trace *uploadDeleteTrace) writeSnapshot() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.writes...)
}

// uploadRecoveryProxy fronts the primary account, counts its requests and
// keeps its mutations. The "refused-write" fault refuses the write of
// live/001.bin, and "source-link" expects the engine to abandon a body.
func uploadRecoveryProxy(t *testing.T, h *Harness, dest string, test uploadRecoveryCase) *uploadDeleteTrace {
	t.Helper()
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 10 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	trace := &uploadDeleteTrace{putAborted: make(chan struct{}, 1)}
	proxy.ErrorHandler = func(w http.ResponseWriter, request *http.Request, err error) {
		// The selected-link fixture makes rclone abort the body it is
		// sending when the followed file size differs from the link's own.
		if uploadSourceAbort(test.fault, storage.Path, dest, request, err) {
			trace.mu.Lock()
			trace.abortedPuts++
			trace.mu.Unlock()
			select {
			case trace.putAborted <- struct{}{}:
			default:
			}
		} else {
			t.Errorf("upload fault proxy: %v", err)
		}
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.mu.Lock()
		trace.requests++
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			trace.writes = append(trace.writes, r.Method+" "+r.URL.Path)
		}
		trace.mu.Unlock()
		if test.fault == "refused-write" && r.Method == http.MethodPut && r.URL.Path == storage.Path+"/"+dest+"/live/001.bin" {
			http.Error(w, "injected live write refusal", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, bookmark := range bookmarks {
		if bookmark.ID != h.Primary.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, h.Primary.key); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatal("owned upload bookmark missing")
	}
	if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	return trace
}

// uploadRecoveryVerifyObject checks an object against the bytes it should
// hold and, for a dynamic large object, returns its segments' names.
// segmentsUnder is the name the engine was given to write: the object's own
// name for an upload, and the stage name for a copy, which still stages.
func uploadRecoveryVerifyObject(t *testing.T, object oracle.ObjectState, body []byte, class, segmentContainer, segmentsUnder string) []string {
	t.Helper()
	if object.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) || object.Size != int64(len(body)) || object.Class != class {
		t.Fatalf("uploaded identity differs: %+v", object)
	}
	if class != "dlo" {
		return nil
	}
	container, prefix, ok := strings.Cut(object.Manifest, "/")
	generation, rooted := strings.CutPrefix(prefix, segmentsUnder+"/")
	if !ok || container != segmentContainer || !rooted || generation == "" || strings.Contains(generation, "/") {
		t.Fatalf("unexpected generated manifest: %q", object.Manifest)
	}
	if len(object.Segments) != 2 {
		t.Fatalf("DLO backing count: %+v", object.Segments)
	}
	var allowed []string
	for index, segment := range object.Segments {
		start := index * (1 << 20)
		part := body[start:min(start+(1<<20), len(body))]
		if segment.Name != container+"/"+prefix+fmt.Sprintf("/%08d", index) || segment.Digest != fmt.Sprintf("%x", sha256.Sum256(part)) || segment.Size != int64(len(part)) || segment.ContentEncoding != "" {
			t.Fatalf("backing segment differs: %+v", segment)
		}
		allowed = append(allowed, segment.Name)
	}
	return allowed
}

// uploadRecoveryRun is what a finished upload leaves for a later process to
// read: every file at its own name and nowhere else, one captured record for
// each segment path the engine reported and no other recovery record (ADR
// 0122 decisions 3 and 8), and the same durable facts after a reopen.
func uploadRecoveryRun(t *testing.T, h *Harness, test uploadRecoveryCase) {
	t.Helper()
	dest := h.Container(h.Primary, "upload-recovery")
	other := h.Container(h.Secondary, "untouched")
	h.Seed(h.Primary, dest, "untouched", []byte("same account sentinel"), nil)
	h.Seed(h.Primary, dest, ".aeronautes/stage/other-job/batch/keep", []byte("other job sentinel"), nil)
	h.Seed(h.Secondary, other, "untouched", []byte("other account sentinel"), nil)
	if test.class == "dlo" {
		h.rawPut(h.Primary, "/"+dest+"_segments", nil, nil)
		t.Cleanup(func() { h.purge(h.Primary, dest+"_segments") })
		h.Seed(h.Primary, dest+"_segments", "outside/keep", []byte("unrelated segment sentinel"), nil)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root := t.TempDir()
	var paths []string
	bodies := make(map[string][]byte)
	settings := app.TransferSettingsDTO{}
	var plannedBytes int64
	for index := range test.count {
		name := fmt.Sprintf("%03d.bin", index)
		body := []byte("plain bytes " + name)
		if test.class == "dlo" {
			body = []byte(strings.Repeat("abcdefg", 160000))
			settings.ChunkSize, settings.LargeObjects = "1Mi", "dlo"
		}
		paths = append(paths, seedListingLocal(t, root, name, body))
		bodies[name] = body
		plannedBytes += int64(len(body))
	}
	seedListingLocal(t, root, "unselected", []byte("local sentinel"))
	localBefore := snapshotListingLocal(t, root)
	app.WireFilePicker(h.Transfer, listingFilePicker{files: paths})
	trace := uploadRecoveryProxy(t, h, dest, test)
	picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: dest, Prefix: "live/"})
	if err != nil {
		t.Fatal(err)
	}
	receipt := h.StartUpload(t.Context(), app.UploadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: dest, Prefix: "live/", SelectionToken: picked.Token, Settings: settings})
	if receipt.Err() != nil {
		t.Fatal(receipt.Err())
	}
	job := h.AwaitJob(receipt.JobID())
	state := uploadRecoveryRead(t, h.database, job.ID)
	deadline := time.Now().Add(5 * time.Second)
	for len(state.Audit) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		state = uploadRecoveryRead(t, h.database, job.ID)
	}
	if len(state.Audit) != 1 || state.Audit[0].Resource != "job "+job.ID || state.Audit[0].Operation != "transfer.local."+job.State {
		t.Fatalf("missing exact terminal audit: %+v", state.Audit)
	}
	wantCounters := jobs.Counters{ObjectsPlanned: int64(test.count), ObjectsCopied: int64(test.count), ObjectsVerified: int64(test.count), BytesPlanned: plannedBytes, BytesCopied: plannedBytes}
	if job.State != "complete" || job.Counters != wantCounters || state.Audit[0].Result != "applied" || job.Error != "" {
		t.Errorf("upload outcome differs: job=%+v audit=%+v want counters=%+v", job, state.Audit, wantCounters)
	}
	wantItems := jobs.ItemTotals{Items: int64(test.count), Planned: int64(test.count), Admitted: int64(test.count)}
	if !state.ItemsCaptured || state.ItemTotals != wantItems || len(state.Items) != len(paths) {
		t.Fatalf("upload lost item records: captured=%t totals=%+v rows=%d want=%+v", state.ItemsCaptured, state.ItemTotals, len(state.Items), wantItems)
	}
	byKey := map[string]jobs.ItemRecord{}
	for i, item := range state.Items {
		name := fmt.Sprintf("%03d.bin", i)
		if item.Key != "live/"+name || item.Source != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: paths[i]}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: dest, Object: "live/" + name}) ||
			item.Execution == nil {
			t.Fatalf("upload changed retained identity: %+v", item)
		}
		byKey[item.Key] = item
	}
	assertUploadItemPages(t, h, job.ID, byKey, wantItems)
	if wantSummary := " · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems"; state.Audit[0].Summary != wantSummary {
		t.Fatalf("terminal audit differs from the item facets: %q want=%q", state.Audit[0].Summary, wantSummary)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeB, afterB)
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), nil)
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	var allowed []string
	var wantRows []jobs.RecoveryRecord
	wantWrites := map[string]int{}
	for index := range test.count {
		name := "live/" + fmt.Sprintf("%03d.bin", index)
		live, found := afterA.Containers[dest].Objects[name]
		if !found {
			t.Fatalf("the upload left nothing at %s", name)
		}
		segments := uploadRecoveryVerifyObject(t, live, bodies[strings.TrimPrefix(name, "live/")], test.class, dest+"_segments", name)
		allowed = append(append(allowed, dest+"/"+name), segments...)
		wantWrites["PUT "+storage.Path+"/"+dest+"/"+name] = 1
		for _, segment := range segments {
			wantWrites["PUT "+storage.Path+"/"+segment] = 1
		}
		if test.class == "dlo" {
			container, prefix, _ := strings.Cut(live.Manifest, "/")
			wantRows = append(wantRows, jobs.RecoveryRecord{JobID: job.ID, BookmarkID: h.Primary.BookmarkID, Kind: jobs.RecoveryCaptured, Container: container, Prefix: prefix + "/"})
		}
	}
	// Every name the upload wrote is the file's own or a segment of it, and
	// each was written once: there is no stage name and nothing is deleted.
	gotWrites := map[string]int{}
	for _, write := range trace.writeSnapshot() {
		gotWrites[write]++
	}
	if !reflect.DeepEqual(gotWrites, wantWrites) {
		t.Errorf("upload mutations differ: got=%v want=%v", gotWrites, wantWrites)
	}
	h.AssertNoCollateral(beforeA, afterA, allowed...)
	wantStatus := app.RecoveryStatusDTO{}
	if len(wantRows) > 0 {
		wantStatus = app.RecoveryStatusDTO{Jobs: 1, Records: int64(len(wantRows))}
	}
	if !reflect.DeepEqual(state.Rows, wantRows) || state.Status != wantStatus || job.RecoveryRecords != int64(len(wantRows)) {
		t.Errorf("durable recovery identities differ: got=%+v status=%+v want=%+v", state.Rows, state.Status, wantRows)
	}
	requests := trace.requestCount()
	uploadRecoveryReopen(t, h, job.ID, state)
	if trace.requestCount() != requests {
		t.Error("database reopen issued storage requests")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
}

func TestUploadRecoveryMock(t *testing.T) {
	t.Parallel()
	uploadRecoveryCases(t, findMockHarness)
}
func TestUploadRecoveryRealSwift(t *testing.T) {
	t.Parallel()

	if uploadRecoveryChild(t) {
		return
	}
	uploadRecoveryCases(t, New)
}

func uploadRecoveryCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, test := range []uploadRecoveryCase{{"none", "plain", 2}, {"none", "dlo", 1}} {
		t.Run(test.fault+"_"+test.class, func(t *testing.T) { uploadRecoveryRun(t, makeHarness(t), test) })
	}
}
