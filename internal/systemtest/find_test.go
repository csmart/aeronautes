package systemtest

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func findMockHarness(t *testing.T) *Harness {
	t.Helper()
	primary := swifttest.New("test:tester", "testing")
	secondary := swifttest.New("test2:tester2", "testing2")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/v1.0" {
			mock, account := primary, "/v1/AUTH_primary"
			if r.Header.Get("X-Auth-User") == "test2:tester2" {
				mock, account = secondary, "/v1/AUTH_secondary"
			}
			response := httptest.NewRecorder()
			mock.ServeHTTP(response, r)
			for name, values := range response.Header() {
				w.Header()[name] = append([]string(nil), values...)
			}
			if response.Header().Get("X-Storage-Url") != "" {
				w.Header().Set("X-Storage-Url", server.URL+account)
			}
			w.WriteHeader(response.Code)
			_, _ = w.Write(response.Body.Bytes())
			return
		}
		if r.URL.Path == "/info" {
			primary.ServeHTTP(w, r)
			return
		}
		for prefix, mock := range map[string]*swifttest.Server{
			"/v1/AUTH_primary": primary, "/v1/AUTH_secondary": secondary,
		} {
			if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
				request := r.Clone(r.Context())
				request.URL.Path = "/v1/AUTH_test" + strings.TrimPrefix(r.URL.Path, prefix)
				if request.URL.RawPath != "" {
					request.URL.RawPath = strings.Replace(request.URL.RawPath, prefix, "/v1/AUTH_test", 1)
				}
				mock.ServeHTTP(w, request)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("AERONAUTES_SAIO_AUTH_URL", server.URL+"/auth/v1.0")
	t.Setenv("AERONAUTES_SAIO_USER", "test:tester")
	t.Setenv("AERONAUTES_SAIO_KEY", "testing")
	t.Setenv("AERONAUTES_SAIO_USER2", "test2:tester2")
	t.Setenv("AERONAUTES_SAIO_KEY2", "testing2")
	t.Setenv("AERONAUTES_REQUIRE_TWO_ACCOUNTS", "1")
	return New(t)
}

func awaitFindStatus(t *testing.T, h *Harness, id string) (*app.FindStatusDTO, *Receipt) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		status, receipt := h.FindStatus(t.Context(), id)
		if receipt.Err() != nil {
			t.Fatal(receipt.Err())
		}
		if status.Done {
			return status, receipt
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("Find did not reach terminal status")
		}
	}
}

func findReadOnly(t *testing.T, h *Harness) {
	t.Helper()
	defer h.CloseFind()
	primary := h.Container(h.Primary, "find-primary")
	secondary := h.Container(h.Secondary, "find-secondary")
	h.Seed(h.Primary, primary, " scope/MATCH.txt", []byte("data"), nil)
	h.Seed(h.Primary, primary, " scope/nested/match 2", []byte("ab"), nil)
	h.Seed(h.Primary, primary, " scope/unrelated", []byte("untouched"), nil)
	h.Seed(h.Primary, primary, "outside-MATCH", []byte("outside"), nil)
	h.Seed(h.Secondary, secondary, " scope/MATCH.txt", []byte("secondary sentinel"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	id, start := h.StartFind(t.Context(), primary, " scope/", "mAtCh")
	if start.Err() != nil || id == "" {
		t.Fatalf("Find start = %q %v", id, start.Err())
	}
	status, poll := awaitFindStatus(t, h, id)
	if status.Error != "" || status.Scanned != 3 || status.Truncated || len(status.Matches) != 2 {
		t.Fatalf("Find status = %+v", status)
	}
	want := []app.FindMatchDTO{{Name: " scope/MATCH.txt", Bytes: 4}, {Name: " scope/nested/match 2", Bytes: 2}}
	for i := range want {
		if status.Matches[i] != want[i] {
			t.Fatalf("match %d = %+v, want %+v", i, status.Matches[i], want[i])
		}
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	status.Done, status.Error = false, "caller changed snapshot"
	start.CreditFindComplete(poll)
	poll.CreditRead()
}

func TestFindReadOnlyMock(t *testing.T)      { findReadOnly(t, findMockHarness(t)) }
func TestFindReadOnlyRealSwift(t *testing.T) { findReadOnly(t, New(t)) }

func findRefusals(t *testing.T, h *Harness) {
	t.Helper()
	defer h.CloseFind()
	primary := h.Container(h.Primary, "find-refuse")
	secondary := h.Container(h.Secondary, "find-refuse-sentinel")
	h.Seed(h.Primary, primary, "keep", []byte("primary"), nil)
	h.Seed(h.Secondary, secondary, "keep", []byte("secondary"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	id, start := h.StartFind(t.Context(), primary, "", " \t ")
	if id != "" || start.Err() == nil {
		t.Fatalf("blank Find was admitted: %q %v", id, start.Err())
	}
	status, lookup := h.FindStatus(t.Context(), "absent-scan-id")
	if status != nil || lookup.Err() == nil {
		t.Fatalf("absent scan answered: %+v %v", status, lookup.Err())
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	start.CreditRefusal()
	lookup.CreditRefusal()
}

func TestFindRefusalsMock(t *testing.T)      { findRefusals(t, findMockHarness(t)) }
func TestFindRefusalsRealSwift(t *testing.T) { findRefusals(t, New(t)) }

func findCancellation(t *testing.T, h *Harness) {
	t.Helper()
	defer h.CloseFind()
	primary := h.Container(h.Primary, "find-cancel")
	secondary := h.Container(h.Secondary, "find-cancel-sentinel")
	h.Seed(h.Primary, primary, "scope/match", []byte("primary"), nil)
	h.Seed(h.Secondary, secondary, "scope/match", []byte("secondary"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	selector := swifttest.HTTPResponseSelector{
		Operation: t.Name(), Method: http.MethodGet,
		EscapedPath: storage.EscapedPath() + "/" + url.PathEscape(primary),
		RawQuery:    url.Values{"format": {"json"}, "limit": {"10000"}, "prefix": {"scope/"}}.Encode(),
		Occurrence:  1,
	}
	barrier, err := swifttest.NewHTTPResponseBarrier(selector, false, 30*time.Second)
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
	held := barrier.Wrap(proxy)
	ended := make(chan struct{})
	var once sync.Once
	var selected atomic.Int32
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == selector.Method && r.URL.EscapedPath() == selector.EscapedPath && r.URL.RawQuery == selector.RawQuery {
			selected.Add(1)
			defer once.Do(func() { close(ended) })
		}
		held.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	t.Cleanup(barrier.Close)
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var active app.BookmarkDTO
	for _, bookmark := range bookmarks {
		if bookmark.ID == h.Primary.BookmarkID {
			active = bookmark
		}
	}
	if active.ID == "" {
		t.Fatal("primary bookmark not found")
	}
	active.StorageURLOverride = front.URL + storage.EscapedPath()
	if _, err := h.Bookmarks.Save(t.Context(), active, h.Primary.key); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Browse.Connect(t.Context(), active.ID); err != nil {
		t.Fatal(err)
	}
	id, start := h.StartFind(t.Context(), primary, "scope/", "match")
	if start.Err() != nil || id == "" {
		t.Fatalf("Find start = %q %v", id, start.Err())
	}
	event, err := barrier.Wait(t.Context())
	if err != nil || event.StatusCode != http.StatusOK || selected.Load() != 1 {
		t.Fatalf("Find response did not reach the exact barrier: %+v %v count=%d", event, err, selected.Load())
	}
	pending, poll := h.FindStatus(t.Context(), id)
	if poll.Err() != nil || pending.Done || pending.Scanned != 0 {
		t.Fatalf("held response was prematurely consumed: %+v %v", pending, poll.Err())
	}
	cancel := h.CancelFind(t.Context(), id)
	if cancel.Err() != nil {
		t.Fatal(cancel.Err())
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled Find request did not terminate")
	}
	if err := barrier.Err(); err == nil || !strings.Contains(err.Error(), "context canceled") || selected.Load() != 1 {
		t.Fatalf("expected cancellation of the held response, not timeout/loss/retry: %v count=%d", err, selected.Load())
	}
	h.CloseFind()
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	start.CreditFindCancelled(cancel, ended)
	cancel.CreditFindCancellation()
}

func TestFindCancellationMock(t *testing.T)      { findCancellation(t, findMockHarness(t)) }
func TestFindCancellationRealSwift(t *testing.T) { findCancellation(t, New(t)) }
