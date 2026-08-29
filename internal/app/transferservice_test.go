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
	"strings"
	"testing"
	"time"

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
	block   chan struct{} // non-nil: Copy blocks until ctx dies (cancel tests)
	// chunk > 0: bodies over it are segmented into <container>_segments
	// under a generated prefix with a DLO manifest — the rclone shape.
	chunk    int
	n        int
	captured []rclone.SegmentPrefix
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
		if err := put(fmt.Sprintf("%s/%s%08d", segContainer, prefix, i), body[i*e.chunk:end]); err != nil {
			return err
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
		select {
		case <-e.block:
		case <-ctx.Done():
			return &rclone.Result{Cancelled: true}, nil
		}
	}
	for _, name := range names {
		if err := e.transfer(ctx, src+"/"+name, dst+"/"+name); err != nil {
			return &rclone.Result{ExitCode: 1, Errors: []string{err.Error()}}, nil
		}
	}
	result := &rclone.Result{SegmentPrefixes: e.captured}
	e.captured = nil
	return result, nil
}

// localPath reports whether a spec is a plain filesystem path (upload
// source / download destination) rather than a remote spec.
func localPath(spec string) bool { return strings.HasPrefix(spec, "/") }

func (e *httpEngine) CopyTo(ctx context.Context, src, dst string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	if err := e.transfer(ctx, src, dst); err != nil {
		return &rclone.Result{ExitCode: 1, Errors: []string{err.Error()}}, nil
	}
	result := &rclone.Result{SegmentPrefixes: e.captured}
	e.captured = nil
	return result, nil
}

func (h *harness) installEngine(block chan struct{}) *[]rclone.Options {
	return h.installChunkingEngine(block, 0)
}

func (h *harness) installChunkingEngine(block chan struct{}, chunk int) *[]rclone.Options {
	var seen []rclone.Options
	h.transfer.SetEngineFactory(func(remotes []rclone.Remote, opts rclone.Options) (transfercopy.Engine, error) {
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

func awaitTerminal(t *testing.T, h *harness, jobID string) JobDTO {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := h.jobsSvc.Get(context.Background(), jobID)
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
	if job.State != "complete" {
		t.Fatalf("job = %+v", job)
	}
	if job.Counters.ObjectsCopied != 2 || job.Counters.ObjectsVerified == 0 {
		t.Errorf("counters = %+v", job.Counters)
	}

	// The overwrite committed and metadata rode the §27.4 apply.
	d, err := h.transfer.connections.sessions[dstBookmark].ObjectDetail(ctx, "backup", "one.txt")
	if err != nil {
		t.Fatal(err)
	}
	if d.Meta["Shoot"] != "wedding" || d.Meta["Aeronautes-Fingerprint"] == "" {
		t.Errorf("meta = %v", d.Meta)
	}

	// Audit: a start and a terminal event.
	events, err := h.auditSvc.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var sawStart, sawDone bool
	for _, e := range events {
		if e.Operation == "transfer.copy.start" {
			sawStart = true
		}
		if e.Operation == "transfer.copy.complete" {
			sawDone = true
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
	block := make(chan struct{})
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
	// Give the goroutine a moment to reach the blocking engine call.
	time.Sleep(100 * time.Millisecond)
	if err := h.transfer.CancelJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "cancelled" {
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
	upload := UploadRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "up/",
		Paths: []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")},
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
	if _, err := h.transfer.StartUpload(ctx, upload); err == nil || !strings.Contains(err.Error(), "collisions") {
		t.Errorf("second upload should hit exists-collisions: %v", err)
	}

	// Download the uploaded prefix into a fresh directory.
	destDir := t.TempDir()
	download := DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "up/", DestDir: destDir}
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
	// Re-running rejects on local-exists — nothing overwritten.
	if _, err := h.transfer.StartDownload(ctx, download); err == nil || !strings.Contains(err.Error(), "collisions") {
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
	block := make(chan struct{})
	h.installEngine(block)
	h.transfer.SetMaxActiveJobs(1)

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
	time.Sleep(200 * time.Millisecond)
	j1, _ := h.jobsSvc.Get(ctx, first)
	j2, _ := h.jobsSvc.Get(ctx, second)
	j3, _ := h.jobsSvc.Get(ctx, third)
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
	close(block)
	if got := awaitTerminal(t, h, first); got.State != "complete" {
		t.Fatalf("first = %+v", got)
	}
	if got := awaitTerminal(t, h, second); got.State != "complete" {
		t.Fatalf("second = %+v", got)
	}
}
