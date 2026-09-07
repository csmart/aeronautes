package systemtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
)

type listingPageRequest struct {
	path, prefix, marker, delimiter, limit string
}

type listingPageTrace struct {
	mu       sync.Mutex
	requests []listingPageRequest
}

func (trace *listingPageTrace) snapshot() []listingPageRequest {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]listingPageRequest(nil), trace.requests...)
}

func (trace *listingPageTrace) assertMarkers(t *testing.T, prefix, delimiter string, want ...string) {
	t.Helper()
	var got []string
	for _, request := range trace.snapshot() {
		if request.prefix == prefix && request.delimiter == delimiter {
			got = append(got, request.marker)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listing markers for prefix %q delimiter %q: got %q, want %q", prefix, delimiter, got, want)
	}
}

func limitListingPages(t *testing.T, h *Harness, account *Account, container, mode, initialMarker string) *listingPageTrace {
	t.Helper()
	storage, err := url.Parse(account.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	selectedPath := storage.EscapedPath()
	if container != "" {
		selectedPath += "/" + url.PathEscape(container)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 10 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	trace := &listingPageTrace{}
	direct := proxy.Director
	proxy.Director = func(request *http.Request) {
		direct(request)
		query := request.URL.Query()
		if request.Method != http.MethodGet || request.URL.EscapedPath() != selectedPath || query.Get("format") != "json" {
			return
		}
		trace.mu.Lock()
		trace.requests = append(trace.requests, listingPageRequest{selectedPath, query.Get("prefix"), query.Get("marker"), query.Get("delimiter"), query.Get("limit")})
		trace.mu.Unlock()
		if mode != "normal" {
			query.Set("limit", "1")
		}
		request.URL.RawQuery = query.Encode()
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		request := response.Request
		query := request.URL.Query()
		if mode == "short" || mode == "normal" || request.Method != http.MethodGet || request.URL.EscapedPath() != selectedPath || query.Get("format") != "json" || query.Get("marker") == initialMarker {
			return nil
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		closeErr := response.Body.Close()
		if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || len(body) > 1<<20 {
			t.Errorf("invalid controlled listing fixture: status=%d read=%v close=%v size=%d", response.StatusCode, readErr, closeErr, len(body))
			return fmt.Errorf("invalid controlled listing fixture")
		}
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(body, &rows); err != nil || len(rows) != 1 {
			t.Errorf("controlled second page must contain one real row: rows=%d error=%v", len(rows), err)
			return fmt.Errorf("controlled second page was not reached")
		}
		switch mode {
		case "late-error":
			body = []byte("[")
		case "repeat", "outside-prefix":
			name := query.Get("marker")
			if mode == "outside-prefix" {
				name = "z-outside"
			}
			encoded, err := json.Marshal(name)
			if err != nil {
				return err
			}
			rows[0]["name"] = encoded
			delete(rows[0], "subdir")
			body, err = json.Marshal(rows)
			if err != nil {
				return err
			}
		default:
			t.Errorf("unknown controlled listing mode %q", mode)
			return fmt.Errorf("unknown controlled listing mode")
		}
		response.ContentLength = int64(len(body))
		response.Header.Set("Content-Length", strconv.Itoa(len(body)))
		response.Body = io.NopCloser(bytes.NewReader(body))
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		t.Errorf("listing fixture proxy: %v", err)
		http.Error(w, "listing fixture proxy failed", http.StatusBadGateway)
	}
	front := httptest.NewServer(proxy)
	t.Cleanup(front.Close)
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, bookmark := range bookmarks {
		if bookmark.ID != account.BookmarkID {
			continue
		}
		found = true
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, account.key); err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("owned fixture bookmark not found")
	}
	if account == h.Primary {
		if _, err := h.Browse.Connect(t.Context(), account.BookmarkID); err != nil {
			t.Fatal(err)
		}
	}
	return trace
}

func shortListingReads(t *testing.T, h *Harness) {
	t.Helper()
	defer h.CloseFind()
	primary := h.Container(h.Primary, "paged-read")
	containers := []string{primary, h.Container(h.Primary, "paged-read-extra-a"), h.Container(h.Primary, "paged-read-extra-b")}
	sort.Strings(containers)
	secondary := h.Container(h.Secondary, "paged-read-sentinel")
	names := []string{"scope/match-a", "scope/match-b%2F +日本語", "scope/nested/match-c"}
	for index, name := range names {
		h.Seed(h.Primary, primary, name, bytes.Repeat([]byte("x"), index+3), nil)
	}
	h.Seed(h.Primary, primary, "outside", []byte("untouched"), nil)
	h.Seed(h.Secondary, secondary, names[0], []byte("secondary"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	containerTrace := limitListingPages(t, h, h.Primary, "", "short", h.ns)
	page, containerRead := h.BrowseContainers(t.Context(), h.ns, 3)
	if containerRead.Err() != nil || len(page.Rows) != 3 || page.NextMarker != containers[2] {
		t.Fatalf("short container pages hid a tail: %+v, error=%v", page, containerRead.Err())
	}
	for index, name := range containers {
		if page.Rows[index].Name != name {
			t.Fatalf("container %d = %q, want %q", index, page.Rows[index].Name, name)
		}
	}
	containerTrace.assertMarkers(t, "", "", h.ns, containers[0], containers[1])

	objectTrace := limitListingPages(t, h, h.Primary, primary, "short", "")
	objects, objectRead := h.BrowseObjects(t.Context(), primary, "scope/", "", 3)
	if objectRead.Err() != nil || len(objects.Rows) != 3 || objects.NextMarker != "scope/nested/" {
		t.Fatalf("short delimiter pages hid a tail: %+v, error=%v", objects, objectRead.Err())
	}
	for index, name := range []string{names[0], names[1], "scope/nested/"} {
		wantKind := "object"
		if index == 2 {
			wantKind = "prefix"
		}
		if objects.Rows[index].Name != name || objects.Rows[index].Kind != wantKind {
			t.Fatalf("delimiter row %d = %+v", index, objects.Rows[index])
		}
	}
	empty, endRead := h.BrowseObjects(t.Context(), primary, "scope/", objects.NextMarker, 3)
	if endRead.Err() != nil || len(empty.Rows) != 0 || empty.NextMarker != "" {
		t.Fatalf("terminal listing = %+v, error=%v", empty, endRead.Err())
	}
	objectTrace.assertMarkers(t, "scope/", "/", "", names[0], names[1], "scope/nested/")

	id, start := h.StartFind(t.Context(), primary, "scope/", "match")
	if start.Err() != nil || id == "" {
		t.Fatalf("Find start = %q, %v", id, start.Err())
	}
	status, poll := awaitFindStatus(t, h, id)
	if status.Error != "" || status.Scanned != 3 || status.Truncated || len(status.Matches) != 3 {
		t.Fatalf("short pages hid Find matches: %+v", status)
	}
	for index, name := range names {
		if status.Matches[index].Name != name || status.Matches[index].Bytes != int64(index+3) {
			t.Fatalf("Find match %d = %+v", index, status.Matches[index])
		}
	}
	objectTrace.assertMarkers(t, "scope/", "", "", names[0], names[1], names[2])
	h.CloseFind()
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	containerRead.CreditRead()
	objectRead.CreditRead()
	start.CreditFindComplete(poll)
	poll.CreditRead()
}

func TestShortListingReadsMock(t *testing.T)      { shortListingReads(t, findMockHarness(t)) }
func TestShortListingReadsRealSwift(t *testing.T) { shortListingReads(t, New(t)) }

func listingCopy(t *testing.T, h *Harness, mode string) {
	t.Helper()
	defer h.CloseFind()
	primary := h.Container(h.Primary, "paged-copy-src")
	secondary := h.Container(h.Secondary, "paged-copy-dst")
	names := []string{"scope/a", "scope/b%2F +日本語", "scope/c\nnative"}
	wantNative := int64(1)
	switch mode {
	case "preview", "mixed-short", "mixed-normal":
	case "native":
		names[0] += "\nnative"
		names[1] += "\nnative"
		wantNative = 3
	default:
		t.Fatalf("unknown Copy listing case %q", mode)
	}
	for index, name := range names {
		h.Seed(h.Primary, primary, name, bytes.Repeat([]byte("x"), index+3), nil)
	}
	h.Seed(h.Primary, primary, "outside", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, secondary, "outside", []byte("destination sentinel"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	listingMode := "short"
	if mode == "mixed-normal" {
		listingMode = "normal"
	}
	sourceTrace := limitListingPages(t, h, h.Primary, primary, listingMode, "")
	destTrace := limitListingPages(t, h, h.Secondary, secondary, listingMode, "")
	request := app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: primary, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: secondary, DestPrefix: "scope/",
	}
	if mode == "preview" {
		plan, read := h.PlanCopy(t.Context(), request)
		if read.Err() != nil || plan == nil {
			t.Fatalf("copy preview: %v, %v", plan, read.Err())
		}
		if plan.Summary.New != 3 || plan.Summary.TransferObjects != 3 || plan.Summary.TransferBytes != 12 || plan.Summary.NativeRouted != 1 || plan.Summary.Changed != 0 || plan.Summary.Unknown != 0 || plan.Summary.DestOnly != 0 {
			t.Fatalf("short pages truncated the preview: %+v", plan.Summary)
		}
		sourceTrace.assertMarkers(t, "scope/", "", "", names[0], names[1], names[2])
		destTrace.assertMarkers(t, "scope/", "", "")
		h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
		h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
		read.CreditRead()
		return
	}
	receipt := h.StartCopy(t.Context(), request)
	if receipt.Err() != nil {
		t.Fatal(receipt.Err())
	}
	job := h.AwaitJob(receipt.JobID())
	if job.Counters.ObjectsPlanned != 3 || job.Counters.BytesPlanned != 12 || job.Counters.NativeRouted != wantNative || job.Counters.Overwrites != 0 || job.Counters.OverwritesRefused != 0 {
		t.Fatalf("listing pages changed the selected Copy plan: %+v", job)
	}
	if job.State != "complete" || job.Counters.ObjectsCopied != 3 || job.Counters.BytesCopied != 12 || job.Counters.ObjectsVerified != 3 || job.Counters.ObjectsFailed != 0 {
		t.Fatalf("listing pages truncated Copy or fresh verification: %+v", job)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeA, afterA)
	allowed := make([]string, 0, len(names))
	for _, name := range names {
		want := beforeA.Containers[primary].Objects[name]
		got, found := afterB.Containers[secondary].Objects[name]
		allowed = append(allowed, secondary+"/"+name)
		if !found || got.Size != want.Size || got.Digest != want.Digest {
			t.Fatalf("copied name %q is missing or differs: got=%+v want=%+v", name, got, want)
		}
	}
	h.AssertNoCollateral(beforeB, afterB, allowed...)
	// Execution and verification must both observe the source tail;
	// successful writes alone do not prove that verification reached it.
	terminalReads := 0
	for _, call := range sourceTrace.snapshot() {
		if call.prefix == "scope/" && call.delimiter == "" && call.marker == names[2] {
			terminalReads++
		}
	}
	if mode != "mixed-normal" && terminalReads < 2 {
		t.Fatalf("source tail reached only %d times across planning and fresh verification", terminalReads)
	}
	receipt.CreditHappy()
}

func TestShortListingPreviewMock(t *testing.T)         { listingCopy(t, findMockHarness(t), "preview") }
func TestShortListingPreviewRealSwift(t *testing.T)    { listingCopy(t, New(t), "preview") }
func TestShortListingNativeCopyMock(t *testing.T)      { listingCopy(t, findMockHarness(t), "native") }
func TestShortListingNativeCopyRealSwift(t *testing.T) { listingCopy(t, New(t), "native") }
func TestShortListingMixedCopyMock(t *testing.T)       { listingCopy(t, findMockHarness(t), "mixed-short") }
func TestShortListingMixedCopyRealSwift(t *testing.T)  { listingCopy(t, New(t), "mixed-short") }
func TestNormalListingMixedCopyMock(t *testing.T)      { listingCopy(t, findMockHarness(t), "mixed-normal") }
func TestNormalListingMixedCopyRealSwift(t *testing.T) { listingCopy(t, New(t), "mixed-normal") }

func laterListingRefusal(t *testing.T, h *Harness, kind, mode string) *Receipt {
	t.Helper()
	defer h.CloseFind()
	primary := h.Container(h.Primary, "later-listing")
	h.Container(h.Primary, "later-listing-extra")
	secondary := h.Container(h.Secondary, "later-listing-sentinel")
	for _, name := range []string{"scope/match-a", "scope/match-b"} {
		h.Seed(h.Primary, primary, name, []byte("source"), nil)
	}
	h.Seed(h.Secondary, secondary, "outside", []byte("secondary"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	container, initial := primary, ""
	if kind == "containers" {
		container, initial = "", h.ns
	}
	trace := limitListingPages(t, h, h.Primary, container, mode, initial)
	var receipt *Receipt
	switch kind {
	case "containers":
		page, call := h.BrowseContainers(t.Context(), initial, 3)
		receipt = call
		if call.Err() == nil || len(page.Rows) != 0 || page.NextMarker != "" {
			t.Fatalf("invalid later container page was accepted: %+v, error=%v", page, call.Err())
		}
	case "objects":
		page, call := h.BrowseObjects(t.Context(), primary, "scope/", "", 3)
		receipt = call
		if call.Err() == nil || len(page.Rows) != 0 || page.NextMarker != "" {
			t.Fatalf("invalid later object page was accepted: %+v, error=%v", page, call.Err())
		}
	case "find":
		id, start := h.StartFind(t.Context(), primary, "scope/", "match")
		if start.Err() != nil || id == "" {
			t.Fatalf("Find was not admitted: %q %v", id, start.Err())
		}
		status, _ := awaitFindStatus(t, h, id)
		if status.Error == "" || status.Scanned != 1 || len(status.Matches) != 1 || status.Matches[0].Name != "scope/match-a" {
			t.Fatalf("later page error was lost or valid earlier observations changed: %+v", status)
		}
	case "plan":
		plan, call := h.PlanCopy(t.Context(), app.CopyRequestDTO{
			SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: primary, SourcePrefix: "scope/",
			DestBookmarkID: h.Secondary.BookmarkID, DestContainer: secondary, DestPrefix: "scope/",
		})
		if call.Err() == nil || plan != nil {
			t.Fatalf("invalid later page produced a preview: %+v, error=%v", plan, call.Err())
		}
	default:
		t.Fatal("unknown listing consumer")
	}
	calls := trace.snapshot()
	if len(calls) != 2 || calls[0].marker != initial || calls[1].marker == initial {
		t.Fatalf("later refusal did not use exactly two advancing requests: %+v", calls)
	}
	h.CloseFind()
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	return receipt
}

func laterListingRefusals(t *testing.T, newHarness func(*testing.T) *Harness, kind string) {
	t.Helper()
	modes := []string{"repeat", "late-error"}
	if kind != "containers" {
		modes = append(modes, "outside-prefix")
	}
	var receipt *Receipt
	for _, mode := range modes {
		t.Logf("later listing action=%s mode=%s", kind, mode)
		receipt = laterListingRefusal(t, newHarness(t), kind, mode)
	}
	if receipt != nil {
		receipt.CreditRefusal()
	}
}

func TestLaterContainerRefusalsMock(t *testing.T) {
	laterListingRefusals(t, findMockHarness, "containers")
}
func TestLaterObjectRefusalsMock(t *testing.T)         { laterListingRefusals(t, findMockHarness, "objects") }
func TestLaterFindRefusalsMock(t *testing.T)           { laterListingRefusals(t, findMockHarness, "find") }
func TestLaterPlanRefusalsMock(t *testing.T)           { laterListingRefusals(t, findMockHarness, "plan") }
func TestLaterContainerRefusalsRealSwift(t *testing.T) { laterListingRefusals(t, New, "containers") }
func TestLaterObjectRefusalsRealSwift(t *testing.T)    { laterListingRefusals(t, New, "objects") }
func TestLaterFindRefusalsRealSwift(t *testing.T)      { laterListingRefusals(t, New, "find") }
func TestLaterPlanRefusalsRealSwift(t *testing.T)      { laterListingRefusals(t, New, "plan") }
