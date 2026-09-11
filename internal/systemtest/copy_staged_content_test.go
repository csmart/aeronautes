package systemtest

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

type stagedContentRequest struct {
	Method, Path, Query, IfNoneMatch, Manifest, AcceptEncoding string
}

type stagedContentTrace struct {
	mu       sync.Mutex
	requests []stagedContentRequest
}

func (trace *stagedContentTrace) snapshot() []stagedContentRequest {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]stagedContentRequest(nil), trace.requests...)
}

func stagedContentProxy(t *testing.T, h *Harness, controlPath string, readHold *stagedContentReadHold) (*swifttest.HTTPResponseBarrier, *stagedContentTrace) {
	t.Helper()
	storage, err := url.Parse(h.Secondary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	selected := &url.URL{Path: storage.Path + "/" + controlPath}
	selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: http.MethodPut, EscapedPath: selected.EscapedPath(), Occurrence: 1}
	barrier, err := swifttest.NewHTTPResponseBarrier(selector, false, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 15 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("staged content proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	held := barrier.Wrap(proxy)
	trace := &stagedContentTrace{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.mu.Lock()
		trace.requests = append(trace.requests, stagedContentRequest{r.Method, r.URL.Path, r.URL.RawQuery,
			r.Header.Get("If-None-Match"), r.Header.Get("X-Object-Manifest"), r.Header.Get("Accept-Encoding")})
		trace.mu.Unlock()
		if readHold != nil && readHold.serve(w, r, transport, &target) {
			return
		}
		if r.Method == selector.Method && r.URL.EscapedPath() == selector.EscapedPath && r.URL.RawQuery == selector.RawQuery {
			// Only this small control PUT can use the bounded response capture;
			// segment uploads and full staged GETs must stream without its limit.
			if len(r.Header.Values("Expect")) != 0 {
				prepared, err := copyCompletionContinue(r)
				if err != nil {
					t.Errorf("staged content control handshake: %v", err)
					http.Error(w, "fixture control handshake failed", http.StatusBadRequest)
					return
				}
				r = prepared
			}
			held.ServeHTTP(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	t.Cleanup(barrier.Close)
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range bookmarks {
		if bookmark.ID != h.Secondary.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, h.Secondary.key); err != nil {
			t.Fatal(err)
		}
		return barrier, trace
	}
	t.Fatal("owned destination bookmark missing")
	return nil, nil
}

type stagedContentFixture struct {
	src, dst, segments      string
	control, subject, later []byte
	beforeA, beforeB        *oracle.AccountSnapshot
}

func newStagedContentFixture(t *testing.T, h *Harness) stagedContentFixture {
	t.Helper()
	f := stagedContentFixture{
		src: h.Container(h.Primary, "staged-content-src"), dst: h.Container(h.Secondary, "staged-content-dst"),
		control: []byte("first control bytes"),
		subject: append(bytes.Repeat([]byte("A"), 1<<20), bytes.Repeat([]byte("B"), 1<<20)...),
	}
	f.segments = f.dst + "_segments"
	h.rawPut(h.Secondary, "/"+f.segments, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, f.segments) })
	aliases := h.Container(h.Secondary, "staged-content-aliases")
	h.Seed(h.Primary, f.src, "scope/a-control", f.control, map[string]string{"X-Object-Meta-Team": "content-proof"})
	h.Seed(h.Primary, f.src, "scope/b-subject", f.subject, map[string]string{"X-Object-Meta-Team": "content-proof"})
	h.Seed(h.Primary, f.src, "unselected", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, f.dst, "unselected", []byte("destination sentinel"), nil)
	h.Seed(h.Secondary, f.dst, "scope/.aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	shared := []byte("shared segment sentinel")
	h.Seed(h.Secondary, f.segments, "shared/0", shared, nil)
	h.Seed(h.Secondary, aliases, "dlo", nil, map[string]string{"X-Object-Manifest": f.segments + "/shared/"})
	h.SeedSLO(h.Secondary, aliases, "slo", fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+f.segments+"/shared/0", md5.Sum(shared), len(shared)))
	f.beforeA, f.beforeB = h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	return f
}

func stagedContentDigest(body []byte) string { return fmt.Sprintf("%x", sha256.Sum256(body)) }

func stagedContentHeld(t *testing.T, h *Harness, f stagedContentFixture, id string, barrier *swifttest.HTTPResponseBarrier) (string, oracle.ObjectState, *oracle.AccountSnapshot) {
	t.Helper()
	event, err := barrier.Wait(t.Context())
	if err != nil || event.StatusCode != http.StatusCreated {
		t.Fatalf("first live PUT did not reach the after-effect barrier: %+v %v", event, err)
	}
	page, err := h.jobStore.ReadItems(t.Context(), id, 0, 100)
	names := []string{"a-control", "b-subject"}
	if f.later != nil {
		names = append(names, "c-later")
	}
	if err != nil || len(page.Items) != len(names) || page.NextCursor != 0 {
		t.Fatalf("acknowledged batch identities required: %+v %v", page, err)
	}
	gate := h.SnapshotNamespace(h.Secondary)
	for i, name := range names {
		item := page.Items[i]
		stageName := "scope/.aeronautes/stage/" + id + "/batch/" + name
		if item.Key != name || !item.Planned || !item.Admitted || item.Staging == nil ||
			*item.Staging != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Secondary.BookmarkID, Container: f.dst, Object: stageName}) ||
			item.Execution != nil || item.Verification != nil {
			t.Fatalf("barrier crossed the claimed item boundary: %+v", item)
		}
		body := f.control
		if i == 1 {
			body = f.subject
		}
		if i == 2 {
			body = f.later
		}
		if stage := gate.Containers[f.dst].Objects[stageName]; stage.Size != int64(len(body)) || stage.Digest != stagedContentDigest(body) {
			t.Fatalf("acknowledged staging identity lacks its expected bytes: %+v", stage)
		}
	}
	stageName := page.Items[1].Staging.Object
	stage := gate.Containers[f.dst].Objects[stageName]
	if got := gate.Containers[f.dst].Objects["scope/a-control"]; got.Digest != stagedContentDigest(f.control) || got.Size != int64(len(f.control)) {
		t.Fatalf("first landed effect not independently witnessed: %+v", got)
	}
	if _, exists := gate.Containers[f.dst].Objects["scope/b-subject"]; exists {
		t.Fatal("subject was already live at the selected barrier")
	}
	if stage.Class != "dlo" || len(stage.Segments) != 2 {
		t.Fatalf("subject did not stage as the intended two-part DLO: %+v", stage)
	}
	prefix, ok := strings.CutPrefix(stage.Manifest, f.segments+"/")
	if !ok || !strings.HasPrefix(prefix, stageName+"/") {
		t.Fatalf("staged manifest is outside its owned generated prefix: %+v", stage)
	}
	prefix = strings.TrimSuffix(prefix, "/") + "/"
	for i, segment := range stage.Segments {
		if segment.Name != f.segments+"/"+prefix+fmt.Sprintf("%08d", i) || segment.Size != 1<<20 ||
			segment.Digest != stagedContentDigest(f.subject[i*(1<<20):(i+1)*(1<<20)]) {
			t.Fatalf("unexpected owned staged segment: %+v", segment)
		}
	}
	return stageName, stage, gate
}

func alterStagedContent(t *testing.T, h *Harness, f stagedContentFixture, mode string, stage oracle.ObjectState) []byte {
	t.Helper()
	prefix := strings.TrimPrefix(stage.Manifest, f.segments+"/")
	prefix = strings.TrimSuffix(prefix, "/") + "/"
	put := func(index int, body []byte) {
		h.Seed(h.Secondary, f.segments, prefix+fmt.Sprintf("%08d", index), body, nil)
	}
	left, right := f.subject[:1<<20], f.subject[1<<20:]
	switch mode {
	case "control":
		return f.subject
	case "same_size_corruption":
		bad := bytes.Repeat([]byte("C"), 1<<20)
		put(0, bad)
		return append(bad, right...)
	case "permuted_segments":
		put(0, right)
		put(1, left)
		return append(append([]byte(nil), right...), left...)
	case "missing_with_duplicate":
		h.rawDeleteChecked(h.Secondary, "/"+f.segments+"/"+prefix+"00000000")
		put(2, right)
		return append(append([]byte(nil), right...), right...)
	case "resegmented_control":
		// The same prefix now assembles four pieces, so content equality must
		// survive a different segment count and segment entity tags.
		for i := 0; i < 4; i++ {
			put(i, f.subject[i*(1<<19):(i+1)*(1<<19)])
		}
		return f.subject
	default:
		t.Fatalf("unknown staged content case %q", mode)
		return nil
	}
}

func copyStagedContent(t *testing.T, h *Harness, mode string) {
	t.Helper()
	f := newStagedContentFixture(t, h)
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	sourceTrace := copyCompletionProxy(t, h, h.Primary, "")
	barrier, trace := stagedContentProxy(t, h, f.dst+"/scope/a-control", nil)
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: f.src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: f.dst, DestPrefix: "scope/",
		Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	stageName, stage, gate := stagedContentHeld(t, h, f, id, barrier)
	wantBody := alterStagedContent(t, h, f, mode, stage)
	altered := h.SnapshotNamespace(h.Secondary)
	changedStage := altered.Containers[f.dst].Objects[stageName]
	if changedStage.Class != "dlo" || changedStage.Manifest != stage.Manifest || changedStage.Size != int64(len(f.subject)) || changedStage.Digest != stagedContentDigest(wantBody) {
		t.Fatalf("controlled stage bytes not independently witnessed: %+v", changedStage)
	}
	var listedBytes int64
	for _, segment := range changedStage.Segments {
		listedBytes += segment.Size
	}
	if listedBytes != int64(len(f.subject)) {
		t.Fatalf("fixture would be refused by the old segment length check: %d", listedBytes)
	}
	if mode == "resegmented_control" && (len(changedStage.Segments) != 4 || reflect.DeepEqual(stage.Segments, changedStage.Segments)) {
		t.Fatal("matching control did not change segment structure")
	}
	if _, exists := altered.Containers[f.dst].Objects["scope/b-subject"]; exists {
		t.Fatal("subject became live while the first commit was held")
	}
	allowedAlterations := []string{f.dst + "/" + stageName}
	for _, segment := range append(append([]oracle.SegmentState(nil), stage.Segments...), changedStage.Segments...) {
		allowedAlterations = append(allowedAlterations, segment.Name)
	}
	h.AssertNoCollateral(gate, altered, allowedAlterations...)
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h)
	if err := barrier.Err(); err != nil {
		t.Fatal(err)
	}
	saved := copyCompletionRead(t, h.database, id)
	accepted := mode == "control" || mode == "resegmented_control"
	assertStagedContentResult(t, h, f, id, stageName, changedStage, accepted, jobs.CodeUnverified, saved, sourceTrace, trace)
	t.Logf("staged content boundary: mode=%s bytes=%d segments=%d state=%s copied=%d verified=%d failed=%d retained_recovery=%d",
		mode, len(f.subject), len(changedStage.Segments), saved.Job.State, saved.Job.Counters.ObjectsCopied,
		saved.Job.Counters.ObjectsVerified, saved.Job.Counters.ObjectsFailed, len(saved.Rows))
}

func TestCopyStagedContentMock(t *testing.T) {
	for _, mode := range []string{"control", "same_size_corruption", "permuted_segments", "missing_with_duplicate", "resegmented_control"} {
		t.Run(mode, func(t *testing.T) { copyStagedContent(t, findMockHarness(t), mode) })
	}
}

func TestCopyStagedContentRealSwift(t *testing.T) {
	for _, mode := range []string{"control", "same_size_corruption", "permuted_segments", "missing_with_duplicate", "resegmented_control"} {
		t.Run(mode, func(t *testing.T) { copyStagedContent(t, New(t), mode) })
	}
}

type stagedContentReadHold struct {
	mu      sync.Mutex
	path    string
	entered chan struct{}
	done    chan error
	abort   chan struct{}
}

func newStagedContentReadHold(t *testing.T) *stagedContentReadHold {
	t.Helper()
	hold := &stagedContentReadHold{entered: make(chan struct{}), done: make(chan error, 1), abort: make(chan struct{})}
	t.Cleanup(func() { close(hold.abort) })
	return hold
}

func (hold *stagedContentReadHold) arm(path string) {
	hold.mu.Lock()
	hold.path = path
	hold.mu.Unlock()
}

func (hold *stagedContentReadHold) serve(w http.ResponseWriter, r *http.Request, transport *http.Transport, target *url.URL) bool {
	hold.mu.Lock()
	selected := hold.path != "" && r.Method == http.MethodGet && r.URL.Path == hold.path && r.URL.RawQuery == ""
	if selected {
		hold.path = ""
	}
	hold.mu.Unlock()
	if !selected {
		return false
	}
	var outcome error
	defer func() { hold.done <- outcome }()
	request := r.Clone(r.Context())
	request.URL.Scheme, request.URL.Host = target.Scheme, target.Host
	request.RequestURI = ""
	response, err := transport.RoundTrip(request)
	if err != nil {
		outcome = fmt.Errorf("held staged GET: %w", err)
		http.Error(w, "fixture staged read failed", http.StatusBadGateway)
		return true
	}
	defer func() { outcome = errors.Join(outcome, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		outcome = fmt.Errorf("held staged GET status %d", response.StatusCode)
		http.Error(w, "fixture staged read status failed", http.StatusBadGateway)
		return true
	}
	for key, values := range response.Header {
		w.Header()[key] = append([]string(nil), values...)
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.CopyN(w, response.Body, 1); err != nil {
		outcome = fmt.Errorf("held staged first byte: %w", err)
		return true
	}
	if err := http.NewResponseController(w).Flush(); err != nil {
		outcome = fmt.Errorf("flush held staged first byte: %w", err)
		return true
	}
	close(hold.entered)
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
		outcome = r.Context().Err()
	case <-hold.abort:
		outcome = errors.New("held staged read aborted during cleanup")
	case <-timer.C:
		outcome = errors.New("held staged read did not observe cancellation")
	}
	return true
}

func (hold *stagedContentReadHold) cancel(t *testing.T, h *Harness, id string) {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-hold.entered:
	case err := <-hold.done:
		t.Fatalf("staged read failed before its body boundary: %v", err)
	case <-timer.C:
		t.Fatal("staged content GET never reached its body boundary")
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-hold.done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("held staged read did not retain request cancellation: %v", err)
		}
	case <-timer.C:
		t.Fatal("cancelled staged read handler did not finish")
	}
}

func copyStagedContentFault(t *testing.T, h *Harness, mode string) {
	t.Helper()
	f := newStagedContentFixture(t, h)
	if mode == "record_refusal" {
		f.later = []byte("later staged bytes")
		h.Seed(h.Primary, f.src, "scope/c-later", f.later, nil)
		f.beforeA = h.SnapshotNamespace(h.Primary)
	}
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	sourceTrace := copyCompletionProxy(t, h, h.Primary, "")
	var hold *stagedContentReadHold
	if mode == "cancel_final_read" {
		hold = newStagedContentReadHold(t)
	}
	barrier, trace := stagedContentProxy(t, h, f.dst+"/scope/a-control", hold)
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: f.src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: f.dst, DestPrefix: "scope/",
		Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	stageName, stage, _ := stagedContentHeld(t, h, f, id, barrier)
	if hold != nil {
		storage, err := url.Parse(h.Secondary.storageURL)
		if err != nil {
			t.Fatal(err)
		}
		hold.arm(storage.Path + "/" + f.dst + "/" + stageName)
	} else {
		wantBody := alterStagedContent(t, h, f, "same_size_corruption", stage)
		stage = h.SnapshotNamespace(h.Secondary).Containers[f.dst].Objects[stageName]
		if stage.Digest != stagedContentDigest(wantBody) || stage.Size != int64(len(f.subject)) {
			t.Fatalf("fault fixture did not establish same-sized wrong staged bytes: %+v", stage)
		}
	}
	query := ""
	switch mode {
	case "cancel_final_read":
	case "record_refusal":
		query = `CREATE TRIGGER refuse_staged_content_record BEFORE INSERT ON job_item_observations
			WHEN NEW.phase='execution' AND NEW.result='failed' AND EXISTS
			(SELECT 1 FROM job_items WHERE id=NEW.item_id AND item_key='b-subject')
			BEGIN SELECT RAISE(ABORT,'fixture staged failure record refusal'); END`
	case "terminal_refusal":
		query = `CREATE TRIGGER refuse_staged_content_terminal BEFORE UPDATE OF state ON jobs
			WHEN NEW.state IN ('complete','partial','failed','cancelled')
			BEGIN SELECT RAISE(ABORT,'fixture staged terminal refusal'); END`
	default:
		t.Fatalf("unknown staged content fault %q", mode)
	}
	if query != "" {
		if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
			t.Fatal(err)
		}
	}
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	if hold != nil {
		hold.cancel(t, h, id)
	}
	copyCompletionAwaitPins(t, h)
	if err := barrier.Err(); err != nil {
		t.Fatal(err)
	}
	saved := copyCompletionRead(t, h.database, id)
	assertStagedContentFault(t, h, f, id, stageName, stage, mode, saved, sourceTrace, trace)
}

func TestCopyStagedContentFaultMock(t *testing.T) {
	for _, mode := range []string{"cancel_final_read", "record_refusal", "terminal_refusal"} {
		t.Run(mode, func(t *testing.T) { copyStagedContentFault(t, findMockHarness(t), mode) })
	}
}

func TestCopyStagedContentFaultRealSwift(t *testing.T) {
	for _, mode := range []string{"cancel_final_read", "record_refusal", "terminal_refusal"} {
		t.Run(mode, func(t *testing.T) { copyStagedContentFault(t, New(t), mode) })
	}
}
