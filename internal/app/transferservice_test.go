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
}

func (e *httpEngine) endpoint(spec string) (base string, token string, path string) {
	name, rest, _ := strings.Cut(spec, ":")
	remote := e.remotes[name]
	return remote.StorageURL, remote.Token, rest
}

func (e *httpEngine) transfer(ctx context.Context, srcSpec, dstSpec string) error {
	srcBase, srcTok, srcPath := e.endpoint(srcSpec)
	dstBase, dstTok, dstPath := e.endpoint(dstSpec)

	get, err := http.NewRequestWithContext(ctx, http.MethodGet, srcBase+"/"+escapePath(srcPath), nil)
	if err != nil {
		return err
	}
	get.Header.Set("X-Auth-Token", srcTok)
	resp, err := http.DefaultClient.Do(get)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GET %s: %s", srcPath, resp.Status)
	}
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, dstBase+"/"+escapePath(dstPath), bytes.NewReader(body))
	if err != nil {
		return err
	}
	put.Header.Set("X-Auth-Token", dstTok)
	put.Header.Set("Content-Type", resp.Header.Get("Content-Type"))
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
	return &rclone.Result{}, nil
}

func (e *httpEngine) CopyTo(ctx context.Context, src, dst string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	if err := e.transfer(ctx, src, dst); err != nil {
		return &rclone.Result{ExitCode: 1, Errors: []string{err.Error()}}, nil
	}
	return &rclone.Result{}, nil
}

func (h *harness) installEngine(block chan struct{}) {
	h.transfer.SetEngineFactory(func(remotes []rclone.Remote) (transfercopy.Engine, error) {
		byName := map[string]rclone.Remote{}
		for _, r := range remotes {
			byName[r.Name] = r
		}
		return &httpEngine{remotes: byName, block: block}, nil
	})
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
