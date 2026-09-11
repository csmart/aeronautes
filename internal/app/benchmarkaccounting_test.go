package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/deletion"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

const benchmarkAccountingContainer = "aeronautes-benchmark-accounting"

// ADR 0036 forbids a new sweep or container DELETE after any checked
// accounting-storage error, including cleanup of an earlier failure.
func TestBenchmarkDeletionAccounting(t *testing.T) {
	for _, call := range []string{"fail_cleanup", "main_bulk", "no_bulk", "final_cleanup"} {
		for _, fault := range []string{"healthy", "index_write", "cache_setup"} {
			t.Run(call+"/"+fault, func(t *testing.T) {
				fixture := newBenchmarkAccountingFixture(t, call, fault)
				fixture.start(t)
				var stoppedAt []string
				if len(fixture.barriers) != 0 {
					for _, barrier := range fixture.barriers {
						event, err := barrier.Wait(t.Context())
						wantStatus := http.StatusOK
						if call == "no_bulk" {
							wantStatus = http.StatusInternalServerError
						}
						if fault == "cache_setup" {
							wantStatus = http.StatusNotFound
						}
						if err != nil || event.StatusCode != wantStatus {
							t.Fatalf("owned response barrier: %+v, %v; want status %d", event, err, wantStatus)
						}
					}
					if fixture.parallelListed != nil {
						// The parallel producer may finish its already-admitted
						// terminal page while both DELETE replies are held.
						select {
						case <-fixture.parallelListed:
						case <-time.After(15 * time.Second):
							t.Fatal("owned parallel listing did not finish before index fault")
						}
					}
					stoppedAt = fixture.h.mock.RequestLog()
					if fault == "index_write" {
						refuseDeletionAccounting(t, fixture.cache)
					} else {
						// The already-open main index can close normally; only the
						// next caller's default cache resolution now fails.
						blockBenchmarkAccountingCache(t)
					}
					for _, barrier := range fixture.barriers {
						if err := barrier.Release(); err != nil {
							t.Fatal(err)
						}
					}
				}
				fixture.join(t)
				for _, barrier := range fixture.barriers {
					if err := barrier.Err(); err != nil {
						t.Fatal(err)
					}
				}
				requests := fixture.h.mock.RequestLog()
				t.Logf("joined benchmark request log: %q", requests)
				if stoppedAt != nil && !reflect.DeepEqual(requests, stoppedAt) {
					t.Errorf("benchmark sent follow-up requests after accounting-storage failure: before=%v after=%v", stoppedAt, requests)
				}
				fixture.assertOutcome(t, requests)
			})
		}
	}
}

type benchmarkAccountingFixture struct {
	h              *harness
	service        *BenchmarkService
	session        *swiftcontrol.Session
	caps           *swiftcontrol.Capabilities
	cache          string
	bookmark       string
	jobID          string
	call           string
	fault          string
	barriers       []*swifttest.HTTPResponseBarrier
	done           chan struct{}
	parallelListed chan struct{}
}

func newBenchmarkAccountingFixture(t *testing.T, call, fault string) *benchmarkAccountingFixture {
	t.Helper()
	cache := deletionAccountingCache(t)
	info := `{"bulk_delete":{"max_deletes_per_request":2}}`
	if call == "no_bulk" {
		info = `{}`
	}
	h := newHarnessWithInfo(t, info)
	h.mock.AddContainer("benchmark-collateral", "standard")
	seedObject(h.mock, "benchmark-collateral", "keep", "untouched collateral", map[string]string{"Owner": "fixture"})
	f := &benchmarkAccountingFixture{h: h, cache: cache, call: call, fault: fault}
	if call == "no_bulk" && fault == "index_write" {
		f.parallelListed = make(chan struct{})
	}
	var bulkPosts atomic.Int32
	var terminalPages atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isBulk := r.Method == http.MethodPost && r.URL.EscapedPath() == "/v1/AUTH_test" && r.URL.RawQuery == "bulk-delete"
		var bulkNames []string
		var bulkNumber int
		if isBulk {
			bulkNumber = int(bulkPosts.Add(1))
			body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
			if err != nil || len(body) > 4096 {
				t.Errorf("bounded owned bulk request: %q %v", body, err)
				http.Error(w, "fixture bulk request invalid", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			bulkNames = strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
			if len(bulkNames) < 1 || len(bulkNames) > 2 {
				t.Errorf("bulk request exceeded the two-name fixture: %q", bulkNames)
			}
		}
		reply := httptest.NewRecorder()
		h.mock.ServeHTTP(reply, r)
		if call == "fail_cleanup" && r.Method == http.MethodHead && r.URL.Path == benchmarkAccountingPath("bench/000000") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if call == "final_cleanup" && isBulk && bulkNumber == 1 {
			// An owned residue makes the final cleanup exercise accounting;
			// an empty prefix would never insert an identity facet.
			seedObject(h.mock, benchmarkAccountingContainer, "cleanup-residue", "owned residue", nil)
		}
		failBulk := isBulk && fault == "index_write" && ((call != "final_cleanup" && bulkNumber == 1) || (call == "final_cleanup" && bulkNumber == 2))
		if failBulk {
			var errorsList [][2]string
			for _, name := range bulkNames {
				errorsList = append(errorsList, [2]string{name, "500 Internal Server Error"})
			}
			// Effects above have landed, but the service reports failures.
			// Indexing that response must not manufacture known survival.
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{
				"Number Deleted": 0, "Number Not Found": 0, "Response Status": "200 OK", "Response Body": "", "Errors": errorsList,
			}); err != nil {
				t.Errorf("write owned bulk response: %v", err)
			}
			return
		}
		if call == "no_bulk" && fault == "index_write" && r.Method == http.MethodDelete &&
			(r.URL.Path == benchmarkAccountingPath("bench/000002") || r.URL.Path == benchmarkAccountingPath("bench/000003")) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		for key, values := range reply.Header() {
			w.Header()[key] = append([]string(nil), values...)
		}
		w.WriteHeader(reply.Code)
		if _, err := w.Write(reply.Body.Bytes()); err != nil {
			t.Errorf("forward owned response: %v", err)
		}
		if f.parallelListed != nil && r.Method == http.MethodGet &&
			r.URL.Path == "/v1/AUTH_test/"+benchmarkAccountingContainer && r.URL.Query().Get("marker") == "bench/000003" && terminalPages.Add(1) == 2 {
			close(f.parallelListed)
		}
	})
	var wrapped http.Handler = handler
	var selectors []swifttest.HTTPResponseSelector
	if fault == "index_write" {
		if call == "no_bulk" {
			for _, name := range []string{"bench/000002", "bench/000003"} {
				selectors = append(selectors, swifttest.HTTPResponseSelector{
					Operation: "benchmark-accounting", Method: http.MethodDelete, EscapedPath: benchmarkAccountingPath(name), Occurrence: 1,
				})
			}
		} else {
			occurrence := 1
			if call == "final_cleanup" {
				occurrence = 2
			}
			selectors = append(selectors, swifttest.HTTPResponseSelector{
				Operation: "benchmark-accounting", Method: http.MethodPost, EscapedPath: "/v1/AUTH_test", RawQuery: "bulk-delete", Occurrence: occurrence,
			})
		}
	} else if fault == "cache_setup" && call == "final_cleanup" {
		selectors = append(selectors, swifttest.HTTPResponseSelector{
			Operation: "benchmark-accounting-setup", Method: http.MethodHead, EscapedPath: benchmarkAccountingPath("bench/000003"), Occurrence: 2,
		})
	}
	for _, selector := range selectors {
		barrier, err := swifttest.NewHTTPResponseBarrier(selector, false, 15*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		f.barriers = append(f.barriers, barrier)
		wrapped = barrier.Wrap(wrapped)
	}
	server := httptest.NewServer(wrapped)
	t.Cleanup(server.Close)
	for _, barrier := range f.barriers {
		t.Cleanup(barrier.Close)
	}
	h.authURL = server.URL + "/auth/v1.0"
	h.connect(t)
	f.bookmark = h.transfer.connections.ActiveBookmarkID()
	var err error
	f.session, err = h.transfer.connections.SessionFor(t.Context(), f.bookmark)
	if err != nil {
		t.Fatal(err)
	}
	f.caps, err = f.session.FetchCapabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if (f.caps.BulkDeleteMaxDeletes() > 0) != (call != "no_bulk") {
		t.Fatal("fixture advertised the wrong deletion strategy")
	}
	f.service = NewBenchmarkService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.db)
	if fault == "cache_setup" && call != "final_cleanup" {
		blockBenchmarkAccountingCache(t)
	}
	return f
}

func benchmarkAccountingPath(name string) string {
	return "/v1/AUTH_test/" + benchmarkAccountingContainer + "/" + name
}

func blockBenchmarkAccountingCache(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cache-is-a-file")
	if err := os.WriteFile(path, []byte("owned cache obstruction"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", path)
	t.Setenv("LocalAppData", path)
	t.Setenv("HOME", path)
}

func (f *benchmarkAccountingFixture) start(t *testing.T) {
	t.Helper()
	job, err := f.h.transfer.jobs.Create(t.Context(), jobs.Job{
		Type: jobs.Type("benchmark"), Source: "AUTH_test", Destination: benchmarkAccountingContainer,
		SourceBookmarkID: f.bookmark, Engine: "native",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.jobID = job.ID
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	f.done = make(chan struct{})
	f.h.transfer.runner.register(job.ID, cancel)
	go func() {
		defer close(f.done)
		f.service.runBenchmark(ctx, job.ID, f.bookmark, f.session, f.caps, benchmarkAccountingContainer, 4, []int{1})
	}()
	t.Cleanup(func() {
		cancel()
		for _, barrier := range f.barriers {
			barrier.Close()
		}
		f.join(t)
	})
}

func (f *benchmarkAccountingFixture) join(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(25 * time.Second):
		t.Fatal("owned benchmark did not join")
	}
}

func (f *benchmarkAccountingFixture) assertOutcome(t *testing.T, requests []string) {
	t.Helper()
	job, err := f.h.jobsSvc.Get(t.Context(), f.jobID)
	if err != nil || job == nil {
		t.Fatalf("Get after joined benchmark: %+v %v", job, err)
	}
	t.Logf("joined public benchmark: %+v", *job)
	state, result := "complete", "applied"
	if f.fault == "index_write" {
		state, result = "partial", "verify_mismatch"
	} else if f.fault == "cache_setup" || f.call == "fail_cleanup" {
		state, result = "failed", "failed"
	}
	incomplete := f.fault == "index_write"
	if job.State != state || job.Counters.DeletionAccountingIncomplete != incomplete {
		t.Errorf("benchmark accounting outcome = %+v; want state %s incomplete=%t", job, state, incomplete)
	}
	progress := int64(14)
	if f.call == "fail_cleanup" {
		progress = 8
	} else if f.call == "final_cleanup" || (f.call == "main_bulk" && f.fault == "healthy") {
		progress = 16
	}
	wantCounters := jobs.Counters{ObjectsPlanned: 16, ObjectsCopied: progress, DeletionAccountingIncomplete: incomplete}
	if job.Counters != wantCounters {
		t.Errorf("benchmark progress changed or invented deletion totals: %+v, want %+v", job.Counters, wantCounters)
	}
	if job.Type != "benchmark" || job.SourceBookmarkID != f.bookmark || job.Destination != benchmarkAccountingContainer || job.FinishedAt == "" {
		t.Errorf("benchmark terminal identity incomplete: %+v", job)
	}
	if _, registered := f.h.transfer.runner.cancel(f.jobID); registered {
		t.Error("joined terminal benchmark is still cancel-addressable")
	}
	if f.call == "fail_cleanup" && (!strings.HasPrefix(job.Error, "head: ") || !strings.Contains(job.Error, "HTTP 403")) {
		t.Errorf("original benchmark failure was lost: %s", job.Error)
	}
	if f.fault != "healthy" {
		stage := map[string]string{"fail_cleanup": "; cleanup: ", "main_bulk": "bulk-delete: ", "no_bulk": "cleanup-delete: ", "final_cleanup": "cleanup: "}[f.call]
		if !strings.Contains(job.Error, stage) || !strings.Contains(job.Error, deletion.ErrIdentityAccounting.Error()) {
			t.Errorf("accounting storage failure lost its call site: %s", job.Error)
		}
	}
	if incomplete {
		for _, text := range []string{"fixture deletion index refusal", "final failure and unresolved totals are unavailable", "some requests may have taken effect", "admitted-attempt records (not distinct totals): "} {
			if !strings.Contains(job.Error, text) {
				t.Errorf("benchmark accounting reason omits %q: %s", text, job.Error)
			}
		}
		f.assertAttempts(t, job.Error)
	} else if strings.Contains(job.Error, "deletion accounting is incomplete") || strings.Contains(job.Error, "admitted-attempt records") {
		t.Errorf("known accounting was incorrectly marked incomplete: %s", job.Error)
	}
	if !incomplete {
		paths, err := filepath.Glob(filepath.Join(f.cache, "aeronautes", "deletion-attempts", "*.jsonl"))
		if err != nil || len(paths) != 0 {
			t.Errorf("no unindexed attempts expected: %v %v", paths, err)
		}
	}
	list, err := f.h.jobsSvc.List(t.Context(), f.bookmark, false, 10)
	if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], *job) {
		t.Errorf("public Get/List disagree after join: %+v %v", list, err)
	}
	history, err := f.h.jobsSvc.History(t.Context(), f.bookmark, "", 10)
	if err != nil || len(history.Jobs) != 1 || !reflect.DeepEqual(history.Jobs[0], *job) {
		t.Errorf("public Get/History disagree after join: %+v %v", history, err)
	}
	events, err := f.h.auditSvc.Recent(t.Context(), 20)
	if err != nil {
		t.Fatal(err)
	}
	terminals := 0
	summary := "job " + f.jobID
	if incomplete {
		summary += "; deletion accounting incomplete"
	}
	for _, event := range events {
		if strings.HasPrefix(event.Operation, "benchmark.") {
			terminals++
			t.Logf("joined benchmark audit: %+v", event)
			if event.Operation != "benchmark."+state || event.Result != result || event.Resource != benchmarkAccountingContainer || event.Summary != summary {
				t.Errorf("benchmark terminal audit disagrees: %+v", event)
			}
		}
	}
	if terminals != 1 {
		t.Errorf("benchmark terminal audit count=%d, want 1", terminals)
	}
	latest, err := f.service.Latest(t.Context(), f.bookmark)
	if err != nil {
		t.Fatal(err)
	}
	if state != "complete" {
		if latest != nil || f.service.recommendationFor(f.bookmark) != nil || job.Settings != "" {
			t.Errorf("failed benchmark published measurements or recommendation: latest=%+v settings=%s", latest, job.Settings)
		}
	} else {
		if latest == nil || latest.RanAt == "" || latest.Recommendation.Transfers != 1 || latest.Recommendation.Checkers != 1 || f.service.recommendationFor(f.bookmark) == nil {
			t.Fatalf("healthy benchmark lost its recommendation: %+v", latest)
		}
		var phases []string
		for _, phase := range latest.Phases {
			phases = append(phases, phase.Phase)
			if phase.Concurrency != 1 || phase.OpsPerSec <= 0 {
				t.Errorf("healthy measurement invalid: %+v", phase)
			}
		}
		want := []string{"upload", "listing", "head", "delete"}
		if f.call != "no_bulk" {
			want = append(want, "bulk-delete")
		}
		if !reflect.DeepEqual(phases, want) {
			t.Errorf("healthy phases=%v, want %v", phases, want)
		}
		var saved BenchmarkResultDTO
		if err := json.Unmarshal([]byte(job.Settings), &saved); err != nil || !reflect.DeepEqual(saved.Phases, latest.Phases) || saved.Recommendation != latest.Recommendation {
			t.Errorf("job measurement document disagrees with Latest: %+v %v", saved, err)
		}
	}
	f.assertEffects(t, requests)
}

func (f *benchmarkAccountingFixture) assertAttempts(t *testing.T, reason string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(f.cache, "aeronautes", "deletion-attempts", "*.jsonl"))
	if err != nil || len(paths) != 1 {
		t.Errorf("one retained admitted-attempt file required: %v %v", paths, err)
		return
	}
	if !strings.Contains(reason, paths[0]) || strings.Contains(reason, "full list:") {
		t.Errorf("admitted attempts advertised incorrectly: %s", reason)
	}
	body, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 || body[len(body)-1] != '\n' {
		t.Fatalf("incomplete admitted-attempt JSONL: %q", body)
	}
	t.Logf("retained admitted-attempt JSONL: %s", body)
	var names []string
	for _, line := range bytes.Split(bytes.TrimSuffix(body, []byte{'\n'}), []byte{'\n'}) {
		var attempt deletion.UnindexedAttempt
		if err := json.Unmarshal(line, &attempt); err != nil {
			t.Fatal(err)
		}
		names = append(names, attempt.Name)
		if !strings.Contains(attempt.Detail, "500") {
			t.Errorf("admitted outcome lost reported error detail: %+v", attempt)
		}
	}
	want := []string{"bench/000002", "bench/000003"}
	if f.call == "fail_cleanup" {
		want = []string{"bench/000000", "bench/000001"}
	} else if f.call == "final_cleanup" {
		want = []string{"cleanup-residue"}
	}
	slices.Sort(names)
	if !reflect.DeepEqual(names, want) {
		t.Errorf("admitted-attempt names=%q, want exactly %q", names, want)
	}
}

func (f *benchmarkAccountingFixture) assertEffects(t *testing.T, requests []string) {
	t.Helper()
	containerDeletes, objectDeletes, bulkPosts, listings := 0, 0, 0, 0
	for _, request := range requests {
		switch {
		case request == "DELETE /v1/AUTH_test/"+benchmarkAccountingContainer:
			containerDeletes++
		case strings.HasPrefix(request, "DELETE "+benchmarkAccountingPath("")):
			objectDeletes++
		case request == "POST /v1/AUTH_test?bulk-delete":
			bulkPosts++
		case strings.HasPrefix(request, "GET /v1/AUTH_test/"+benchmarkAccountingContainer+"?"):
			listings++
		}
	}
	wantContainer, wantNative, wantBulk := 0, 2, 0
	if f.fault == "healthy" {
		wantContainer = 1
	}
	switch f.call {
	case "fail_cleanup":
		wantNative = 0
		if f.fault == "healthy" {
			wantBulk = 2
		} else if f.fault == "index_write" {
			wantBulk = 1
		}
	case "main_bulk":
		if f.fault != "cache_setup" {
			wantBulk = 1
		}
	case "no_bulk":
		if f.fault != "cache_setup" {
			wantNative = 4
		}
	case "final_cleanup":
		wantBulk = 2
		if f.fault == "cache_setup" {
			wantBulk = 1
		}
	}
	if containerDeletes != wantContainer || objectDeletes != wantNative || bulkPosts != wantBulk {
		t.Errorf("destructive request counts container=%d native=%d bulk=%d, want %d/%d/%d: %v", containerDeletes, objectDeletes, bulkPosts, wantContainer, wantNative, wantBulk, requests)
	}
	// The benchmark's original four-row listing includes its empty
	// terminal page; neither request belongs to a deletion sweep.
	if f.fault == "cache_setup" && listings != 2 {
		t.Errorf("cache setup refusal admitted a cleanup listing: %v", requests)
	}
	// Observe after freezing the request log so these independent reads
	// cannot hide follow-up work from the benchmark itself.
	var containers []string
	for entry, err := range f.session.Containers(t.Context(), swiftcontrol.ListOptions{Prefix: benchmarkAccountingContainer}) {
		if err != nil {
			t.Fatal(err)
		}
		containers = append(containers, entry.Name)
	}
	if f.fault == "healthy" {
		if len(containers) != 0 {
			t.Errorf("healthy cleanup left benchmark containers: %v", containers)
		}
	} else {
		if !reflect.DeepEqual(containers, []string{benchmarkAccountingContainer}) {
			t.Errorf("accounting failure did not retain the owned container: %v", containers)
		}
		var names []string
		for entry, err := range f.session.Objects(t.Context(), benchmarkAccountingContainer, swiftcontrol.ObjectListOptions{Deep: true}) {
			if err != nil {
				t.Fatal(err)
			}
			object, ok := entry.(swiftcontrol.ObjectEntry)
			if !ok {
				t.Fatalf("unexpected owned listing entry: %+v", entry)
			}
			names = append(names, object.Name)
		}
		var want []string
		if f.call == "fail_cleanup" {
			want = []string{"bench/000002", "bench/000003"}
			if f.fault == "cache_setup" {
				want = []string{"bench/000000", "bench/000001", "bench/000002", "bench/000003"}
			}
		} else if f.fault == "cache_setup" {
			want = []string{"bench/000002", "bench/000003"}
			if f.call == "final_cleanup" {
				want = []string{"cleanup-residue"}
			}
		}
		if !slices.Equal(names, want) {
			t.Errorf("owned benchmark survivor names=%q, want %q", names, want)
		}
		t.Logf("retained benchmark containers=%q names=%q", containers, names)
		for _, name := range names {
			wantBody := bytes.Repeat([]byte{0x42}, 8*1024)
			if name == "cleanup-residue" {
				wantBody = []byte("owned residue")
			}
			body, _, err := f.session.OpenObject(t.Context(), benchmarkAccountingContainer, name)
			if err != nil {
				t.Fatal(err)
			}
			content, readErr := io.ReadAll(io.LimitReader(body, 16*1024))
			closeErr := body.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(content, wantBody) {
				t.Errorf("retained object %q changed: bytes=%d read=%v close=%v", name, len(content), readErr, closeErr)
			}
		}
	}
	body, detail, err := f.session.OpenObject(t.Context(), "benchmark-collateral", "keep")
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || string(content) != "untouched collateral" || detail.Meta["Owner"] != "fixture" {
		t.Errorf("collateral changed: %q %+v read=%v close=%v", content, detail, readErr, closeErr)
	}
}
