package systemtest

import (
	"context"
	"crypto/sha256"
	"database/sql"
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
	Job    *app.JobDTO
	Rows   []jobs.RecoveryRecord
	Status app.RecoveryStatusDTO
	Audit  []app.AuditEventDTO
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
	service := app.NewJobService(jobs.NewStore(database))
	job, err := service.Get(t.Context(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.RecoveryStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events, err := app.NewAuditService(audit.NewStore(database)).Search(t.Context(), app.AuditQueryDTO{OperationPrefix: "transfer.local.", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	return uploadRecoveryState{job, uploadRecoveryRows(t, database), status, events}
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
	abortedStagePuts int
	stagePutAborted  chan struct{}
	mu               sync.Mutex
	paths            []string
	writes           []string
	requests         int
}

func uploadSourceAbort(fault, storagePath, dest string, request *http.Request, err error) bool {
	if fault != "source-link" || request.Method != http.MethodPut {
		return false
	}
	stage, ok := strings.CutPrefix(request.URL.Path, storagePath+"/"+dest+"/.aeronautes/stage/")
	if !ok {
		return false
	}
	job, ok := strings.CutSuffix(stage, "/batch/selected-link")
	if !ok || job == "" || strings.Contains(job, "/") {
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

func (trace *uploadDeleteTrace) snapshot() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.paths...)
}

func (trace *uploadDeleteTrace) writeSnapshot() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.writes...)
}

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
	trace := &uploadDeleteTrace{stagePutAborted: make(chan struct{}, 1)}
	proxy.ModifyResponse = func(response *http.Response) error {
		if test.fault == "committed-verify" && response.Request.Method == http.MethodHead && strings.HasSuffix(response.Request.URL.Path, "/live/000.bin") && response.StatusCode == http.StatusOK {
			response.Header.Set("Content-Length", "1")
			response.ContentLength = 1
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, request *http.Request, err error) {
		// The selected-link fixture makes rclone abort its staged body when
		// the followed file size differs from the link's own size.
		if uploadSourceAbort(test.fault, storage.Path, dest, request, err) {
			trace.mu.Lock()
			trace.abortedStagePuts++
			trace.mu.Unlock()
			select {
			case trace.stagePutAborted <- struct{}{}:
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
		if test.fault == "mixed" && r.Method == http.MethodPut && r.URL.Path == storage.Path+"/"+dest+"/live/001.bin" {
			http.Error(w, "injected live write refusal", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodDelete || !strings.HasPrefix(r.URL.Path, storage.Path+"/"+dest+"/.aeronautes/stage/") {
			proxy.ServeHTTP(w, r)
			return
		}
		trace.mu.Lock()
		trace.paths = append(trace.paths, r.URL.Path)
		trace.mu.Unlock()
		refuse := test.fault == "delete" || test.fault == "mixed" || test.fault == "first" && strings.HasSuffix(r.URL.Path, "/000.bin") || test.fault == "last" && strings.HasSuffix(r.URL.Path, fmt.Sprintf("/%03d.bin", test.count-1))
		if refuse {
			http.Error(w, "injected staging delete refusal", http.StatusForbidden)
			return
		}
		if test.fault == "absent" || test.fault == "landed-error" {
			// Apply the fixture deletion exactly once before changing the
			// response; the independent witness decides whether it landed.
			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, r)
			if response.Code/100 != 2 {
				t.Errorf("fixture deletion did not land: %d", response.Code)
			}
			if test.fault == "absent" {
				http.Error(w, "already absent", http.StatusNotFound)
			} else {
				http.Error(w, "injected response failure after deletion", http.StatusForbidden)
			}
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

func uploadRecoveryVerifyObject(t *testing.T, object oracle.ObjectState, body []byte, class, segmentContainer, stageName string) []string {
	t.Helper()
	if object.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) || object.Size != int64(len(body)) || object.Class != class {
		t.Fatalf("uploaded identity differs: %+v", object)
	}
	if class != "dlo" {
		return nil
	}
	container, prefix, ok := strings.Cut(object.Manifest, "/")
	generation, rooted := strings.CutPrefix(prefix, stageName+"/")
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
			settings.ChunkSize = "1Mi"
		}
		paths = append(paths, seedListingLocal(t, root, name, body))
		bodies[name] = body
		plannedBytes += int64(len(body))
	}
	seedListingLocal(t, root, "unselected", []byte("local sentinel"))
	localBefore := snapshotListingLocal(t, root)
	app.WireFilePicker(h.Transfer, listingFilePicker{files: paths})
	if test.fault == "committing" || strings.HasPrefix(test.fault, "committed") {
		kind := strings.Split(test.fault, "-")[0]
		query := `CREATE TRIGGER refuse_upload_record BEFORE INSERT ON job_recovery WHEN NEW.kind='` + kind + `' BEGIN SELECT RAISE(ABORT, 'injected upload record refusal'); END`
		if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
			t.Fatal(err)
		}
	}
	trace := uploadRecoveryProxy(t, h, dest, test)
	picked, err := h.Transfer.PickUploadFiles(t.Context())
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
	failed, issues := int64(0), int64(0)
	switch test.fault {
	case "committing":
		failed = 1
	case "committed", "delete", "landed-error":
		issues = int64(test.count)
	case "committed-verify":
		failed = 1
		issues = 1
	case "first", "last":
		issues = 1
	case "mixed":
		failed = 1
		issues = 1
	}
	wantState, wantAudit := "complete", "applied"
	if failed > 0 || issues > 0 {
		wantState, wantAudit = "partial", "verify_mismatch"
	}
	wantCounters := jobs.Counters{ObjectsPlanned: int64(test.count), ObjectsCopied: int64(test.count) - failed, ObjectsVerified: int64(test.count) - failed, ObjectsFailed: failed, BytesPlanned: plannedBytes, BytesCopied: plannedBytes}
	if failed > 0 {
		wantCounters.BytesCopied -= int64(len(bodies[fmt.Sprintf("%03d.bin", test.count-1)]))
	}
	if job.State != wantState || job.Counters != wantCounters || state.Audit[0].Result != wantAudit {
		t.Errorf("post-commit outcome differs: job=%+v audit=%+v want counters=%+v state=%s", job, state.Audit, wantCounters, wantState)
	}
	if issues > 0 {
		firstName := "000.bin"
		if test.fault == "last" {
			firstName = fmt.Sprintf("%03d.bin", test.count-1)
		}
		parts := []string{fmt.Sprintf("%d post-commit issue", issues), "live/" + firstName}
		if strings.HasPrefix(test.fault, "committed") {
			parts = append(parts, "commit-record", "injected upload record refusal")
		} else {
			parts = append(parts, "staging-cleanup", ".aeronautes/stage/"+job.ID+"/batch/"+firstName, "HTTP 403")
		}
		for _, part := range parts {
			if !strings.Contains(job.Error, part) {
				t.Errorf("post-commit issues not visible (%q): %q", part, job.Error)
			}
		}
	}
	if failed > 0 && !strings.Contains(job.Error, "1 failed") {
		t.Errorf("transfer failure hidden: %q", job.Error)
	}
	if issues == 0 && failed == 0 && job.Error != "" {
		t.Errorf("unfaulted/absent control error: %q", job.Error)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeB, afterB)
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), nil)
	var allowed, wantDeletes []string
	var manifest string
	for index := range test.count {
		name := fmt.Sprintf("%03d.bin", index)
		stageName := ".aeronautes/stage/" + job.ID + "/batch/" + name
		live, liveFound := afterA.Containers[dest].Objects["live/"+name]
		staged, stageFound := afterA.Containers[dest].Objects[stageName]
		liveExpected := test.fault != "committing" && !(test.fault == "mixed" && index == 1)
		stageExpected := test.fault == "committing" || strings.HasPrefix(test.fault, "committed") || test.fault == "delete" || test.fault == "mixed" || test.fault == "first" && index == 0 || test.fault == "last" && index == test.count-1
		if liveFound != liveExpected || stageFound != stageExpected {
			t.Errorf("wrong live/staging presence for %s: live=%v/%v stage=%v/%v", name, liveFound, liveExpected, stageFound, stageExpected)
		}
		if liveFound {
			allowed = append(allowed, uploadRecoveryVerifyObject(t, live, bodies[name], test.class, dest+"_segments", stageName)...)
		}
		if stageFound {
			allowed = append(allowed, uploadRecoveryVerifyObject(t, staged, bodies[name], test.class, dest+"_segments", stageName)...)
		}
		if liveFound {
			manifest = live.Manifest
		} else if stageFound {
			manifest = staged.Manifest
		}
		if liveFound && stageFound && live.Manifest != staged.Manifest {
			t.Errorf("live and staging manifests differ: %q != %q", live.Manifest, staged.Manifest)
		}
		allowed = append(allowed, dest+"/live/"+name, dest+"/"+stageName)
		if liveExpected && !strings.HasPrefix(test.fault, "committed") {
			storage, _ := url.Parse(h.Primary.storageURL)
			wantDeletes = append(wantDeletes, storage.Path+"/"+dest+"/"+stageName)
		}
	}
	if !reflect.DeepEqual(trace.snapshot(), wantDeletes) {
		t.Errorf("cleanup admission/retry differs: got=%v want=%v", trace.snapshot(), wantDeletes)
	}
	h.AssertNoCollateral(beforeA, afterA, allowed...)
	wantRecords := 1
	if test.class == "dlo" {
		wantRecords = 4
		if test.fault == "committing" {
			wantRecords = 2
		}
		if strings.HasPrefix(test.fault, "committed") {
			wantRecords = 3
		}
	}
	if len(state.Rows) != wantRecords || state.Status != (app.RecoveryStatusDTO{Jobs: 1, Records: int64(wantRecords)}) || job.RecoveryRecords != int64(wantRecords) {
		t.Errorf("recovery facts/status differ: %+v", state)
	}
	wantRows := []jobs.RecoveryRecord{{JobID: job.ID, BookmarkID: h.Primary.BookmarkID, Kind: jobs.RecoveryStaging, Container: dest, Prefix: ".aeronautes/stage/" + job.ID + "/"}}
	if test.class == "dlo" {
		container, prefix, ok := strings.Cut(manifest, "/")
		if !ok {
			t.Fatal("no independently verified manifest for recovery facts")
		}
		for _, kind := range []jobs.RecoveryKind{jobs.RecoveryCaptured, jobs.RecoveryCommitting, jobs.RecoveryCommitted} {
			if len(wantRows) == wantRecords {
				break
			}
			row := jobs.RecoveryRecord{JobID: job.ID, BookmarkID: h.Primary.BookmarkID, Kind: kind, Container: container, Prefix: prefix + "/"}
			if kind == jobs.RecoveryCommitting {
				row.Object = dest + "/live/000.bin"
			}
			wantRows = append(wantRows, row)
		}
	}
	if !reflect.DeepEqual(state.Rows, wantRows) {
		t.Errorf("durable recovery identities differ: got=%+v want=%+v", state.Rows, wantRows)
	}
	requests := trace.requestCount()
	uploadRecoveryReopen(t, h, job.ID, state)
	if trace.requestCount() != requests {
		t.Error("database reopen issued storage requests")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
}

func TestUploadRecoveryMock(t *testing.T) { uploadRecoveryCases(t, findMockHarness) }
func TestUploadRecoveryRealSwift(t *testing.T) {
	if uploadRecoveryChild(t) {
		return
	}
	uploadRecoveryCases(t, New)
}

func uploadRecoveryCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, test := range []uploadRecoveryCase{
		{"none", "plain", 1}, {"none", "dlo", 1}, {"committing", "dlo", 1}, {"committed", "dlo", 1}, {"committed-verify", "dlo", 1},
		{"first", "plain", 2}, {"last", "plain", 2}, {"delete", "plain", 201}, {"delete", "dlo", 1},
		{"absent", "plain", 1}, {"absent", "dlo", 1}, {"landed-error", "plain", 1}, {"landed-error", "dlo", 1}, {"mixed", "plain", 2},
	} {
		t.Run(test.fault+"_"+test.class, func(t *testing.T) { uploadRecoveryRun(t, makeHarness(t), test) })
	}
}
