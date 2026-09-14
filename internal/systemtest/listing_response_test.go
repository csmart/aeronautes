package systemtest

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
)

func listingResponseProxy(t *testing.T, h *Harness, container, fault string) *atomic.Int32 {
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
	selectedPath := storage.EscapedPath()
	if container != "" {
		selectedPath += "/" + url.PathEscape(container)
	}
	var hits atomic.Int32
	proxy.ModifyResponse = func(response *http.Response) error {
		request := response.Request
		if request.Method != http.MethodGet || request.URL.EscapedPath() != selectedPath || request.URL.Query().Get("format") != "json" {
			return nil
		}
		hits.Add(1)
		const maxFixturePage = 1 << 20
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxFixturePage+1))
		closeErr := response.Body.Close()
		if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || len(body) > maxFixturePage {
			t.Errorf("listing fault did not reach a complete bounded successful fixture response: status=%d read=%v close=%v size=%d", response.StatusCode, readErr, closeErr, len(body))
			return fmt.Errorf("invalid listing fixture response")
		}
		switch fault {
		case "second-json":
			body = append(body, []byte(" []")...)
		case "invalid-bytes":
			before := body
			body = bytes.Replace(body, []byte(`"bytes":`), []byte(`"bytes":"invalid","ignored_bytes":`), 1)
			if bytes.Equal(body, before) {
				t.Error("fixture response had no numeric bytes field to corrupt")
				return fmt.Errorf("listing field fault was not applied")
			}
		case "partial-status":
			response.StatusCode = http.StatusPartialContent
		case "content-range":
			response.Header.Set("Content-Range", "")
		}
		response.ContentLength = int64(len(body))
		if fault == "truncated-body" {
			response.ContentLength += 20
		}
		response.Header.Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
		response.Body = io.NopCloser(bytes.NewReader(body))
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		t.Errorf("listing fixture proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	front := httptest.NewServer(proxy)
	t.Cleanup(front.Close)
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var primary app.BookmarkDTO
	for _, bookmark := range bookmarks {
		if bookmark.ID == h.Primary.BookmarkID {
			primary = bookmark
		}
	}
	if primary.ID == "" {
		t.Fatal("primary fixture bookmark not found")
	}
	primary.StorageURLOverride = front.URL + storage.EscapedPath()
	if _, err := h.Bookmarks.Save(t.Context(), primary, h.Primary.key); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Browse.Connect(t.Context(), primary.ID); err != nil {
		t.Fatal(err)
	}
	return &hits
}

func listingResponseRefusal(t *testing.T, h *Harness, kind, fault string) *Receipt {
	t.Helper()
	defer h.CloseFind()
	primary := h.Container(h.Primary, "listing-response")
	secondary := h.Container(h.Secondary, "listing-response-sentinel")
	h.Seed(h.Primary, primary, "match-a", []byte("primary"), nil)
	h.Seed(h.Secondary, secondary, "match-a", []byte("secondary"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	proxyContainer := primary
	if kind == "containers" {
		proxyContainer = ""
	}
	hits := listingResponseProxy(t, h, proxyContainer, fault)
	var receipt *Receipt
	switch kind {
	case "containers":
		page, call := h.BrowseContainers(t.Context(), h.Primary.BookmarkID, "", 3)
		receipt = call
		if receipt.Err() == nil || len(page.Rows) != 0 || page.NextMarker != "" {
			t.Fatalf("malformed container page was accepted: rows=%d marker=%q error=%v", len(page.Rows), page.NextMarker, receipt.Err())
		}
	case "objects":
		page, call := h.BrowseObjects(t.Context(), h.Primary.BookmarkID, primary, "", "", 3)
		receipt = call
		if receipt.Err() == nil || len(page.Rows) != 0 || page.NextMarker != "" {
			t.Fatalf("malformed object page was accepted: rows=%d marker=%q error=%v", len(page.Rows), page.NextMarker, receipt.Err())
		}
	case "find":
		id, start := h.StartFind(t.Context(), primary, "", "match")
		if start.Err() != nil || id == "" {
			t.Fatalf("Find was not admitted before its listing response: %q %v", id, start.Err())
		}
		status, _ := awaitFindStatus(t, h, id)
		if status.Error == "" || status.Scanned != 0 || len(status.Matches) != 0 {
			t.Fatalf("malformed listing was accepted by Find: %+v", status)
		}
	default:
		t.Fatal("unknown listing action")
	}
	if hits.Load() != 1 {
		t.Fatalf("selected response observed %d times, want one without a retry", hits.Load())
	}
	h.CloseFind()
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	return receipt
}

func listingResponseRefusals(t *testing.T, newHarness func(*testing.T) *Harness, kind string) {
	t.Helper()
	var receipt *Receipt
	for _, fault := range []string{"truncated-body", "second-json", "partial-status", "content-range", "invalid-bytes"} {
		t.Logf("listing action=%s fault=%s", kind, fault)
		receipt = listingResponseRefusal(t, newHarness(t), kind, fault)
	}
	if receipt != nil {
		receipt.CreditRefusal()
	}
}

func TestContainerResponseRefusalsMock(t *testing.T) {
	listingResponseRefusals(t, findMockHarness, "containers")
}

func TestObjectResponseRefusalsMock(t *testing.T) {
	listingResponseRefusals(t, findMockHarness, "objects")
}

func TestFindResponseRefusalsMock(t *testing.T) {
	listingResponseRefusals(t, findMockHarness, "find")
}

func TestContainerResponseRefusalsRealSwift(t *testing.T) {
	listingResponseRefusals(t, New, "containers")
}

func TestObjectResponseRefusalsRealSwift(t *testing.T) {
	listingResponseRefusals(t, New, "objects")
}

func TestFindResponseRefusalsRealSwift(t *testing.T) {
	listingResponseRefusals(t, New, "find")
}

func listingReadOnly(t *testing.T, h *Harness) {
	t.Helper()
	defer h.CloseFind()
	primary := h.Container(h.Primary, "listing-read")
	secondary := h.Container(h.Secondary, "listing-read-sentinel")
	const name = " scope/a%2F +日本語"
	h.Seed(h.Primary, primary, name, []byte("data"), nil)
	h.Seed(h.Primary, primary, " scope/nested/child", []byte("ab"), nil)
	h.Seed(h.Primary, primary, "outside", []byte("unchanged"), nil)
	h.Seed(h.Secondary, secondary, name, []byte("secondary"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	containers, containerRead := h.BrowseContainers(t.Context(), h.Primary.BookmarkID, "", 10000)
	if containerRead.Err() != nil {
		t.Fatal(containerRead.Err())
	}
	found := false
	for _, row := range containers.Rows {
		if row.Name == secondary {
			t.Fatal("Browse crossed the account boundary")
		}
		if row.Name == primary {
			found = true
		}
	}
	if !found || containers.NextMarker != "" {
		t.Fatalf("owned container missing or listing unfinished: %+v", containers)
	}
	page, objectRead := h.BrowseObjects(t.Context(), h.Primary.BookmarkID, primary, " scope/", "", 3)
	if objectRead.Err() != nil || len(page.Rows) != 2 || page.NextMarker != "" {
		t.Fatalf("object listing = %+v, error=%v", page, objectRead.Err())
	}
	object, prefix := page.Rows[0], page.Rows[1]
	if object.Kind != "object" || object.Name != name || object.Bytes != 4 || object.ETag == "" || object.LastModified == "" || object.ContentType == "" {
		t.Fatalf("literal object fields = %+v", object)
	}
	if prefix.Kind != "prefix" || prefix.Name != " scope/nested/" {
		t.Fatalf("delimiter row = %+v", prefix)
	}
	empty, emptyRead := h.BrowseObjects(t.Context(), h.Primary.BookmarkID, primary, "absent/", "", 3)
	if emptyRead.Err() != nil || len(empty.Rows) != 0 || empty.NextMarker != "" {
		t.Fatalf("empty listing = %+v, error=%v", empty, emptyRead.Err())
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	containerRead.CreditRead()
	objectRead.CreditRead()
}

func TestListingReadOnlyMock(t *testing.T)      { listingReadOnly(t, findMockHarness(t)) }
func TestListingReadOnlyRealSwift(t *testing.T) { listingReadOnly(t, New(t)) }
