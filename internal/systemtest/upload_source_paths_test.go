package systemtest

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

type uploadSourceCase struct {
	name         string
	noncanonical bool
	native       bool
	fileSymlink  bool
}

func TestUploadSourceAbortScope(t *testing.T) {
	const path = "/v1/AUTH_owned/destination/.aeronautes/stage/job/batch/selected-link"
	for _, tc := range []struct {
		name, fault, method, path string
		err                       error
		cancelled, want           bool
	}{
		{name: "cancelled_body", err: context.Canceled, cancelled: true, want: true},
		{name: "active_context", err: context.Canceled},
		{name: "unexpected_eof", err: io.ErrUnexpectedEOF, want: true},
		{name: "upstream_eof", err: &net.OpError{Op: "readfrom", Net: "tcp", Err: io.ErrUnexpectedEOF}, want: true},
		{name: "body_reset", err: &net.OpError{Op: "readfrom", Net: "tcp", Err: syscall.ECONNRESET}, want: true},
		{name: "body_broken_pipe", err: &net.OpError{Op: "write", Net: "tcp", Err: syscall.EPIPE}, want: true},
		{name: "response_reset", err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}},
		{name: "dial_reset", err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNRESET}},
		{name: "unscoped_reset", err: syscall.ECONNRESET},
		{name: "plain_eof", err: io.EOF},
		{name: "deadline", err: context.DeadlineExceeded, cancelled: true},
		{name: "similar_text", err: fmt.Errorf("unexpected EOF")},
		{name: "no_error"},
		{name: "other_fault", fault: "none", err: io.ErrUnexpectedEOF},
		{name: "other_method", method: http.MethodDelete, err: io.ErrUnexpectedEOF},
		{name: "other_account", path: strings.Replace(path, "AUTH_owned", "AUTH_other", 1), err: io.ErrUnexpectedEOF},
		{name: "other_container", path: strings.Replace(path, "destination", "other", 1), err: io.ErrUnexpectedEOF},
		{name: "live_name", path: "/v1/AUTH_owned/destination/scope/selected-link", err: io.ErrUnexpectedEOF},
		{name: "other_file", path: strings.Replace(path, "selected-link", "other", 1), err: io.ErrUnexpectedEOF},
		{name: "empty_job", path: strings.Replace(path, "/job/", "//", 1), err: io.ErrUnexpectedEOF},
		{name: "nested_job", path: strings.Replace(path, "/job/", "/job/extra/", 1), err: io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fault, method, target := tc.fault, tc.method, tc.path
			if fault == "" {
				fault = "source-link"
			}
			if method == "" {
				method = http.MethodPut
			}
			if target == "" {
				target = path
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			request, err := http.NewRequestWithContext(ctx, method, "http://fixture"+target, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := uploadSourceAbort(fault, "/v1/AUTH_owned", "destination", request, tc.err); got != tc.want {
				t.Fatalf("source abort classification: got=%t want=%t error=%v", got, tc.want, tc.err)
			}
		})
	}
}

func uploadSourcePaths(t *testing.T, h *Harness, tc uploadSourceCase) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("owned symlink pathname reproduction was specified for Linux")
	}
	dst := h.Container(h.Primary, "upload-path-dst")
	segments := h.Container(h.Primary, "upload-path-segments")
	aliases := h.Container(h.Primary, "upload-path-aliases")
	other := h.Container(h.Secondary, "untouched")
	h.Seed(h.Primary, dst, "unselected", []byte("destination sentinel"), nil)
	h.Seed(h.Primary, dst, ".aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
	shared := []byte("shared bytes")
	h.Seed(h.Primary, segments, "shared/0", shared, nil)
	h.Seed(h.Primary, aliases, "dlo", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	slo := fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(shared), len(shared))
	h.SeedSLO(h.Primary, aliases, "slo", slo)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root := t.TempDir()
	seedListingLocal(t, root, "root/item", []byte("bravo"))
	canonical := seedListingLocal(t, root, "other/item", []byte("alpha"))
	first := seedListingLocal(t, root, "other/selected-first", []byte("first"))
	seedListingLocal(t, root, "unselected/keep", []byte("local sentinel"))
	if err := os.Mkdir(filepath.Join(root, "other", "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "other", "child"), filepath.Join(root, "root", "link")); err != nil {
		t.Fatal(err)
	}
	captured := canonical
	if tc.noncanonical {
		captured = root + "/root/link/../item"
	}
	if tc.fileSymlink {
		captured = filepath.Join(root, "selected-link")
		if err := os.Symlink(canonical, captured); err != nil {
			t.Fatal(err)
		}
	}
	selected, err := os.ReadFile(captured)
	if err != nil || string(selected) != "alpha" {
		t.Fatalf("captured source witness: %q %v", selected, err)
	}
	localBefore := snapshotListingLocal(t, root)
	fault := "none"
	if tc.fileSymlink && !tc.native {
		fault = "source-link"
	}
	trace := uploadRecoveryProxy(t, h, dst, uploadRecoveryCase{fault: fault})
	files := []string{captured}
	if tc.noncanonical {
		files = []string{first, captured}
	}
	app.WireFilePicker(h.Transfer, listingFilePicker{files: files})
	picked, err := h.Transfer.PickUploadFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	prefix, route := "scope/", "rclone"
	if tc.native {
		prefix, route = "native\n/", "native"
	}
	name := prefix + filepath.Base(captured)
	request := app.UploadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: dst, Prefix: prefix, SelectionToken: picked.Token}
	if tc.noncanonical {
		uploadSourceRefused(t, h, request, trace)
		h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
		h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
		if !reflect.DeepEqual(localBefore, snapshotListingLocal(t, root)) {
			t.Error("refused upload changed owned local files or links")
		}
		return
	}
	preflight, err := h.Transfer.PreflightUpload(t.Context(), request)
	if err != nil || preflight.Rejected() || len(preflight.Items) != 1 || preflight.Items[0].Path != "" || preflight.Items[0].Route != route || preflight.Items[0].Name != name {
		t.Fatalf("source preflight witness: %+v %v", preflight, err)
	}
	id, err := h.Transfer.StartUpload(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	copyCompletionAwaitPins(t, h, h.Primary)
	saved := uploadRecoveryRead(t, h.database, id)
	want := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: 5, ObjectsCopied: 1, ObjectsVerified: 1, BytesCopied: 5}
	wantState := "complete"
	copied := !tc.fileSymlink || tc.native
	if tc.native {
		want.NativeRouted = 1
	}
	if !copied {
		want = jobs.Counters{ObjectsPlanned: 1, BytesPlanned: 5, ObjectsFailed: 1}
		wantState = "partial"
	}
	if saved.Job.State != wantState || saved.Job.Counters != want ||
		(copied && saved.Job.Error != "") || (!copied && !strings.Contains(saved.Job.Error, "engine exit 6")) {
		t.Fatalf("source job witness: %+v", saved.Job)
	}
	wantItems := jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1}
	if !copied {
		wantItems.Failed, wantItems.Unresolved, wantItems.Problems = 1, 1, 1
	}
	if !saved.ItemsCaptured || saved.ItemTotals != wantItems || len(saved.Items) != 1 {
		t.Fatalf("selected source lost its recorded outcome: %+v", saved.ItemTotals)
	}
	item := saved.Items[0]
	if item.Key != name || item.Source != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: captured}) ||
		item.Destination != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: dst, Object: name}) || item.Execution == nil {
		t.Fatalf("source record changed the captured identity: %+v", item)
	}
	if tc.native && item.Staging != nil || !tc.native && (item.Staging == nil || item.Staging.Object != ".aeronautes/stage/"+id+"/batch/"+filepath.Base(captured)) {
		t.Fatalf("source record changed actual staging: %+v", item)
	}
	if copied {
		if item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: 5}) ||
			item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
			t.Fatalf("source record lost copied or verified bytes: %+v", item)
		}
	} else if item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved,
		Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeUnknown}}) || item.Verification != nil {
		t.Fatalf("failed symlink invocation lost uncertainty or invented verification: %+v", item)
	}
	assertUploadItemPages(t, h, id, map[string]jobs.ItemRecord{item.Key: item}, wantItems)
	var body []byte
	if copied {
		liveURL, err := url.Parse(h.Primary.storageURL)
		if err != nil {
			t.Fatal(err)
		}
		liveURL.Path += "/" + dst + "/" + name
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, liveURL.String(), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Auth-Token", h.Primary.token)
		response, err := h.Primary.rawClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var readErr error
		body, readErr = io.ReadAll(io.LimitReader(response.Body, 64))
		closeErr := response.Body.Close()
		if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || len(body) != 5 {
			t.Fatalf("independent live body witness: status=%d bytes=%d read=%v close=%v", response.StatusCode, len(body), readErr, closeErr)
		}
		if string(body) != string(selected) {
			t.Errorf("actual upload read a different source: selected=%q uploaded=%q", selected, body)
		}
	}
	visible, err := h.Jobs.Get(t.Context(), id)
	if err != nil || !reflect.DeepEqual(visible, saved.Job) {
		t.Errorf("public and durable source result differ: %+v %v", visible, err)
	}
	wantAudit := "applied"
	if !copied {
		wantAudit = "verify_mismatch"
	}
	if len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.local."+wantState || saved.Audit[0].Resource != "job "+id || saved.Audit[0].Result != wantAudit {
		t.Errorf("source upload audit differs: %+v", saved.Audit)
	}
	var bookmark string
	if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT bookmark_id FROM audit_events WHERE operation=? AND resource=?", "transfer.local."+wantState, "job "+id).Scan(&bookmark); err != nil || bookmark != h.Primary.BookmarkID {
		t.Errorf("source upload audit authority differs: %q %v", bookmark, err)
	}
	wantRows := []jobs.RecoveryRecord{{JobID: id, BookmarkID: h.Primary.BookmarkID, Kind: jobs.RecoveryStaging, Container: dst, Prefix: ".aeronautes/stage/" + id + "/"}}
	if !reflect.DeepEqual(saved.Rows, wantRows) || saved.Job.RecoveryRecords != 1 || saved.Status != (app.RecoveryStatusDTO{Jobs: 1, Records: 1}) {
		t.Errorf("source upload recovery differs: %+v", saved)
	}
	stage := "/" + dst + "/.aeronautes/stage/" + id + "/batch/" + filepath.Base(captured)
	stagePuts, livePuts, stageDeletes := 0, 0, 0
	for _, write := range trace.writeSnapshot() {
		switch {
		case strings.HasPrefix(write, "PUT ") && strings.HasSuffix(write, stage):
			stagePuts++
		case strings.HasPrefix(write, "PUT ") && strings.HasSuffix(write, "/"+dst+"/"+name):
			livePuts++
		case strings.HasPrefix(write, "DELETE ") && strings.HasSuffix(write, stage):
			stageDeletes++
		}
	}
	wantStage, wantLive := 0, 0
	if copied {
		wantLive = 1
		if !tc.native {
			wantStage = 1
		}
	}
	if !copied {
		// Engine exit does not acknowledge each attempted body's proxy callback.
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			trace.mu.Lock()
			aborted := trace.abortedStagePuts
			trace.mu.Unlock()
			if aborted >= stagePuts {
				break
			}
			select {
			case <-trace.stagePutAborted:
			case <-deadline.C:
				t.Fatalf("source-read aborts were not acknowledged: got=%d want=%d", aborted, stagePuts)
			}
		}
	}
	trace.mu.Lock()
	abortedStagePuts := trace.abortedStagePuts
	trace.mu.Unlock()
	stageOK := stagePuts == wantStage && abortedStagePuts == 0
	if !copied {
		// Runner.pacingArgs supplies two low-level retries; an aborted body
		// can be retried when rclone sees a transport failure before its size error.
		const lowLevelRetries = 2
		stageOK = stagePuts >= 1 && stagePuts <= 1+lowLevelRetries && abortedStagePuts == stagePuts
	}
	if !stageOK || livePuts != wantLive || stageDeletes != wantStage || len(trace.writeSnapshot()) != stagePuts+wantStage+wantLive {
		t.Errorf("actual engine stage/commit trace differs: stage_puts=%d aborted=%d live_puts=%d stage_deletes=%d writes=%v", stagePuts, abortedStagePuts, livePuts, stageDeletes, trace.writeSnapshot())
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err == nil {
		t.Error("terminal source upload accepts cancellation")
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	live, found := afterA.Containers[dst].Objects[name]
	if found != copied || (copied && (live.Size != 5 || live.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) || live.Class != "plain")) {
		t.Errorf("source upload snapshot differs from full body: %+v", live)
	}
	var allowed []string
	if copied {
		allowed = []string{dst + "/" + name}
	}
	if stray := collateral(beforeA, afterA, allowed); len(stray) != 0 {
		t.Errorf("source upload changed remote collateral: %v", stray)
	}
	h.AssertNoCollateral(beforeB, afterB)
	if !reflect.DeepEqual(localBefore, snapshotListingLocal(t, root)) {
		t.Error("source upload changed owned local files or links")
	}
	t.Logf("upload source service observation case=%s selected=%q uploaded=%q copied=%d bytes=%d stage_puts=%d live_puts=%d stage_deletes=%d aborted=%d",
		tc.name, selected, body, saved.Job.Counters.ObjectsCopied, saved.Job.Counters.BytesCopied, stagePuts, livePuts, stageDeletes, abortedStagePuts)
	requests := trace.requestCount()
	uploadRecoveryReopen(t, h, id, saved)
	if requests != trace.requestCount() {
		t.Error("source upload reopen contacted the remote")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
	if !reflect.DeepEqual(localBefore, snapshotListingLocal(t, root)) {
		t.Error("source upload reopen changed owned local files or links")
	}
}

func uploadSourceRefused(t *testing.T, h *Harness, request app.UploadRequestDTO, trace *uploadDeleteTrace) {
	t.Helper()
	counts := func() []int64 {
		var result []int64
		for _, query := range []string{
			"SELECT COUNT(*) FROM jobs",
			"SELECT COUNT(*) FROM job_recovery",
			"SELECT COUNT(*) FROM job_items",
			"SELECT COUNT(*) FROM job_item_observations",
			"SELECT COUNT(*) FROM audit_events WHERE operation LIKE 'transfer.%'",
		} {
			var count int64
			if err := h.database.Reader().QueryRowContext(t.Context(), query).Scan(&count); err != nil {
				t.Fatal(err)
			}
			result = append(result, count)
		}
		return result
	}
	before := counts()
	preflight, err := h.Transfer.PreflightUpload(t.Context(), request)
	if err != nil || !preflight.Rejected() || len(preflight.Items) != 1 || len(preflight.Problems) != 1 {
		t.Errorf("unsafe source did not refuse the whole selection: %+v %v", preflight, err)
	} else {
		problem := preflight.Problems[0]
		if problem.Name != "selection 2" || problem.Kind != "source-path" || !problem.Reject ||
			!strings.Contains(problem.Detail, "cannot be used unchanged") || !utf8.ValidString(problem.Name+problem.Detail) {
			t.Errorf("unsafe source refusal lost its safe selection label: %+v", problem)
		}
		encoded, err := json.Marshal(preflight)
		if err != nil || strings.ContainsRune(string(encoded), '\ufffd') || preflight.Items[0].Path != "" {
			t.Errorf("source preflight crossed the bridge lossily or exposed a path: %s %v", encoded, err)
		}
	}
	id, err := h.Transfer.StartUpload(t.Context(), request)
	if id != "" {
		copyCompletionAwaitPins(t, h, h.Primary)
	}
	if err == nil || id != "" {
		t.Errorf("unsafe source admitted a job: %q %v", id, err)
	}
	if after := counts(); !reflect.DeepEqual(before, after) || len(trace.writeSnapshot()) != 0 {
		t.Errorf("unsafe source admitted work: before=%v after=%v writes=%v", before, after, trace.writeSnapshot())
	}
	if err := h.jobStore.Available(); err != nil {
		t.Errorf("path refusal incorrectly made the store unavailable: %v", err)
	}
}

func uploadSourcePathsCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, tc := range []uploadSourceCase{
		{name: "canonical_engine"},
		{name: "canonical_native", native: true},
		{name: "noncanonical_engine", noncanonical: true},
		{name: "noncanonical_native", noncanonical: true, native: true},
		{name: "file_symlink_engine", fileSymlink: true},
		{name: "file_symlink_native", fileSymlink: true, native: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uploadSourcePaths(t, makeHarness(t), tc)
		})
	}
}

func TestUploadSourcePathsMock(t *testing.T) {
	t.Parallel()
	uploadSourcePathsCases(t, findMockHarness)
}

func TestUploadSourcePathsRealSwift(t *testing.T) {
	t.Parallel()
	uploadSourcePathsCases(t, New)
}
