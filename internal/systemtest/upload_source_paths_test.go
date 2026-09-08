package systemtest

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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
	wantStagePuts := wantStage
	if !copied {
		wantStagePuts = 1
	}
	if stagePuts != wantStagePuts || livePuts != wantLive || stageDeletes != wantStage || len(trace.writeSnapshot()) != wantStagePuts+wantStage+wantLive {
		t.Errorf("actual engine stage/commit trace differs: stage_puts=%d live_puts=%d stage_deletes=%d writes=%v", stagePuts, livePuts, stageDeletes, trace.writeSnapshot())
	}
	if !copied {
		// Engine exit does not acknowledge the proxy's cancellation handler.
		select {
		case <-trace.stagePutCancelled:
		case <-time.After(5 * time.Second):
			t.Fatal("source-read cancellation was not acknowledged")
		}
	}
	trace.mu.Lock()
	cancelledStagePuts := trace.cancelledStagePuts
	trace.mu.Unlock()
	wantCancelled := 0
	if !copied {
		wantCancelled = 1
	}
	if cancelledStagePuts != wantCancelled {
		t.Errorf("source-read cancellation witness differs: got=%d want=%d", cancelledStagePuts, wantCancelled)
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
	t.Logf("upload source service observation case=%s selected=%q uploaded=%q copied=%d bytes=%d stage_puts=%d live_puts=%d stage_deletes=%d",
		tc.name, selected, body, saved.Job.Counters.ObjectsCopied, saved.Job.Counters.BytesCopied, stagePuts, livePuts, stageDeletes)
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
	uploadSourcePathsCases(t, findMockHarness)
}

func TestUploadSourcePathsRealSwift(t *testing.T) {
	uploadSourcePathsCases(t, New)
}
