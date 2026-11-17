package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// secondCluster stands up an independent mock account and saves a
// bookmark for it, returning the bookmark id and the mock.
func secondCluster(t *testing.T, h *harness) (string, *swifttest.Server) {
	t.Helper()
	mock := swifttest.New("test2:tester2", "testing2")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	saved, err := h.bookmarks.Save(context.Background(), BookmarkDTO{
		DisplayName: "DR", AuthURL: srv.URL + "/auth/v1.0", Username: "test2:tester2",
	}, "testing2")
	if err != nil {
		t.Fatal(err)
	}
	return saved.ID, mock
}

// httpEngine is the test engine: it moves bytes over raw HTTP using
// exactly the remotes handed to the factory — so the test also proves
// the service wired real storage URLs and tokens into the engine.
type httpEngine struct {
	remotes map[string]rclone.Remote
	block   *engineBarrier
	// chunk > 0: bodies over it are segmented into <container>_segments
	// under a generated prefix with a DLO manifest — the rclone shape.
	chunk    int
	n        int
	captured []rclone.SegmentPrefix
	// said is how the file being transferred reports itself, set for each
	// name of a Copy.
	said func(level, msg string)
}

// Tests must observe engine entry before changing a planned namespace or
// cancelling work; elapsed time does not prove that the job reached Copy.
type engineBarrier struct {
	entered     chan struct{}
	release     chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
	// entries counts the engine runs that have reached the barrier, for a
	// test that needs several jobs held in their engines together.
	entries atomic.Int32
}

func newEngineBarrier(t *testing.T) *engineBarrier {
	t.Helper()
	b := &engineBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(b.unblock)
	return b
}

func (b *engineBarrier) waitUntilEntered(t *testing.T) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("job never reached the engine barrier")
	}
}

func (b *engineBarrier) waitUntilHolding(t *testing.T, runs int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for b.entries.Load() < runs {
		if time.Now().After(deadline) {
			t.Fatalf("%d engine runs reached the barrier, want %d held together", b.entries.Load(), runs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (b *engineBarrier) unblock() {
	b.releaseOnce.Do(func() { close(b.release) })
}

func (e *httpEngine) endpoint(spec string) (base string, token string, path string) {
	name, rest, _ := strings.Cut(spec, ":")
	remote := e.remotes[name]
	return remote.StorageURL, remote.Token, rest
}

func (e *httpEngine) transfer(ctx context.Context, srcSpec, dstSpec string) error {
	var body []byte
	var contentType string
	if localPath(srcSpec) {
		data, err := os.ReadFile(srcSpec)
		if err != nil {
			return err
		}
		body, contentType = data, "application/octet-stream"
	} else {
		srcBase, srcTok, srcPath := e.endpoint(srcSpec)
		get, err := http.NewRequestWithContext(ctx, http.MethodGet, srcBase+"/"+escapePath(srcPath), nil)
		if err != nil {
			return err
		}
		get.Header.Set("X-Auth-Token", srcTok)
		resp, err := http.DefaultClient.Do(get)
		if err != nil {
			return err
		}
		body, _ = io.ReadAll(resp.Body)
		contentType = resp.Header.Get("Content-Type")
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("GET %s: %s", srcPath, resp.Status)
		}
	}
	if localPath(dstSpec) {
		if err := os.MkdirAll(filepath.Dir(dstSpec), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dstSpec, body, 0o644)
	}
	if e.chunk > 0 && len(body) > e.chunk {
		return e.segmented(ctx, dstSpec, body, contentType)
	}
	dstBase, dstTok, dstPath := e.endpoint(dstSpec)
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, dstBase+"/"+escapePath(dstPath), bytes.NewReader(body))
	if err != nil {
		return err
	}
	put.Header.Set("X-Auth-Token", dstTok)
	put.Header.Set("Content-Type", contentType)
	putResp, err := http.DefaultClient.Do(put)
	if err != nil {
		return err
	}
	putResp.Body.Close()
	if putResp.StatusCode >= 300 {
		return fmt.Errorf("PUT %s: %s", dstPath, putResp.Status)
	}
	return nil
}

// segmented writes rclone-style: chunks under <container>_segments and
// a zero-byte DLO manifest at the destination name.
func (e *httpEngine) segmented(ctx context.Context, dstSpec string, body []byte, contentType string) error {
	dstBase, dstTok, dstPath := e.endpoint(dstSpec)
	container, name, _ := strings.Cut(dstPath, "/")
	e.n++
	prefix := fmt.Sprintf("%s/2026-08-29-%022d/", name, e.n)
	segContainer := container + "_segments"
	put := func(path string, data []byte) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, dstBase+"/"+escapePath(path), bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("X-Auth-Token", dstTok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("PUT %s: %s", path, resp.Status)
		}
		return nil
	}
	if err := put(segContainer, nil); err != nil { // ensure sidecar
		return err
	}
	for i := 0; i*e.chunk < len(body); i++ {
		end := min((i+1)*e.chunk, len(body))
		segment := fmt.Sprintf("%s%08d", prefix, i)
		if err := put(segContainer+"/"+segment, body[i*e.chunk:end]); err != nil {
			return err
		}
		if e.said != nil {
			e.said("debug", fmt.Sprintf("Uploading segment file %q into %q", segment, segContainer))
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, dstBase+"/"+escapePath(dstPath), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", dstTok)
	req.Header.Set("X-Object-Manifest", segContainer+"/"+prefix)
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	e.captured = append(e.captured, rclone.SegmentPrefix{Container: segContainer, Prefix: prefix})
	return nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func (e *httpEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	if e.block != nil {
		e.block.enterOnce.Do(func() { close(e.block.entered) })
		e.block.entries.Add(1)
		select {
		case <-e.block.release:
		case <-ctx.Done():
			return &rclone.Result{Cancelled: true}, nil
		}
	}
	for _, name := range names {
		// The engine says where it puts an object's segments and which
		// objects it wrote, by the name each was listed under; every
		// transfer takes its word for both (ADR 0122 decision 7).
		e.said = func(level, msg string) {
			if onEvent != nil {
				onEvent(rclone.Event{Level: level, Object: name, Msg: msg})
			}
		}
		if err := e.transfer(ctx, src+"/"+name, dst+"/"+name); err != nil {
			return &rclone.Result{ExitCode: 1, Errors: []string{err.Error()}}, nil
		}
		e.said("info", "Copied (new)")
	}
	result := &rclone.Result{SegmentPrefixes: e.captured}
	e.captured = nil
	return result, nil
}

// localPath reports whether a spec is a plain filesystem path (upload
// source / download destination) rather than a remote spec.
func localPath(spec string) bool { return strings.HasPrefix(spec, "/") }

func (h *harness) installEngine(block *engineBarrier) *[]rclone.Options {
	return h.installChunkingEngine(block, 0)
}

func (h *harness) installChunkingEngine(block *engineBarrier, chunk int) *[]rclone.Options {
	var seen []rclone.Options
	h.transfer.setEngineFactory(func(source transfercopy.RemoteSource, opts rclone.Options) (transfercopy.Engine, error) {
		remotes, err := source(context.Background())
		if err != nil {
			return nil, err
		}
		byName := map[string]rclone.Remote{}
		for _, r := range remotes {
			byName[r.Name] = r
		}
		seen = append(seen, opts)
		return &httpEngine{remotes: byName, block: block, chunk: chunk}, nil
	})
	return &seen
}

func seedObject(mock *swifttest.Server, container, name, content string, meta map[string]string) {
	sum := md5.Sum([]byte(content))
	mock.PutObject(container, name, swifttest.Object{
		Bytes: int64(len(content)), ETag: hex.EncodeToString(sum[:]),
		ContentType: "text/plain", Meta: meta, Data: []byte(content),
	})
}

// terminalWait bounds awaitTerminal. The slowest caller, the 205-object
// preflight-view download, completes in about four seconds on a healthy
// hosted runner and eight seconds locally under the race detector; a hosted
// runner running at less than half speed exceeded the former ten-second
// bound (22 September 2026). Thirty seconds still fails a stuck job long
// before the package budget.
const terminalWait = 30 * time.Second

func awaitTerminal(t *testing.T, h *harness, jobID string) JobDTO {
	t.Helper()
	deadline := time.Now().Add(terminalWait)
	for time.Now().Before(deadline) {
		raw, err := h.jobsSvc.jobs.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		bookmark := raw.SourceBookmarkID
		if bookmark == "" {
			bookmark = raw.DestBookmarkID
		}
		job, err := h.jobsSvc.Get(context.Background(), bookmark, jobID)
		if err != nil {
			t.Fatal(err)
		}
		switch job.State {
		case "complete", "partial", "cancelled", "failed":
			return *job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job never reached a terminal state")
	return JobDTO{}
}

func awaitJobAudit(t *testing.T, h *harness, jobID, operation string) {
	t.Helper()
	// The terminal row and audit event are separate writes; observing
	// the row does not join the job goroutine's final audit append.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		events, err := h.auditSvc.Search(ctx, AuditQueryDTO{OperationPrefix: operation})
		if err != nil {
			t.Fatalf("waiting for %s audit for job %s: %v", operation, jobID, err)
		}
		for _, event := range events {
			if event.Operation == operation && (event.Summary == "job "+jobID || strings.HasPrefix(event.Summary, "job "+jobID+" · ")) {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("missing %s audit for job %s: %+v", operation, jobID, events)
		case <-ticker.C:
		}
	}
}

func TestPlanAndStartCopyAcrossBookmarks(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	srcBookmark := h.transfer.connections.ActiveBookmarkID()
	dstBookmark, dstMock := secondCluster(t, h)
	h.installEngine(nil)

	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "one.txt", "first object", map[string]string{"Shoot": "wedding"})
	seedObject(h.mock, "media", "two.txt", "second", nil)
	seedObject(h.mock, "media", "same.txt", "unchanged bytes", nil)
	dstMock.AddContainer("backup", "standard")
	seedObject(dstMock, "backup", "same.txt", "unchanged bytes", nil)
	seedObject(dstMock, "backup", "two.txt", "different old!", nil)

	req := CopyRequestDTO{
		SourceBookmarkID: srcBookmark, SourceContainer: "media",
		DestBookmarkID: dstBookmark, DestContainer: "backup",
	}

	// Preview: honest counts, no writes.
	preview, err := h.transfer.PlanCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.DataPath, "workstation") {
		t.Errorf("preview does not name its data path (K-XFER-005): %q", preview.DataPath)
	}
	if preview.Summary.New != 1 || preview.Summary.Same != 1 || preview.Summary.Changed != 1 {
		t.Fatalf("preview = %+v", preview.Summary)
	}
	if preview.VersionedDestination {
		t.Error("unversioned destination reported versioned")
	}

	// Execute.
	jobID, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "partial" {
		t.Fatalf("job = %+v", job)
	}
	if job.Counters.ObjectsCopied != 1 || job.Counters.ObjectsVerified != 2 || job.Counters.ObjectsFailed != 0 || job.Counters.OverwritesRefused != 1 || job.Counters.Overwrites != 0 {
		t.Errorf("counters = %+v", job.Counters)
	}

	// Eligible creation still carries source metadata alongside refusals.
	d, err := h.transfer.connections.sessions[dstBookmark].session.ObjectDetail(ctx, "backup", "one.txt")
	if err != nil {
		t.Fatal(err)
	}
	// The source's metadata and the modification time the engine records,
	// which the metadata request repeats for a source that states none,
	// and no stamp of the application's own.
	source, err := h.transfer.connections.sessions[srcBookmark].session.ObjectDetail(ctx, "media", "one.txt")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"Shoot": "wedding", "Mtime": strconv.FormatInt(source.LastModified.Unix(), 10)}; !reflect.DeepEqual(d.Meta, want) {
		t.Errorf("meta = %v, want %v", d.Meta, want)
	}

	// Audit: a start and a terminal event.
	awaitJobAudit(t, h, jobID, "transfer.copy.partial")
	events, err := h.auditSvc.RecentFor(ctx, h.bookmarkID(), 10)
	if err != nil {
		t.Fatal(err)
	}
	var sawStart, sawDone bool
	for _, e := range events {
		if e.Operation == "transfer.copy.start" {
			sawStart = true
		}
		if e.Operation == "transfer.copy.partial" {
			sawDone = true
			if e.Result != "verify_mismatch" || !strings.Contains(e.Summary, "1 overwrite refused") {
				t.Errorf("refusal missing from terminal audit: %+v", e)
			}
		}
	}
	if !sawStart || !sawDone {
		t.Errorf("audit trail incomplete: %+v", events)
	}

	// The §47 filter: scoped to the destination bookmark, the job shows;
	// scoped to a stranger, it does not.
	scoped, err := h.jobsSvc.List(ctx, dstBookmark, false, 10)
	if err != nil || len(scoped) != 1 {
		t.Fatalf("dest-scoped list = %d, %v", len(scoped), err)
	}
	none, err := h.jobsSvc.List(ctx, "no-such-bookmark", false, 10)
	if err != nil || len(none) != 0 {
		t.Fatalf("stranger-scoped list = %d, %v", len(none), err)
	}
}

func TestStartCopyValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	srcBookmark := h.transfer.connections.ActiveBookmarkID()
	dstBookmark, dstMock := secondCluster(t, h)
	h.installEngine(nil)
	h.mock.AddContainer("media", "standard")

	// Prefix shape enforced.
	_, err := h.transfer.StartCopy(ctx, CopyRequestDTO{
		SourceBookmarkID: srcBookmark, SourceContainer: "media", SourcePrefix: "no-slash",
		DestBookmarkID: dstBookmark, DestContainer: "backup",
	})
	if err == nil || !strings.Contains(err.Error(), "prefix") {
		t.Errorf("bad prefix accepted: %v", err)
	}

	// Missing destination container fails fast, before any job exists.
	_, err = h.transfer.StartCopy(ctx, CopyRequestDTO{
		SourceBookmarkID: srcBookmark, SourceContainer: "media",
		DestBookmarkID: dstBookmark, DestContainer: "not-there",
	})
	if err == nil || !strings.Contains(err.Error(), "destination container") {
		t.Errorf("missing dest container accepted: %v", err)
	}
	jobsList, _ := h.jobsSvc.List(ctx, "", false, 10)
	if len(jobsList) != 0 {
		t.Errorf("failed validation still created jobs: %+v", jobsList)
	}
	_ = dstMock
}

func TestCancelCopyJob(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	srcBookmark := h.transfer.connections.ActiveBookmarkID()
	dstBookmark, dstMock := secondCluster(t, h)
	block := newEngineBarrier(t)
	h.installEngine(block)

	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "stuck.txt", "will not arrive", nil)
	dstMock.AddContainer("backup", "standard")

	jobID, err := h.transfer.StartCopy(ctx, CopyRequestDTO{
		SourceBookmarkID: srcBookmark, SourceContainer: "media",
		DestBookmarkID: dstBookmark, DestContainer: "backup",
	})
	if err != nil {
		t.Fatal(err)
	}
	block.waitUntilEntered(t)
	if err := h.transfer.CancelJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	want := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: int64(len("will not arrive")), ObjectsFailed: 1}
	if job.State != "partial" || job.Counters != want || job.Error != "execution failed (example: stuck.txt: the engine stopped without reporting this object: engine child cancelled); context canceled" {
		t.Fatalf("job = %+v", job)
	}
	// Cancelling a finished job is refused.
	if err := h.transfer.CancelJob(ctx, jobID); err == nil {
		t.Error("cancel of a terminal job accepted")
	}
}

func TestUploadAndDownloadJobs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.installEngine(nil)
	h.mock.AddContainer("media", "standard")

	// Upload two files; a collision on the second run is rejected whole.
	dir := t.TempDir()
	for name, content := range map[string]string{"a.txt": "alpha", "b.txt": "bravo!"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.transfer.setFilePicker(fakePicker{
		files: []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")},
	})
	picked, err := h.transfer.PickUploadFiles(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "up/"})
	if err != nil || picked == nil || len(picked.Files) != 2 {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	if strings.Contains(strings.Join(picked.Files, " "), string(filepath.Separator)) {
		t.Fatalf("picker leaked paths across the bridge: %v", picked.Files)
	}
	upload := UploadRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "up/",
		SelectionToken: picked.Token,
	}
	preflight, err := h.transfer.PreflightUpload(ctx, upload)
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %v %+v", err, preflight)
	}
	jobID, err := h.transfer.StartUpload(ctx, upload)
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Counters.ObjectsCopied != 2 || job.Type != "upload" {
		t.Fatalf("upload job = %+v", job)
	}
	// The start consumed that selection (ADR 0041), so pick again: the
	// assertion under test is the existing-name refusal, not the
	// lifetime of a spent token.
	repicked, err := h.transfer.PickUploadFiles(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "up/"})
	if err != nil || repicked == nil {
		t.Fatalf("re-pick after the start consumed the selection: %+v %v", repicked, err)
	}
	upload.SelectionToken = repicked.Token
	preflight, err = h.transfer.PreflightUpload(ctx, upload)
	if err != nil || !preflight.Rejected() || len(preflight.Items) != 0 || len(preflight.Problems) != 2 {
		t.Fatalf("second upload preflight should refuse both existing names: %+v %v", preflight, err)
	}
	for _, problem := range preflight.Problems {
		if problem.Kind != "exists" || !problem.Reject {
			t.Errorf("second upload lost its existing-name refusal: %+v", problem)
		}
	}
	if id, err := h.transfer.StartUpload(ctx, upload); err == nil || id != "" {
		t.Errorf("second upload should refuse a job for existing names: %q %v", id, err)
	}

	// Download the uploaded prefix into a fresh directory.
	destDir := t.TempDir()
	h.transfer.setFilePicker(fakePicker{dir: destDir})
	pickedDir, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "up/"})
	if err != nil || pickedDir == nil {
		t.Fatalf("pick dir: %+v %v", pickedDir, err)
	}
	download := DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "up/", DestDirToken: pickedDir.Token}
	jobID, err = h.transfer.StartDownload(ctx, download)
	if err != nil {
		t.Fatal(err)
	}
	job = awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Counters.ObjectsCopied != 2 || job.Type != "download" {
		t.Fatalf("download job = %+v", job)
	}
	data, err := os.ReadFile(filepath.Join(destDir, "a.txt"))
	if err != nil || string(data) != "alpha" {
		t.Errorf("a.txt = %q %v", data, err)
	}
	// A token the picker never issued is refused outright.
	if _, err := h.transfer.StartUpload(ctx, UploadRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "up/", SelectionToken: "forged-token",
	}); err == nil || !strings.Contains(err.Error(), "selection") {
		t.Errorf("forged selection token = %v, want refusal", err)
	}
	// Re-running rejects on local-exists — nothing overwritten. The
	// first start consumed the selection, so pick the same directory
	// again; the assertion is the overwrite refusal, not token reuse.
	repickedDir, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "up/"})
	if err != nil || repickedDir == nil {
		t.Fatalf("re-pick destination: %+v %v", repickedDir, err)
	}
	download.DestDirToken = repickedDir.Token
	if _, err := h.transfer.StartDownload(ctx, download); err == nil || !strings.Contains(err.Error(), "problems") {
		t.Errorf("second download should hit local-exists: %v", err)
	}
}

func TestPerJobSettings(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	srcBookmark := h.transfer.connections.ActiveBookmarkID()
	dstBookmark, dstMock := secondCluster(t, h)
	seenOptions := h.installEngine(nil)

	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "one.txt", "payload", nil)
	dstMock.AddContainer("backup", "standard")

	// Hostile settings are refused before any job exists.
	bad := CopyRequestDTO{
		SourceBookmarkID: srcBookmark, SourceContainer: "media",
		DestBookmarkID: dstBookmark, DestContainer: "backup",
		Settings: TransferSettingsDTO{ChunkSize: "64Mi; rm -rf /"},
	}
	if _, err := h.transfer.StartCopy(ctx, bad); err == nil || !strings.Contains(err.Error(), "chunk size") {
		t.Errorf("hostile chunk size accepted: %v", err)
	}

	// Typed overrides reach the engine and are recorded on the job.
	req := CopyRequestDTO{
		SourceBookmarkID: srcBookmark, SourceContainer: "media",
		DestBookmarkID: dstBookmark, DestContainer: "backup",
		Settings: TransferSettingsDTO{ChunkSize: "64Mi", Transfers: 8, BWLimit: "10M"},
	}
	jobID, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" {
		t.Fatalf("job = %+v", job)
	}
	if len(*seenOptions) == 0 || (*seenOptions)[0].ChunkSize != "64Mi" || (*seenOptions)[0].Transfers != 8 {
		t.Errorf("engine options = %+v", *seenOptions)
	}
	if !strings.Contains(job.Settings, `"chunkSize":"64Mi"`) {
		t.Errorf("effective settings not recorded: %q", job.Settings)
	}
}

func TestSchedulerBudgetQueues(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	srcBookmark := h.transfer.connections.ActiveBookmarkID()
	dstBookmark, dstMock := secondCluster(t, h)
	block := newEngineBarrier(t)
	h.installEngine(block)
	h.transfer.runner.setMaxActiveJobs(1)

	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "one.txt", "payload", nil)
	dstMock.AddContainer("backup", "standard")
	req := CopyRequestDTO{
		SourceBookmarkID: srcBookmark, SourceContainer: "media",
		DestBookmarkID: dstBookmark, DestContainer: "backup",
	}

	first, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	third, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	// The budget is 1: the first job runs (blocked in the engine), the
	// others stay Queued.
	block.waitUntilEntered(t)
	j1, _ := h.jobsSvc.Get(ctx, h.bookmarkID(), first)
	j2, _ := h.jobsSvc.Get(ctx, h.bookmarkID(), second)
	j3, _ := h.jobsSvc.Get(ctx, h.bookmarkID(), third)
	if j1.State != "running" || j2.State != "queued" || j3.State != "queued" {
		t.Fatalf("states = %s / %s / %s, want running / queued / queued", j1.State, j2.State, j3.State)
	}

	// A queued job cancels without ever starting.
	if err := h.transfer.CancelJob(ctx, third); err != nil {
		t.Fatal(err)
	}
	if got := awaitTerminal(t, h, third); got.State != "cancelled" || got.StartedAt != "" {
		t.Fatalf("queued cancel = %+v (a queued job must never have started)", got)
	}

	// Freeing the slot lets the second run to completion.
	block.unblock()
	if got := awaitTerminal(t, h, first); got.State != "complete" {
		t.Fatalf("first = %+v", got)
	}
	if got := awaitTerminal(t, h, second); got.State != "complete" {
		t.Fatalf("second = %+v", got)
	}
}

// droppingEngine claims success while copying nothing.
type droppingEngine struct{ httpEngine }

func (e *droppingEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	return &rclone.Result{}, nil
}

func TestFindInContainer(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	find := NewFindService(h.transfer.connections)
	t.Cleanup(func() {
		if err := find.ServiceShutdown(); err != nil {
			t.Errorf("Find cleanup: %v", err)
		}
	})
	h.mock.AddContainer("media", "standard")
	for i := 0; i < 40; i++ {
		seedObject(h.mock, "media", fmt.Sprintf("photos/img-%04d.jpg", i), "x", nil)
	}
	seedObject(h.mock, "media", "docs/Report-Final.pdf", "pdf", nil)
	seedObject(h.mock, "media", "docs/report-draft.pdf", "pdf", nil)

	id, err := find.StartFind(ctx, findBM(find), "media", "", "REPORT", false)
	if err != nil {
		t.Fatal(err)
	}
	var status *FindStatusDTO
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err = find.FindStatus(ctx, findBM(find), id)
		if err != nil {
			t.Fatal(err)
		}
		if status.Done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !status.Done || status.Error != "" {
		t.Fatalf("status = %+v", status)
	}
	// Case-insensitive, both reports, whole namespace scanned.
	if len(status.Matches) != 2 || status.Scanned != 42 {
		t.Fatalf("matches = %+v scanned = %d", status.Matches, status.Scanned)
	}
	if _, err := find.StartFind(ctx, findBM(find), "media", "", "   ", false); err == nil {
		t.Error("blank query accepted")
	}
	if err := find.CancelFind(ctx, findBM(find), id); err != nil {
		t.Error(err)
	}
	if _, err := find.FindStatus(ctx, findBM(find), id); err == nil {
		t.Error("cancelled scan still answers")
	}
}

func TestRecoveryStatusRetainsCrashData(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("backup", "standard")
	h.mock.AddContainer("backup_segments", "standard")

	// Simulate a crashed copy job: terminal row + persisted recovery
	// records + real litter on the cluster, with one captured prefix
	// COMMITTED (its live manifest must survive) and one orphaned.
	crashed, err := h.transfer.jobs.Create(ctx, jobs.Job{Type: jobs.TypeCopy, DestBookmarkID: bookmark})
	if err != nil {
		t.Fatal(err)
	}
	_ = h.transfer.jobs.Transition(ctx, crashed.ID, jobs.StatePlanning)
	_ = h.transfer.jobs.Transition(ctx, crashed.ID, jobs.StateCancelled)

	staging := ".aeronautes/stage/" + crashed.ID + "/"
	seedObject(h.mock, "backup", staging+"000001", "stranded staged bytes", nil)
	committedPrefix := "kept.bin/2026-08-30-0000000000000000000001/"
	orphanPrefix := "lost.bin/2026-08-30-0000000000000000000002/"
	seedObject(h.mock, "backup_segments", committedPrefix+"00000000", "kept-seg", nil)
	h.mock.PutObject("backup", "kept.bin", swifttest.Object{Manifest: "backup_segments/" + committedPrefix})
	for i := 0; i < 2; i++ {
		seedObject(h.mock, "backup_segments", fmt.Sprintf("%s%08d", orphanPrefix, i), "orphan-seg", nil)
	}
	for _, r := range []jobs.RecoveryRecord{
		{JobID: crashed.ID, BookmarkID: bookmark, Kind: jobs.RecoveryStaging, Container: "backup", Prefix: staging},
		{JobID: crashed.ID, BookmarkID: bookmark, Kind: jobs.RecoveryCaptured, Container: "backup_segments", Prefix: committedPrefix},
		{JobID: crashed.ID, BookmarkID: bookmark, Kind: jobs.RecoveryCommitted, Container: "backup_segments", Prefix: committedPrefix},
		{JobID: crashed.ID, BookmarkID: bookmark, Kind: jobs.RecoveryCaptured, Container: "backup_segments", Prefix: orphanPrefix},
	} {
		if err := h.transfer.jobs.RecoveryAdd(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	status, err := h.jobsSvc.RecoveryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status != (RecoveryStatusDTO{Jobs: 1, Records: 4}) {
		t.Fatalf("retained recovery = %+v", status)
	}
	session := h.transfer.connections.sessions[bookmark].session
	if _, err := session.ObjectDetail(ctx, "backup", staging+"000001"); err != nil {
		t.Fatalf("stranded staging object not retained: %v", err)
	}
	if _, err := session.ObjectDetail(ctx, "backup_segments", orphanPrefix+"00000000"); err != nil {
		t.Fatalf("uncertain segment not retained: %v", err)
	}
	// The committed manifest still assembles.
	d, err := session.ObjectDetail(ctx, "backup", "kept.bin")
	if err != nil || d.ContentLength != int64(len("kept-seg")) {
		t.Fatalf("committed DLO harmed: %+v %v", d, err)
	}
	// Repeated recovery cannot turn uncertainty into deletion authority.
	if pending, err := h.transfer.jobs.RecoveryPending(ctx); err != nil || len(pending) != 4 {
		t.Fatalf("recovery records not retained: %+v, %v", pending, err)
	}
	if next, err := h.jobsSvc.RecoveryStatus(ctx); err != nil || next != status {
		t.Fatalf("repeated recovery status = %+v, %v", next, err)
	}
}

func TestJobRecordsRecoveryDurably(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	srcBookmark := h.transfer.connections.ActiveBookmarkID()
	dstBookmark, dstMock := secondCluster(t, h)
	h.installChunkingEngine(nil, 1024)
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "big.bin", strings.Repeat("b", 4000), nil)
	dstMock.AddContainer("backup", "standard")

	// Where the engine put a large object's segments is kept as the engine
	// reported it, and nothing else is: a copy stages nothing and commits
	// nothing (ADR 0122 decisions 4 and 8).
	jobID, err := h.transfer.StartCopy(ctx, CopyRequestDTO{
		SourceBookmarkID: srcBookmark, SourceContainer: "media",
		DestBookmarkID: dstBookmark, DestContainer: "backup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" {
		t.Fatalf("job = %+v", job)
	}
	pending, err := h.transfer.jobs.RecoveryPending(ctx)
	if err != nil || len(pending) != 1 || pending[0].Kind != jobs.RecoveryCaptured ||
		pending[0].Container != "backup_segments" || !strings.HasPrefix(pending[0].Prefix, "big.bin/") {
		t.Fatalf("recovery records = %+v, %v; want the one segment path the engine reported", pending, err)
	}
}

func TestRerunJob(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	srcBookmark := h.transfer.connections.ActiveBookmarkID()
	dstBookmark, dstMock := secondCluster(t, h)
	seen := h.installEngine(nil)

	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "scope/a.txt", "alpha", nil)
	seedObject(h.mock, "media", "scope/b.txt", "bravo", nil)
	dstMock.AddContainer("backup", "standard")

	first, err := h.transfer.StartCopy(ctx, CopyRequestDTO{
		SourceBookmarkID: srcBookmark, SourceContainer: "media", SourcePrefix: "scope/",
		DestBookmarkID: dstBookmark, DestContainer: "backup", DestPrefix: "mirror/",
		Settings: TransferSettingsDTO{Transfers: 9},
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, first); job.State != "complete" {
		t.Fatalf("first = %+v", job)
	}

	if _, err := h.transfer.RerunJob(ctx, "nonexistent"); err == nil {
		t.Fatal("missing job accepted")
	}

	prior, err := h.transfer.jobs.Get(ctx, first)
	if err != nil || prior.ItemRecordsVersion != 1 {
		t.Fatalf("actual Copy did not capture item history: %+v %v", prior, err)
	}
	beforeItems, err := h.transfer.jobs.ReadItems(ctx, first, 0, 100)
	if err != nil || len(beforeItems.Items) != 2 || beforeItems.NextCursor != 0 {
		t.Fatalf("actual Copy history incomplete: %+v %v", beforeItems, err)
	}
	for _, item := range beforeItems.Items {
		if !item.Admitted || item.Execution == nil || item.Execution.Result != jobs.ItemCopied || item.Verification == nil || item.Verification.Result != jobs.ItemVerified {
			t.Fatalf("actual Copy history does not establish inspected outcomes: %+v", item)
		}
	}
	seedObject(h.mock, "media", "scope/new.txt", "new current source", nil)
	rerun, err := h.transfer.RerunJob(ctx, prior.ID)
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, rerun)
	if job.State != "complete" {
		t.Fatalf("rerun = %+v", job)
	}
	// Current planning sees the new source name outside the retained admission.
	if rerun == first || rerun == prior.ID || job.Counters.ObjectsCopied != 1 || job.Counters.ObjectsPlanned != 1 {
		t.Fatalf("rerun did not plan current state: id=%s counters=%+v", rerun, job.Counters)
	}
	// Scope and effective settings carried over exactly.
	if job.Source != "media/scope/" || job.Destination != "backup/mirror/" {
		t.Fatalf("scope = %s → %s", job.Source, job.Destination)
	}
	if !strings.Contains(job.Settings, `"transfers":9`) {
		t.Fatalf("settings not carried: %s", job.Settings)
	}
	if len(*seen) < 1 || (*seen)[len(*seen)-1].Transfers != 9 {
		t.Fatalf("engine options on rerun: %+v", *seen)
	}
	afterItems, err := h.transfer.jobs.ReadItems(ctx, first, 0, 100)
	if err != nil || !reflect.DeepEqual(beforeItems, afterItems) {
		t.Fatalf("fresh rerun rewrote its predecessor: %+v %v", afterItems, err)
	}
}

func TestRerunHistoricalMirrorIsUnavailable(t *testing.T) {
	h := newHarness(t)
	job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeMirror})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.transfer.jobs.Transition(t.Context(), job.ID, jobs.StateCancelled); err != nil {
		t.Fatal(err)
	}
	if id, err := h.transfer.RerunJob(t.Context(), job.ID); id != "" || err == nil || !strings.Contains(err.Error(), "Mirror is unavailable") {
		t.Fatalf("historical Mirror rerun did not explain refusal: %q, %v", id, err)
	}
}

func TestCrashSweepCommitBoundary(t *testing.T) {
	// The review-wave C-01 sequence, both sides of the boundary. A
	// staged DLO's live manifest PUT LANDED but the process died before
	// the committed record: the sweep must resolve from the committing
	// record's live identity and KEEP the segments — the old code
	// deleted the staging namespace first, derived the staged name,
	// found it gone, and destroyed the live object's segments.
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("backup", "standard")
	h.mock.AddContainer("backup_segments", "standard")

	newCrashed := func() string {
		t.Helper()
		crashed, err := h.transfer.jobs.Create(ctx, jobs.Job{Type: jobs.TypeCopy, DestBookmarkID: bookmark})
		if err != nil {
			t.Fatal(err)
		}
		_ = h.transfer.jobs.Transition(ctx, crashed.ID, jobs.StatePlanning)
		_ = h.transfer.jobs.Transition(ctx, crashed.ID, jobs.StateCancelled)
		return crashed.ID
	}

	// Case A: commit landed, committed record lost.
	jobA := newCrashed()
	stagingA := ".aeronautes/stage/" + jobA + "/"
	prefixA := stagingA + "000001/2026-08-30-0000000000000000000011/"
	seedObject(h.mock, "backup", stagingA+"000001", "", nil) // staged manifest placeholder
	for i := 0; i < 3; i++ {
		seedObject(h.mock, "backup_segments", fmt.Sprintf("%s%08d", prefixA, i), "segment-bytes", nil)
	}
	h.mock.PutObject("backup", "video.bin", swifttest.Object{Manifest: "backup_segments/" + prefixA})
	for _, r := range []jobs.RecoveryRecord{
		{JobID: jobA, BookmarkID: bookmark, Kind: jobs.RecoveryStaging, Container: "backup", Prefix: stagingA},
		{JobID: jobA, BookmarkID: bookmark, Kind: jobs.RecoveryCaptured, Container: "backup_segments", Prefix: prefixA},
		{JobID: jobA, BookmarkID: bookmark, Kind: jobs.RecoveryCommitting, Container: "backup_segments", Prefix: prefixA, Object: "backup/video.bin"},
	} {
		if err := h.transfer.jobs.RecoveryAdd(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	// Missing live names do not rule out outside aliases or later writes.
	jobB := newCrashed()
	stagingB := ".aeronautes/stage/" + jobB + "/"
	prefixB := stagingB + "000001/2026-08-30-0000000000000000000022/"
	seedObject(h.mock, "backup", stagingB+"000001", "", nil)
	for i := 0; i < 2; i++ {
		seedObject(h.mock, "backup_segments", fmt.Sprintf("%s%08d", prefixB, i), "doomed", nil)
	}
	for _, r := range []jobs.RecoveryRecord{
		{JobID: jobB, BookmarkID: bookmark, Kind: jobs.RecoveryStaging, Container: "backup", Prefix: stagingB},
		{JobID: jobB, BookmarkID: bookmark, Kind: jobs.RecoveryCaptured, Container: "backup_segments", Prefix: prefixB},
		{JobID: jobB, BookmarkID: bookmark, Kind: jobs.RecoveryCommitting, Container: "backup_segments", Prefix: prefixB, Object: "backup/never-made-it.bin"},
	} {
		if err := h.transfer.jobs.RecoveryAdd(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := h.jobsSvc.RecoveryStatus(ctx); err != nil {
		t.Fatal(err)
	}

	// Case A: the live DLO still assembles — its segments survived.
	session := h.transfer.connections.sessions[bookmark].session
	d, err := session.ObjectDetail(ctx, "backup", "video.bin")
	if err != nil || d.ContentLength != 3*int64(len("segment-bytes")) {
		t.Fatalf("live DLO after sweep = %+v %v (its segments were destroyed)", d, err)
	}
	if _, err := session.ObjectDetail(ctx, "backup", stagingA+"000001"); err != nil {
		t.Errorf("uncertain staged manifest not retained: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := session.ObjectDetail(ctx, "backup_segments", fmt.Sprintf("%s%08d", prefixB, i)); err != nil {
			t.Errorf("uncertain segment %d not retained: %v", i, err)
		}
	}
	pending, err := h.transfer.jobs.RecoveryPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 6 {
		t.Errorf("recovery records not retained: %+v", pending)
	}
}

func TestOverlappingScopesRefused(t *testing.T) {
	// The review-wave C-03 pair: src a/z/ → dst a/ (deterministic
	// source self-deletion under Mirror) and src a/ → dst a/z/
	// (recursive output ingestion). A destination equal to or inside the
	// source refuses at resolve time for Copy preview and start, even when
	// the two bookmark IDs differ but resolve to the same account. A Copy
	// into the source's parent is allowed (DESIGN §65.9 item 9, amended);
	// Mirror refuses every nesting.
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "a/z/file", "payload", nil)

	cases := []struct{ srcPrefix, dstPrefix string }{
		{"a/", "a/z/"},
		{"a/", "a/"},
		{"", "a/"},
	}
	for _, tc := range cases {
		req := CopyRequestDTO{
			SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: tc.srcPrefix,
			DestBookmarkID: bookmark, DestContainer: "media", DestPrefix: tc.dstPrefix,
		}
		if _, err := h.transfer.PlanCopy(ctx, req); err == nil || !strings.Contains(err.Error(), "overlap") {
			t.Errorf("PlanCopy %q→%q = %v, want overlap refusal", tc.srcPrefix, tc.dstPrefix, err)
		}
		if _, err := h.transfer.StartCopy(ctx, req); err == nil {
			t.Errorf("StartCopy %q→%q accepted", tc.srcPrefix, tc.dstPrefix)
		}
		if _, err := h.transfer.StartMirror(ctx, MirrorRequestDTO{CopyRequestDTO: req, Confirm: "media"}); err == nil {
			t.Errorf("StartMirror %q→%q accepted", tc.srcPrefix, tc.dstPrefix)
		}
	}

	// TWO bookmark ids naming ONE account still refuse: the identity is
	// the canonicalized resolved endpoint, never the bookmark id.
	twin, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "same-account twin", AuthURL: h.authURL, Username: "test:tester",
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.transfer.PlanCopy(ctx, CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "a/",
		DestBookmarkID: twin.ID, DestContainer: "media", DestPrefix: "a/z/",
	}); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Errorf("dual-bookmark same-account overlap = %v, want refusal", err)
	}

	// A copy up into the source's parent plans, and Mirror still refuses it.
	up := CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "a/z/",
		DestBookmarkID: bookmark, DestContainer: "media", DestPrefix: "a/",
	}
	if _, err := h.transfer.PlanCopy(ctx, up); err != nil {
		t.Errorf("copy up a/z/ → a/ refused: %v", err)
	}
	if _, err := h.transfer.StartMirror(ctx, MirrorRequestDTO{CopyRequestDTO: up, Confirm: "media"}); err == nil {
		t.Error("StartMirror a/z/ → a/ accepted")
	}

	// Disjoint prefixes in one container stay allowed.
	seedObject(h.mock, "media", "in/x.txt", "x", nil)
	okReq := CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "in/",
		DestBookmarkID: bookmark, DestContainer: "media", DestPrefix: "out/",
	}
	if _, err := h.transfer.PlanCopy(ctx, okReq); err != nil {
		t.Errorf("disjoint same-container copy refused: %v", err)
	}
}

// fakePicker satisfies FilePicker for tests.
type fakePicker struct {
	files []string
	dir   string
}

func (f fakePicker) PickFiles(string) ([]string, error)   { return f.files, nil }
func (f fakePicker) PickDirectory(string) (string, error) { return f.dir, nil }

func TestCrashSweepProductionSlashPair(t *testing.T) {
	// The follow-up review's C01-slash sequence, with PRODUCTION forms:
	// the captured row is slash-terminated (log-derived), the committing
	// row and the live X-Object-Manifest are UNSLASHED (rclone's header
	// form), and the staged object is already gone. The sweep must still
	// match them and KEEP the live DLO's segments.
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("backup", "standard")
	h.mock.AddContainer("backup_segments", "standard")

	crashed, err := h.transfer.jobs.Create(ctx, jobs.Job{Type: jobs.TypeCopy, DestBookmarkID: bookmark})
	if err != nil {
		t.Fatal(err)
	}
	_ = h.transfer.jobs.Transition(ctx, crashed.ID, jobs.StatePlanning)
	_ = h.transfer.jobs.Transition(ctx, crashed.ID, jobs.StateCancelled)

	staging := ".aeronautes/stage/" + crashed.ID + "/"
	prefix := staging + "000001/2026-08-30-0000000000000000000033/" // canonical captured form
	for i := 0; i < 2; i++ {
		seedObject(h.mock, "backup_segments", fmt.Sprintf("%s%08d", prefix, i), "live-bytes", nil)
	}
	// Live manifest header WITHOUT the trailing slash — rclone's form.
	h.mock.PutObject("backup", "video.bin", swifttest.Object{Manifest: "backup_segments/" + strings.TrimSuffix(prefix, "/")})
	// No staged manifest object: the staging namespace is already gone.
	for _, r := range []jobs.RecoveryRecord{
		{JobID: crashed.ID, BookmarkID: bookmark, Kind: jobs.RecoveryStaging, Container: "backup", Prefix: staging},
		{JobID: crashed.ID, BookmarkID: bookmark, Kind: jobs.RecoveryCaptured, Container: "backup_segments", Prefix: prefix},
		// Committing row planted UNSLASHED, as an older build (or the
		// raw header path) would have written it.
		{JobID: crashed.ID, BookmarkID: bookmark, Kind: jobs.RecoveryCommitting, Container: "backup_segments", Prefix: strings.TrimSuffix(prefix, "/"), Object: "backup/video.bin"},
	} {
		if err := h.transfer.jobs.RecoveryAdd(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := h.jobsSvc.RecoveryStatus(ctx); err != nil {
		t.Fatal(err)
	}
	session := h.transfer.connections.sessions[bookmark].session
	d, err := session.ObjectDetail(ctx, "backup", "video.bin")
	if err != nil || d.ContentLength != 2*int64(len("live-bytes")) {
		t.Fatalf("live DLO after sweep = %+v %v (C01-slash: its segments were destroyed)", d, err)
	}
}

func TestRecoveryStatusRetainsLaterRecords(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()

	status, err := h.jobsSvc.RecoveryStatus(ctx)
	if err != nil || status != (RecoveryStatusDTO{}) {
		t.Fatalf("empty status = %+v, %v", status, err)
	}

	// A job "crashes later in the same run": terminal row + recovery
	// records + real litter appear.
	h.mock.AddContainer("backup", "standard")
	h.mock.AddContainer("backup_segments", "standard")
	crashed, err := h.transfer.jobs.Create(ctx, jobs.Job{Type: jobs.TypeCopy, DestBookmarkID: bookmark})
	if err != nil {
		t.Fatal(err)
	}
	_ = h.transfer.jobs.Transition(ctx, crashed.ID, jobs.StatePlanning)
	_ = h.transfer.jobs.Transition(ctx, crashed.ID, jobs.StateCancelled)
	prefix := "orphan/2026-08-30-0000000000000000000099/"
	seedObject(h.mock, "backup_segments", prefix+"00000000", "litter", nil)
	for _, r := range []jobs.RecoveryRecord{
		{JobID: crashed.ID, BookmarkID: bookmark, Kind: jobs.RecoveryStaging, Container: "backup", Prefix: ".aeronautes/stage/" + crashed.ID + "/"},
		{JobID: crashed.ID, BookmarkID: bookmark, Kind: jobs.RecoveryCaptured, Container: "backup_segments", Prefix: prefix},
	} {
		if err := h.transfer.jobs.RecoveryAdd(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	// Observing newly retained work cannot authorise its deletion.
	status, err = h.jobsSvc.RecoveryStatus(ctx)
	if err != nil || status != (RecoveryStatusDTO{Jobs: 1, Records: 2}) {
		t.Fatalf("post-crash status = %+v, %v", status, err)
	}
	session := h.transfer.connections.sessions[bookmark].session
	if _, err := session.ObjectDetail(ctx, "backup_segments", prefix+"00000000"); err != nil {
		t.Fatalf("uncertain segment not retained: %v", err)
	}
}

func TestOverlapFailsClosedOnAccountAliases(t *testing.T) {
	// R-04: two bookmarks that resolve to the SAME account via different
	// hosts must NOT bypass the overlap guard — identity is the account
	// segment (AUTH_xxx), not the host string, and ambiguity fails
	// closed.
	if provenDistinctAccounts(
		"https://vip1.example/v1/AUTH_shared",
		"https://vip2.example/v1/AUTH_shared") {
		t.Error("two hosts for one account were treated as distinct accounts")
	}
	// Genuinely different accounts are provably distinct → allowed.
	if !provenDistinctAccounts(
		"https://a.example/v1/AUTH_alice",
		"https://b.example/v1/AUTH_bob") {
		t.Error("different accounts not recognized as distinct")
	}
	// An unresolvable identity is NOT proven distinct (fail closed).
	if provenDistinctAccounts("https://x.example/weird", "https://y.example/weird") {
		t.Error("ambiguous endpoints treated as distinct (should fail closed)")
	}
}

// The overlap refusal says in plain words where the two scopes meet.
func TestDescribeOverlap(t *testing.T) {
	cases := []struct {
		source, dest string
		same         bool
		want         string
	}{
		{"", "", true, `both are the whole of container "testing", in the same account`},
		{"a/", "a/b/", true, `the destination, prefix "a/b/" of container "testing", is inside the source, prefix "a/" of container "testing", in the same account`},
		{"", "a/", false, `the destination, prefix "a/" of container "testing", is inside the source, the whole of container "testing", and the two bookmarks cannot be shown to be different accounts`},
	}
	for _, c := range cases {
		if got := describeOverlap("testing", c.source, c.dest, c.same); got != c.want {
			t.Errorf("describeOverlap(%q, %q, %v) = %q, want %q", c.source, c.dest, c.same, got, c.want)
		}
	}
}

// A copy from a folder up to the top of its own container lands the objects
// at the top and leaves the sources; one source name that would land back
// inside the source folder refuses the copy before anything is written
// (DESIGN §65.9 item 9, 0.5.0 test run).
func TestCopyUpIntoTheSourceParent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	h.installEngine(nil)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("media", "standard")
	// The folder's marker, as a folder made in the application has: it must
	// stay in files/ and never be written to the empty name at the top.
	seedObject(h.mock, "media", "files/", "", nil)
	seedObject(h.mock, "media", "files/one.txt", "first object", nil)
	seedObject(h.mock, "media", "files/two.txt", "second", nil)
	req := CopyRequestDTO{
		SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "files/",
		DestBookmarkID: bookmark, DestContainer: "media", DestPrefix: "",
	}
	preview, err := h.transfer.PlanCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary.New != 2 {
		t.Fatalf("preview = %+v, want 2 new", preview.Summary)
	}
	jobID, err := h.transfer.StartCopy(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" || job.Counters.ObjectsCopied != 2 {
		t.Fatalf("job = %+v", job)
	}
	session := h.transfer.connections.sessions[bookmark].session
	for name, payload := range map[string]string{"one.txt": "first object", "two.txt": "second", "files/one.txt": "first object", "files/two.txt": "second"} {
		d, err := session.ObjectDetail(ctx, "media", name)
		if err != nil || d.ContentLength != int64(len(payload)) {
			t.Errorf("%s after the copy: %+v, %v", name, d, err)
		}
	}

	seedObject(h.mock, "media", "files/files/inner.txt", "nested", nil)
	if _, err := h.transfer.PlanCopy(ctx, req); err == nil || !strings.Contains(err.Error(), `would write "files/inner.txt" back inside the source folder "files/"`) {
		t.Errorf("PlanCopy with a name landing back in the source = %v", err)
	}
	if jobID, err := h.transfer.StartCopy(ctx, req); err == nil {
		if job := awaitTerminal(t, h, jobID); job.State != "failed" || !strings.Contains(job.Error, "back inside the source folder") {
			t.Errorf("StartCopy with a name landing back in the source: job = %+v", job)
		}
	}
	if _, err := session.ObjectDetail(ctx, "media", "files/inner.txt"); err == nil {
		t.Error("files/inner.txt was written inside the source folder")
	}
}
