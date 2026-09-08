package systemtest

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

type copyListingFidelityCase struct {
	name, object, literal string
	valid                 bool
}

func copyListingFidelityProxy(t *testing.T, h *Harness, container string, tc copyListingFidelityCase) (*atomic.Int32, *uploadDeleteTrace) {
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
	var hits atomic.Int32
	proxy.ModifyResponse = func(response *http.Response) error {
		r := response.Request
		if r.Method != http.MethodGet || r.URL.Path != storage.Path+"/"+container || r.URL.Query().Get("format") != "json" || r.URL.Query().Get("prefix") != "scope/" || r.URL.Query().Get("marker") != "" {
			return nil
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		closeErr := response.Body.Close()
		if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || len(body) > 1<<20 {
			t.Errorf("fidelity proxy incomplete source page: status=%d read=%v close=%v bytes=%d", response.StatusCode, readErr, closeErr, len(body))
			return fmt.Errorf("invalid fixture page")
		}
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(body, &rows); err != nil {
			return err
		}
		matches := 0
		for _, row := range rows {
			var name string
			if err := json.Unmarshal(row["name"], &name); err != nil {
				return err
			}
			if name == tc.object {
				if bytes.Count(body, row["name"]) != 1 {
					return fmt.Errorf("fixture name token is not unique")
				}
				body = bytes.Replace(body, row["name"], []byte(tc.literal), 1)
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("fixture name matched %d rows", matches)
		}
		hits.Add(1)
		response.ContentLength = int64(len(body))
		response.Header.Set("Content-Length", strconv.Itoa(len(body)))
		response.Body = io.NopCloser(bytes.NewReader(body))
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("fidelity proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	trace := &uploadDeleteTrace{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.mu.Lock()
		trace.requests++
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			trace.writes = append(trace.writes, r.Method+" "+r.URL.Path)
		}
		trace.mu.Unlock()
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, bookmark := range bookmarks {
		if bookmark.ID != h.Primary.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, h.Primary.key); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatal("source fixture bookmark missing")
	}
	return &hits, trace
}

func copyListingFidelityRun(t *testing.T, h *Harness, tc copyListingFidelityCase) {
	t.Helper()
	src := h.Container(h.Primary, "listing-fidelity-src")
	dst := h.Container(h.Secondary, "listing-fidelity-dst")
	segments := h.Container(h.Secondary, "listing-fidelity-segments")
	aliases := h.Container(h.Secondary, "listing-fidelity-aliases")
	h.Seed(h.Primary, src, "scope/a\ncontrol", []byte("control"), nil)
	h.Seed(h.Primary, src, tc.object, []byte("selected"), nil)
	h.Seed(h.Primary, src, "outside", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, dst, "outside", []byte("destination sentinel"), nil)
	h.Seed(h.Secondary, dst, "scope/.aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	shared := []byte("shared bytes")
	h.Seed(h.Secondary, segments, "shared/0", shared, nil)
	h.Seed(h.Secondary, aliases, "dlo-alias", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	slo := fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(shared), len(shared))
	h.SeedSLO(h.Secondary, aliases, "slo-alias", slo)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	hits, srcTrace := copyListingFidelityProxy(t, h, src, tc)
	dstTrace := copyCompletionProxy(t, h, h.Secondary, "")
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/", DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/"})
	if err != nil {
		t.Fatal(err)
	}
	copyCompletionAwaitPins(t, h)
	saved := copyCompletionRead(t, h.database, id)
	visible, err := h.Jobs.Get(t.Context(), id)
	if err != nil || !reflect.DeepEqual(visible, saved.Job) {
		t.Errorf("public/durable summary mismatch: %v", err)
	}
	if !jobs.Terminal(jobs.State(saved.Job.State)) || saved.Job.StartedAt == "" || saved.Job.FinishedAt == "" {
		t.Errorf("missing terminal result: %+v", saved.Job)
	}
	if len(saved.Audit) != 1 || saved.Audit[0].BookmarkID != h.Primary.BookmarkID || saved.Audit[0].Operation != "transfer.copy."+saved.Job.State || saved.Audit[0].Resource != src+"/scope/ → "+dst+"/scope/" {
		t.Errorf("terminal audit lost authority or result: %+v", saved.Audit)
	}
	if len(saved.Rows) != 1 || saved.Rows[0].BookmarkID != h.Secondary.BookmarkID || saved.Rows[0].Container != dst || saved.Rows[0].Prefix != "scope/.aeronautes/stage/"+id+"/" {
		t.Errorf("recovery record lost: %+v", saved.Rows)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeA, afterA)
	allowed := []string{dst + "/scope/a\ncontrol", dst + "/" + tc.object}
	if stray := collateral(beforeB, afterB, allowed); len(stray) != 0 {
		t.Errorf("unselected fixture collateral changed: %v", stray)
	}
	if len(srcTrace.writeSnapshot()) != 0 {
		t.Errorf("source mutated: %v", srcTrace.writeSnapshot())
	}
	wantState, wantResult := "failed", audit.ResultFailed
	wantCounters := jobs.Counters{}
	if tc.valid {
		wantState, wantResult = "complete", audit.ResultApplied
		wantCounters = jobs.Counters{ObjectsPlanned: 2, BytesPlanned: 15, ObjectsCopied: 2, BytesCopied: 15, ObjectsVerified: 2, NativeRouted: 2}
	}
	if saved.Job.State != wantState || saved.Job.Counters != wantCounters || len(saved.Audit) != 1 || saved.Audit[0].Result != wantResult || saved.Audit[0].Summary != "job "+id {
		t.Errorf("EXPECTED FIDELITY OUTCOME: got=%+v audit=%+v want=%s %+v %s", saved.Job, saved.Audit, wantState, wantCounters, wantResult)
	}
	wantRecovery := []jobs.RecoveryRecord{{JobID: id, BookmarkID: h.Secondary.BookmarkID, Kind: jobs.RecoveryStaging, Container: dst, Prefix: "scope/.aeronautes/stage/" + id + "/"}}
	if !reflect.DeepEqual(saved.Rows, wantRecovery) || saved.Status != (app.RecoveryStatusDTO{Jobs: 1, Records: 1}) || saved.Job.RecoveryRecords != 1 {
		t.Errorf("exact recovery record differs: %+v", saved)
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err == nil {
		t.Error("completed Copy still accepts cancellation")
	}
	if tc.valid {
		if saved.Job.State != "complete" || saved.Job.Counters.ObjectsCopied != 2 || saved.Job.Counters.ObjectsVerified != 2 || saved.Job.Counters.ObjectsFailed != 0 {
			t.Errorf("valid-name Copy failed: %+v", saved.Job)
		}
		for _, name := range []string{"scope/a\ncontrol", tc.object} {
			want := beforeA.Containers[src].Objects[name]
			got, found := afterB.Containers[dst].Objects[name]
			if !found || got.Size != want.Size || got.Digest != want.Digest {
				t.Errorf("valid-name copy changed content: name=%q got=%+v want=%+v", name, got, want)
			}
		}
		if hits.Load() != 2 {
			t.Errorf("valid-name source planning and verification hits=%d", hits.Load())
		}
	} else {
		if !strings.Contains(saved.Job.Error, "listing row requires valid Unicode and unambiguous identity fields") {
			t.Errorf("Copy lost listing identity refusal: %q", saved.Job.Error)
		}
		if saved.Job.State != "failed" || saved.Job.Counters.ObjectsPlanned != 0 || saved.Job.Counters.ObjectsCopied != 0 || saved.Job.Counters.ObjectsVerified != 0 || len(dstTrace.writeSnapshot()) != 0 || hits.Load() != 1 {
			t.Errorf("LOSSY COPY ACCEPTED: literal=%q job=%+v destinationWrites=%q listingHits=%d", tc.literal, saved.Job, dstTrace.writeSnapshot(), hits.Load())
		}
		if changes := collateral(beforeB, afterB, nil); len(changes) != 0 {
			t.Errorf("LOSSY COPY EFFECT: %v", changes)
		}
	}
	encoded, err := json.Marshal(struct {
		Case   string
		Valid  bool
		Hits   int32
		Writes []string
		Saved  copyCompletionState
	}{tc.name, tc.valid, hits.Load(), dstTrace.writeSnapshot(), saved})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("listing fidelity observation: %s", encoded)
	requests := srcTrace.requestCount() + dstTrace.requestCount()
	copyCompletionReopen(t, h, id, saved)
	if requests != srcTrace.requestCount()+dstTrace.requestCount() {
		t.Error("orderly database reopen contacted remote")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
	t.Log("listing fidelity reopen and collateral complete")
}

func copyListingFidelityCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	cases := []copyListingFidelityCase{
		{name: "case-name", object: "scope/b�\nnative", literal: `"scope/decoy","Name":"scope/b�\nnative"`},
		{name: "escaped-case-name", object: "scope/b�\nnative", literal: `"scope/decoy","\u004eame":"scope/b�\nnative"`},
		{name: "duplicate-name", object: "scope/b�\nnative", literal: `"scope/decoy","name":"scope/b�\nnative"`},
		{name: "escaped-duplicate-name", object: "scope/b�\nnative", literal: `"scope/decoy","\u006eame":"scope/b�\nnative"`},
		{name: "raw-invalid", object: "scope/b�\nnative", literal: "\"scope/b\xff\\nnative\""},
		{name: "lone-high", object: "scope/b�\nnative", literal: `"scope/b\ud800\nnative"`},
		{name: "lone-low", object: "scope/b�\nnative", literal: `"scope/b\udc00\nnative"`},
		{name: "literal-replacement", object: "scope/b�\nnative", literal: `"scope/b�\nnative"`, valid: true},
		{name: "escaped-replacement", object: "scope/b�\nnative", literal: `"scope/b\ufffd\nnative"`, valid: true},
		{name: "valid-pair", object: "scope/b🚀\nnative", literal: `"scope/b\ud83d\ude80\nnative"`, valid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { copyListingFidelityRun(t, makeHarness(t), tc) })
	}
}

func TestCopyListingFidelityMock(t *testing.T)      { copyListingFidelityCases(t, findMockHarness) }
func TestCopyListingFidelityRealSwift(t *testing.T) { copyListingFidelityCases(t, New) }
