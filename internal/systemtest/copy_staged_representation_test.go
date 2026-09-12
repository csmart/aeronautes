package systemtest

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

type stagedRepresentationRequest struct {
	Method, Path, Query, AcceptEncoding, Range, UserAgent string
	AcceptEncodingPresent                                 bool
}

type stagedRepresentationTrace struct {
	mu       sync.Mutex
	requests []stagedRepresentationRequest
}

func (trace *stagedRepresentationTrace) snapshot() []stagedRepresentationRequest {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]stagedRepresentationRequest(nil), trace.requests...)
}

func stagedRepresentationSource(t *testing.T, h *Harness) *stagedRepresentationTrace {
	t.Helper()
	storage, err := url.Parse(h.Primary.storageURL)
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
		t.Errorf("representation source proxy: %v", err)
		http.Error(w, "fixture source proxy failed", http.StatusBadGateway)
	}
	trace := &stagedRepresentationTrace{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.mu.Lock()
		trace.requests = append(trace.requests, stagedRepresentationRequest{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, AcceptEncoding: r.Header.Get("Accept-Encoding"),
			Range: r.Header.Get("Range"), UserAgent: r.Header.Get("User-Agent"),
			AcceptEncodingPresent: len(r.Header.Values("Accept-Encoding")) != 0,
		})
		trace.mu.Unlock()
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
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
		return trace
	}
	t.Fatal("owned representation source bookmark missing")
	return nil
}

func stagedGzipBytes(t *testing.T, plain []byte, name string) []byte {
	t.Helper()
	var out bytes.Buffer
	writer, err := gzip.NewWriterLevel(&out, gzip.NoCompression)
	if err != nil {
		t.Fatal(err)
	}
	writer.Name = name
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	decoded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("valid gzip control does not decode to its source: read=%v close=%v", readErr, closeErr)
	}
	return out.Bytes()
}

func representationError(h *Harness, message string) string {
	for _, secret := range []string{h.Primary.token, h.Secondary.token, h.Primary.key, h.Secondary.key} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	secretLine := regexp.MustCompile(`(?im)^.*(?:authorization|x-auth-token|x-auth-key|auth_token|password).*$`)
	return secretLine.ReplaceAllString(message, "[redacted authentication field]")
}

func copyRepresentation(t *testing.T, h *Harness, mode, coding string, body []byte, wantClass string) {
	t.Helper()
	f := newStagedContentFixture(t, h)
	f.subject = body
	headers := map[string]string{"X-Object-Meta-Team": "content-proof"}
	if coding != "" {
		headers["Content-Encoding"] = coding
	}
	h.Seed(h.Primary, f.src, "scope/b-subject", body, headers)
	f.beforeA = h.SnapshotNamespace(h.Primary)
	source := f.beforeA.Containers[f.src].Objects["scope/b-subject"]
	if source.Class != "plain" || source.Size != int64(len(body)) || source.Digest != stagedContentDigest(body) || source.ContentEncoding != coding || source.Meta["Team"] != "content-proof" {
		t.Fatalf("opaque source representation was not independently seeded: %+v", source)
	}
	if (wantClass == "dlo" && (len(body) <= 1<<20 || len(body) > 2<<20)) ||
		(wantClass == "plain" && len(body) > 1024) || (wantClass != "dlo" && wantClass != "plain") {
		t.Fatal("representation fixture does not have its declared bounded stage class")
	}
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	sourceTrace := stagedRepresentationSource(t, h)
	barrier, trace := stagedContentProxy(t, h, f.dst+"/scope/a-control", nil)
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: f.src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: f.dst, DestPrefix: "scope/",
		Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := barrier.Wait(t.Context())
	if err != nil || event.StatusCode != http.StatusCreated {
		copyCompletionAwaitPins(t, h)
		saved := copyCompletionRead(t, h.database, id)
		encoded, encodeErr := json.Marshal(map[string]any{
			"job_id": id, "state": saved.Job.State, "counters": saved.Job.Counters, "error": representationError(h, saved.Job.Error),
			"items": saved.Items, "item_totals": saved.ItemTotals, "recovery": saved.Rows, "audit": saved.Audit,
			"source_requests": sourceTrace.snapshot(), "destination_requests": trace.snapshot(),
			"source_before": f.beforeA, "destination_before": f.beforeB,
			"source_after": h.SnapshotNamespace(h.Primary), "destination_after": h.SnapshotNamespace(h.Secondary),
		})
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		t.Logf("opaque compatibility refusal after body/pins: %s", encoded)
		t.Fatalf("opaque route did not reach the held first publication: %+v %v", event, err)
	}
	page, err := h.jobStore.ReadItems(t.Context(), id, 0, 100)
	if err != nil || len(page.Items) != 2 || page.NextCursor != 0 {
		t.Fatalf("opaque route did not acknowledge both stages: %+v %v", page, err)
	}
	for _, item := range page.Items {
		if !item.Planned || !item.Admitted || item.Staging == nil || item.Execution != nil || item.Verification != nil {
			t.Fatalf("opaque route crossed its held record boundary: %+v", item)
		}
	}
	stageName := page.Items[1].Staging.Object
	if stageName != "scope/.aeronautes/stage/"+id+"/batch/b-subject" {
		t.Fatalf("opaque subject lost its exact admitted stage: %q", stageName)
	}
	held := h.SnapshotNamespace(h.Secondary)
	stage := held.Containers[f.dst].Objects[stageName]
	assertRepresentationStage(t, f, id, source, stage, wantClass)
	if control := held.Containers[f.dst].Objects["scope/a-control"]; control.Size != int64(len(f.control)) || control.Digest != stagedContentDigest(f.control) {
		t.Fatalf("opaque route lost first effect witness: %+v", control)
	}
	if _, exists := held.Containers[f.dst].Objects["scope/b-subject"]; exists {
		t.Fatal("opaque subject published before release")
	}
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h)
	if err := barrier.Err(); err != nil {
		t.Fatal(err)
	}
	saved := copyCompletionRead(t, h.database, id)
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	live := afterB.Containers[f.dst].Objects["scope/b-subject"]
	if saved.Job.State != "complete" || live.Class != wantClass || live.Size != source.Size || live.Digest != source.Digest ||
		live.Manifest != stage.Manifest || !reflect.DeepEqual(live.Segments, stage.Segments) {
		t.Fatalf("opaque compatibility failed: state=%s counters=%+v error=%q source=%+v stage=%+v live=%+v requests=%+v",
			saved.Job.State, saved.Job.Counters, representationError(h, saved.Job.Error), source, stage, live, sourceTrace.snapshot())
	}
	assertRepresentationHeaders(t, source, live, body)
	control := afterB.Containers[f.dst].Objects["scope/a-control"]
	if control.Class != "plain" || control.Size != int64(len(f.control)) || control.Digest != stagedContentDigest(f.control) {
		t.Fatalf("opaque route lost the completed first effect: %+v", control)
	}
	assertRepresentationHeaders(t, f.beforeA.Containers[f.src].Objects["scope/a-control"], control, f.control)
	h.AssertNoCollateral(f.beforeA, afterA)
	allowed := []string{f.dst + "/scope/a-control", f.dst + "/scope/b-subject"}
	for _, segment := range stage.Segments {
		allowed = append(allowed, segment.Name)
	}
	h.AssertNoCollateral(f.beforeB, afterB, allowed...)
	if wantClass == "dlo" {
		assertStagedContentWrites(t, h, f, id, stageName, stage, true, trace.snapshot())
		assertStagedContentRecovery(t, h, f, id, stage, true, saved)
	} else {
		assertPlainRepresentationWrites(t, h, f, id, trace.snapshot())
		wantRecovery := []jobs.RecoveryRecord{{JobID: id, BookmarkID: h.Secondary.BookmarkID,
			Kind: jobs.RecoveryStaging, Container: f.dst, Prefix: "scope/.aeronautes/stage/" + id + "/"}}
		if !reflect.DeepEqual(saved.Rows, wantRecovery) || saved.Status != (app.RecoveryStatusDTO{Jobs: 1, Records: 1}) || saved.Job.RecoveryRecords != 1 {
			t.Fatalf("plain publication acquired segment recovery authority or lost its staging intent: %+v", saved)
		}
	}
	assertRepresentationOutcome(t, h, f, id, saved)
	assertRepresentationWire(t, h, f, id, sourceTrace.snapshot(), trace.snapshot())
	t.Logf("opaque copy compatibility: mode=%s stage_class=%s stored_bytes=%d stored_md5=%x source=%+v stage=%+v live=%+v",
		mode, wantClass, len(body), md5.Sum(body), source, stage, live)
}

func assertRepresentationStage(t *testing.T, f stagedContentFixture, id string, source, stage oracle.ObjectState, wantClass string) {
	t.Helper()
	if stage.Class != wantClass || stage.Size != source.Size || stage.Digest != source.Digest ||
		stage.RawSLOManifest != "" || stage.SymlinkTarget != "" || stage.SymlinkAccount != "" {
		t.Fatalf("pinned engine did not retain the declared opaque stage: source=%+v stage=%+v", source, stage)
	}
	if wantClass == "plain" {
		if stage.Manifest != "" || len(stage.Segments) != 0 {
			t.Fatalf("bounded plain representation acquired backing segments: %+v", stage)
		}
		return
	}
	ownedPrefix := f.segments + "/scope/.aeronautes/stage/" + id + "/batch/b-subject/"
	if !strings.HasPrefix(stage.Manifest, ownedPrefix) || len(stage.Segments) != 2 {
		t.Fatalf("opaque DLO is not the exact owned two-segment route: %+v", stage)
	}
	var segmentBytes int64
	for i, segment := range stage.Segments {
		start, end := i*(1<<20), min((i+1)*(1<<20), len(f.subject))
		if segment.Name != strings.TrimSuffix(stage.Manifest, "/")+fmt.Sprintf("/%08d", i) ||
			segment.Size != int64(end-start) || segment.Digest != stagedContentDigest(f.subject[start:end]) || segment.ContentEncoding != "" {
			t.Fatalf("opaque backing bytes or identity differ: %+v", segment)
		}
		segmentBytes += segment.Size
	}
	if segmentBytes != int64(len(f.subject)) {
		t.Fatal("opaque fixture fails the existing segment length check")
	}
}

func assertRepresentationHeaders(t *testing.T, source, live oracle.ObjectState, body []byte) {
	t.Helper()
	if source.Meta["Team"] != "content-proof" {
		t.Fatalf("opaque fixture lost its source metadata: %+v", source)
	}
	if live.Name != source.Name || live.ContentType != source.ContentType || live.ContentEncoding != source.ContentEncoding ||
		live.ContentDisposition != source.ContentDisposition || live.DeleteAt != source.DeleteAt || live.RawSLOManifest != "" ||
		live.SymlinkTarget != "" || live.SymlinkAccount != "" {
		t.Fatalf("opaque publication changed preserved headers: source=%+v live=%+v", source, live)
	}
	wantMeta := maps.Clone(source.Meta)
	wantMeta["Aeronautes-Fingerprint"] = fmt.Sprintf("v1:%d:%x", len(body), md5.Sum(body))
	gotMeta := maps.Clone(live.Meta)
	if live.Class == "dlo" {
		// The optional aggregate stamp has its own accepted tests; this
		// representation check requires the exact preserved user metadata.
		delete(gotMeta, "Aeronautes-Staged-Aggregate")
	}
	if !maps.Equal(gotMeta, wantMeta) {
		t.Fatalf("opaque publication changed metadata: got=%+v want=%+v", gotMeta, wantMeta)
	}
}

func assertRepresentationOutcome(t *testing.T, h *Harness, f stagedContentFixture, id string, saved copyCompletionState) {
	t.Helper()
	wantCounters := jobs.Counters{ObjectsPlanned: 2, BytesPlanned: int64(len(f.subject) + len(f.control)), ObjectsCopied: 2,
		BytesCopied: int64(len(f.subject) + len(f.control)), ObjectsVerified: 2}
	if saved.Job.State != "complete" || saved.Job.FinishedAt == "" || saved.Job.Error != "" || saved.Job.Counters != wantCounters ||
		!saved.ItemsCaptured || len(saved.Items) != 2 || saved.ItemTotals != (jobs.ItemTotals{Items: 2, Planned: 2, Admitted: 2}) {
		t.Fatalf("opaque publication lost exact healthy accounting: state=%s finished=%q error=%q counters=%+v captured=%v totals=%+v items=%+v",
			saved.Job.State, saved.Job.FinishedAt, representationError(h, saved.Job.Error), saved.Job.Counters, saved.ItemsCaptured, saved.ItemTotals, saved.Items)
	}
	for i, name := range []string{"a-control", "b-subject"} {
		body := f.control
		if i == 1 {
			body = f.subject
		}
		item := saved.Items[i]
		wantStage := jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Secondary.BookmarkID, Container: f.dst,
			Object: "scope/.aeronautes/stage/" + id + "/batch/" + name}
		if item.JobID != id || item.Key != name || !item.Planned || !item.Admitted || item.ListedBytes != int64(len(body)) ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: f.src, Object: "scope/" + name}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Secondary.BookmarkID, Container: f.dst, Object: "scope/" + name}) ||
			item.Staging == nil || *item.Staging != wantStage || item.Execution == nil || item.Verification == nil {
			t.Fatalf("opaque copy lost the exact admitted identity: %+v", item)
		}
		if item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(body))}) ||
			item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
			t.Fatalf("opaque copy lost the exact healthy observations: %+v", item)
		}
	}
	segmentChangeAudit(t, h, saved, segmentChangeScope{src: f.src, dst: f.dst}, id, "transfer.copy.complete", audit.ResultApplied,
		" · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems")
	assertStagedContentPublic(t, h, id, saved)
	if repeat := copyCompletionRead(t, h.database, id); !reflect.DeepEqual(repeat, saved) {
		t.Fatal("public reads changed the opaque copy's retained facts")
	}
}

func assertPlainRepresentationWrites(t *testing.T, h *Harness, f stagedContentFixture, id string, requests []stagedContentRequest) {
	t.Helper()
	storage, err := url.Parse(h.Secondary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	base := storage.Path + "/" + f.dst + "/"
	stagePrefix := base + "scope/.aeronautes/stage/" + id + "/batch/"
	allowed := map[string]bool{}
	for _, name := range []string{"a-control", "b-subject"} {
		allowed[http.MethodPut+" "+stagePrefix+name] = true
		allowed[http.MethodDelete+" "+stagePrefix+name] = true
		allowed[http.MethodPut+" "+base+"scope/"+name] = true
	}
	readCount, readIndex, publicationIndex := 0, -1, -1
	var readCoding string
	for index, request := range requests {
		if request.Method == http.MethodGet && request.Path == stagePrefix+"b-subject" {
			if request.Query != "" {
				t.Fatalf("plain publication read did not name the complete stage: %+v", request)
			}
			readCount, readIndex, readCoding = readCount+1, index, request.AcceptEncoding
		}
		if request.Method == http.MethodPut && request.Path == base+"scope/b-subject" {
			publicationIndex = index
		}
		if request.Method != http.MethodPut && request.Method != http.MethodPost && request.Method != http.MethodDelete {
			continue
		}
		key := request.Method + " " + request.Path
		if !allowed[key] || request.Query != "" || request.Manifest != "" {
			t.Fatalf("plain representation acquired an unexpected mutation: %+v", request)
		}
		delete(allowed, key)
		if request.Method == http.MethodPut && (request.Path == base+"scope/a-control" || request.Path == base+"scope/b-subject") && request.IfNoneMatch != "*" {
			t.Fatalf("plain representation lost conditional publication: %+v", request)
		}
	}
	if len(allowed) != 0 || readCount != 1 || publicationIndex < 0 || readIndex >= publicationIndex {
		t.Fatalf("plain publication boundary differs: remaining=%+v reads=%d read_index=%d publication_index=%d", allowed, readCount, readIndex, publicationIndex)
	}
	// Plain publication already streams OpenObject into a conditional PUT;
	// its read is not the new DLO OpenObjectContent verification operation.
	t.Logf("plain publication GET: requests=%d expected_bytes=%d coding=%q read_index=%d publication_index=%d",
		readCount, len(f.subject), readCoding, readIndex, publicationIndex)
}

func assertRepresentationWire(t *testing.T, h *Harness, f stagedContentFixture, id string, sourceRequests []stagedRepresentationRequest, destinationRequests []stagedContentRequest) {
	t.Helper()
	sourceURL, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	destinationURL, err := url.Parse(h.Secondary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := sourceURL.Path + "/" + f.src + "/scope/b-subject"
	childRequests, subjectGETs := 0, 0
	for _, request := range sourceRequests {
		if request.Method == http.MethodPut || request.Method == http.MethodPost || request.Method == http.MethodDelete {
			t.Fatalf("opaque copy mutated the source: %+v", request)
		}
		if request.UserAgent == "rclone/v1.74.3" {
			childRequests++
			if request.AcceptEncodingPresent || request.AcceptEncoding != "" {
				t.Fatalf("Copy child negotiated a content transformation: %+v", request)
			}
		}
		if request.Method == http.MethodGet && request.Path == sourcePath {
			subjectGETs++
			if request.UserAgent != "rclone/v1.74.3" || request.Query != "" || request.Range != "" {
				t.Fatalf("opaque source read changed its engine, range or identity: %+v", request)
			}
		}
	}
	if childRequests == 0 || subjectGETs > 1 || (len(f.subject) > 0 && subjectGETs != 1) {
		t.Fatalf("opaque source engine route not established: child_requests=%d subject_gets=%d bytes=%d", childRequests, subjectGETs, len(f.subject))
	}
	stageRoot := "/scope/.aeronautes/stage/" + id + "/"
	for _, request := range destinationRequests {
		if request.Method == http.MethodPut &&
			(strings.HasPrefix(request.Path, destinationURL.Path+"/"+f.dst+stageRoot) || strings.HasPrefix(request.Path, destinationURL.Path+"/"+f.segments+stageRoot)) &&
			request.AcceptEncoding != "" {
			t.Fatalf("Copy child retained automatic encoding negotiation on an upload: %+v", request)
		}
	}
	t.Logf("opaque route wire: source_subject_gets=%d source_requests=%+v destination_requests=%+v", subjectGETs, sourceRequests, destinationRequests)
}

func TestCopyStagedRepresentationRealSwift(t *testing.T) {
	t.Parallel()
	plain := bytes.Repeat([]byte("opaque bytes"), 131072)
	first := stagedGzipBytes(t, plain, "encoding-a")
	second := stagedGzipBytes(t, plain, "encoding-b")
	if bytes.Equal(first, second) || md5.Sum(first) == md5.Sum(second) || len(first) != len(second) {
		t.Fatal("distinct gzip controls lost their distinct equal-length stored representations")
	}
	malformed := append([]byte("not a gzip stream"), plain...)
	for _, test := range []struct {
		name, coding string
		body         []byte
	}{
		{"no_coding", "", plain},
		{"valid_gzip", "gzip", first},
		{"same_decoded_other_gzip", "gzip", second},
		{"malformed_gzip", "gzip", malformed},
		{"unknown_coding", "x-aeronautes-opaque", plain},
	} {
		t.Run(test.name, func(t *testing.T) { copyRepresentation(t, New(t), test.name, test.coding, test.body, "dlo") })
	}
}

func TestCopyPlainRepresentationRealSwift(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, coding string
		body         []byte
	}{
		{"empty", "", nil},
		{"empty_gzip", "gzip", nil},
		{"small_valid_gzip", "gzip", stagedGzipBytes(t, []byte("small opaque content"), "small")},
		{"small_malformed_gzip", "gzip", []byte("not a gzip stream")},
	} {
		t.Run(test.name, func(t *testing.T) { copyRepresentation(t, New(t), test.name, test.coding, test.body, "plain") })
	}
}
