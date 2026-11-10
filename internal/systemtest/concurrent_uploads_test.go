package systemtest

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// holdFirstPublication routes the primary account through a proxy that stops
// the first write to one live name before the store sees it, so that another
// job can publish the name while the first is at its commit. It returns a
// channel closed when that write has arrived, and the release.
func holdFirstPublication(t *testing.T, h *Harness, container, name string) (<-chan struct{}, func()) {
	t.Helper()
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	live := (&url.URL{Path: storage.Path + "/" + container + "/" + name}).EscapedPath()
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	proxy := httputil.NewSingleHostReverseProxy(&target)
	reached, gate := make(chan struct{}), make(chan struct{})
	// A flag and not a sync.Once: a Once would make the second write wait
	// for the first one's hold to end.
	var held atomic.Bool
	var released sync.Once
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.EscapedPath() == live && held.CompareAndSwap(false, true) {
			close(reached)
			select {
			case <-gate:
			case <-r.Context().Done():
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	release := func() { released.Do(func() { close(gate) }) }
	// Registered after the server's close, so it runs first and a held
	// request never keeps the server from closing.
	t.Cleanup(release)

	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range bookmarks {
		if bookmark.ID != h.Primary.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, h.Primary.key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	return reached, release
}

func TestTwoUploadsToOneNameMock(t *testing.T)      { twoUploadsToOneName(t, findMockHarness(t)) }
func TestTwoUploadsToOneNameRealSwift(t *testing.T) { twoUploadsToOneName(t, New(t)) }

// ADR 0105 decision 3 leaves two transfers to one live name to the contract
// for that name, and this is that contract with both jobs running. Two uploads
// carry different bytes to one free name. The first is stopped at its commit;
// the second publishes; the first then finds the name taken, records a
// conflict and leaves the publisher's bytes where they are.
func twoUploadsToOneName(t *testing.T, h *Harness) {
	t.Helper()
	container := h.Container(h.Primary, "two-uploads")
	const name = "shared/report.txt"
	const heldBytes, publishedBytes = "the bytes the held job sent", "the bytes the publisher sent"
	reached, release := holdFirstPublication(t, h, container, name)
	start := func(content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "report.txt")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		id, err := overwriteStart(t, h, container, "shared/", []string{path}, false)
		if err != nil {
			t.Fatalf("an upload to a free name was refused: %v", err)
		}
		return id
	}

	held := start(heldBytes)
	select {
	case <-reached:
	case <-time.After(60 * time.Second):
		t.Fatal("the first upload never reached its commit")
	}
	publisher := start(publishedBytes)
	if job := h.AwaitJob(publisher); job.State != "complete" || job.Counters.ObjectsCopied != 1 {
		t.Fatalf("the upload that ran beside the held one = %s %+v", job.State, job.Counters)
	}
	release()

	if job := h.AwaitJob(held); job.State != "partial" || job.Counters.ObjectsCopied != 0 {
		t.Fatalf("the held upload = %s %+v; want a partial job that published nothing", job.State, job.Counters)
	}
	page, err := h.Jobs.Items(t.Context(), h.Primary.BookmarkID, held, "", 100)
	if err != nil || len(page.Items) != 1 || page.Items[0].Execution == nil || page.Items[0].Execution.Result != "conflict" {
		t.Fatalf("the held upload's record is not one conflict: %+v %v", page, err)
	}
	if got := liveETag(t, h, container, name); got != md5of(publishedBytes) {
		t.Fatalf("the name holds %s, want the publisher's bytes %s (the held job's are %s)", got, md5of(publishedBytes), md5of(heldBytes))
	}
}
