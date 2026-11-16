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

// holdFirstWrite routes the primary account through a proxy that stops the
// first write to one name before the store sees it, so that another job can
// write the name while the first is in its request. It returns a channel
// closed when that write has arrived, and the release.
func holdFirstWrite(t *testing.T, h *Harness, container, name string) (<-chan struct{}, func()) {
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

// ADR 0122 decision 1: where two writers act on one name the last one wins,
// and that is not an error of the application. Two uploads carry different
// bytes to one free name. The first is stopped in its write; the second
// writes the name; the first then writes over it. Both jobs report success
// and the name holds the bytes that reached the store last.
func twoUploadsToOneName(t *testing.T, h *Harness) {
	t.Helper()
	container := h.Container(h.Primary, "two-uploads")
	const name = "shared/report.txt"
	const heldBytes, otherBytes = "the bytes the held job sent", "the bytes the other job sent"
	reached, release := holdFirstWrite(t, h, container, name)
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
	written := func(id string) {
		t.Helper()
		if job := h.AwaitJob(id); job.State != "complete" || job.Counters.ObjectsCopied != 1 || job.Counters.ObjectsFailed != 0 {
			t.Fatalf("an upload to a name another job also writes = %s %+v; want complete with one file written", job.State, job.Counters)
		}
		page, err := h.Jobs.Items(t.Context(), h.Primary.BookmarkID, id, "", 100)
		if err != nil || len(page.Items) != 1 || page.Items[0].Execution == nil || page.Items[0].Execution.Result != "copied" {
			t.Fatalf("the upload's record is not one written file: %+v %v", page, err)
		}
	}

	held := start(heldBytes)
	select {
	case <-reached:
	case <-time.After(60 * time.Second):
		t.Fatal("the first upload never reached its write")
	}
	written(start(otherBytes))
	if got := liveETag(t, h, container, name); got != md5of(otherBytes) {
		t.Fatalf("the name holds %s before the held write is released, want the other job's bytes %s", got, md5of(otherBytes))
	}
	release()

	written(held)
	if got := liveETag(t, h, container, name); got != md5of(heldBytes) {
		t.Fatalf("the name holds %s, want the bytes written last %s (the other job's are %s)", got, md5of(heldBytes), md5of(otherBytes))
	}
}
