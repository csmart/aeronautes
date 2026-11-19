package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

const cleanupBenchmarkContainer = "aeronautes-benchmark-cleanup"

type benchmarkCleanupFixture struct {
	h       *harness
	service *BenchmarkService
	jobID   string
	cancel  context.CancelFunc
	done    chan struct{}
	cache   string
	objects int
	mu      sync.Mutex
	wire    []string
	batches [][]string
}

func newBenchmarkCleanupFixture(t *testing.T, fault string, barrier *swifttest.HTTPResponseBarrier) *benchmarkCleanupFixture {
	t.Helper()
	info := `{"bulk_delete":{"max_deletes_per_request":2}}`
	if fault == "native_only" {
		info = `{}`
	}
	h := newHarnessWithInfo(t, info)
	_, cache := deletionAttemptTestCache(t)
	f := &benchmarkCleanupFixture{h: h, done: make(chan struct{}), cache: cache, objects: 4}
	if fault == "chunked" || fault == "bulk_chunk_failure" {
		f.objects = 8
	}
	bulkCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.wire = append(f.wire, r.Method+" "+r.URL.RequestURI())
		bulk := r.Method == http.MethodPost && r.URL.Query().Has("bulk-delete")
		if bulk {
			bulkCalls++
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read bulk request: %v", err)
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			f.batches = append(f.batches, strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"))
		}
		firstBulk := bulk && bulkCalls == 1
		f.mu.Unlock()
		if fault == "upload_lost_effect" && r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/bench/000000") {
			h.mock.ServeHTTP(httptest.NewRecorder(), r)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if fault == "head_failure" && r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/bench/000000") ||
			fault == "native_failure" && r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/bench/000000") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if firstBulk && (fault == "bulk_no_effect" || fault == "bulk_lost_effect" || fault == "bulk_chunk_failure") {
			if fault == "bulk_lost_effect" {
				h.mock.ServeHTTP(httptest.NewRecorder(), r)
			}
			w.Header().Set("Content-Type", "application/json")
			// The same incomplete answer accompanies opposite actual effects.
			io.WriteString(w, `{"Number Deleted":0,"Number Not Found":0,"Response Status":"200 OK","Errors":[]}`)
			return
		}
		if firstBulk && fault == "foreign_name" {
			seedObject(h.mock, cleanupBenchmarkContainer, "leave-me", "unrelated bytes", nil)
		}
		h.mock.ServeHTTP(w, r)
	})
	var wrapped http.Handler = handler
	if barrier != nil {
		wrapped = barrier.Wrap(wrapped)
	}
	server := httptest.NewServer(wrapped)
	t.Cleanup(server.Close)
	if barrier != nil {
		t.Cleanup(barrier.Close)
	}
	h.authURL = server.URL + "/auth/v1.0"
	h.connect(t)
	f.service = NewBenchmarkService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.db)
	return f
}

func (f *benchmarkCleanupFixture) start(t *testing.T) {
	t.Helper()
	bookmark := f.h.bookmarkID()
	session, err := f.h.transfer.connections.SessionFor(t.Context(), bookmark)
	if err != nil {
		t.Fatal(err)
	}
	caps, err := session.FetchCapabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.h.transfer.jobs.Create(t.Context(), jobs.Job{
		Type: "benchmark", Source: "AUTH_test", Destination: cleanupBenchmarkContainer,
		SourceBookmarkID: bookmark, Engine: "native",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.jobID = job.ID
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	f.cancel = cancel
	f.h.transfer.runner.register(job.ID, cancel)
	go func() {
		defer close(f.done)
		f.service.runBenchmark(ctx, job.ID, bookmark, session, caps, cleanupBenchmarkContainer, f.objects, []int{1})
	}()
	t.Cleanup(func() {
		cancel()
		f.join(t)
	})
}

func (f *benchmarkCleanupFixture) join(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(25 * time.Second):
		t.Fatal("benchmark did not join")
	}
}

func TestBenchmarkCleanupOwnsOnlyItsIssuedNames(t *testing.T) {
	for _, fault := range []string{"native_only", "chunked", "head_failure", "upload_lost_effect", "native_failure", "bulk_no_effect", "bulk_lost_effect", "bulk_chunk_failure", "foreign_name"} {
		t.Run(fault, func(t *testing.T) {
			f := newBenchmarkCleanupFixture(t, fault, nil)
			f.start(t)
			f.join(t)
			job, err := f.h.jobsSvc.Get(t.Context(), f.h.bookmarkID(), f.jobID)
			if err != nil {
				t.Fatal(err)
			}
			wantState := "partial"
			if fault == "native_only" || fault == "chunked" {
				wantState = "complete"
			} else if fault == "head_failure" || fault == "upload_lost_effect" {
				wantState = "failed"
			}
			if job.State != wantState || job.Counters.DeletionAccountingIncomplete {
				t.Errorf("outcome = %+v; want %s with known accounting", job, wantState)
			}
			latest, err := f.service.Latest(t.Context(), f.h.bookmarkID())
			if err != nil || (latest != nil) != (wantState == "complete") {
				t.Errorf("recommendation = %+v %v", latest, err)
			}
			if wantState == "complete" && job.Counters.ObjectsCopied != job.Counters.ObjectsPlanned {
				t.Errorf("completed benchmark lost operations: %+v", job.Counters)
			}
			f.mu.Lock()
			requests := append([]string(nil), f.wire...)
			batches := append([][]string(nil), f.batches...)
			f.mu.Unlock()
			for _, batch := range batches {
				if len(batch) > 2 {
					t.Errorf("bulk request exceeds advertised limit: %v", batch)
				}
			}
			bulkCalls, containerDeletes := 0, 0
			deletionStarted := false
			for _, request := range requests {
				if strings.HasPrefix(request, "DELETE ") || strings.Contains(request, "bulk-delete") {
					deletionStarted = true
				}
				if deletionStarted && (strings.HasPrefix(request, "GET ") || strings.HasPrefix(request, "HEAD ")) {
					t.Errorf("cleanup consulted a listing or verifying read: %s", request)
				}
				if strings.Contains(request, "bulk-delete") {
					bulkCalls++
				}
				if request == "DELETE /v1/AUTH_test/"+cleanupBenchmarkContainer {
					containerDeletes++
				}
				if strings.HasPrefix(request, "DELETE ") && strings.Contains(request, "leave-me") {
					t.Errorf("cleanup adopted an unrelated name: %s", request)
				}
			}
			if strings.HasPrefix(fault, "bulk_") && (bulkCalls != 1 || containerDeletes != 0) {
				t.Errorf("unresolved batch reached more deletion: %v", requests)
			}
			if fault == "chunked" && (bulkCalls != 2 || containerDeletes != 1) {
				t.Errorf("healthy run did not finish both bounded batches: %v", requests)
			}
			if fault == "native_only" && bulkCalls != 0 {
				t.Errorf("native fallback issued a bulk request: %v", requests)
			}
			if fault == "native_failure" {
				deletes := 0
				for _, request := range requests {
					if strings.HasPrefix(request, "DELETE ") {
						deletes++
					}
				}
				if deletes != 1 || bulkCalls != 0 {
					t.Errorf("native failure admitted later deletion: %v", requests)
				}
			}
			session, err := f.h.transfer.connections.SessionFor(t.Context(), f.h.bookmarkID())
			if err != nil {
				t.Fatal(err)
			}
			if fault == "bulk_chunk_failure" {
				for _, name := range []string{"bench/000006", "bench/000007"} {
					if _, err := session.ObjectDetail(t.Context(), cleanupBenchmarkContainer, name); err != nil {
						t.Errorf("later batch did not survive the first failure: %s: %v", name, err)
					}
				}
			}
			if fault == "foreign_name" {
				body, _, err := session.OpenObject(t.Context(), cleanupBenchmarkContainer, "leave-me")
				if err != nil {
					t.Fatalf("unrelated object was lost: %v", err)
				}
				data, err := io.ReadAll(body)
				body.Close()
				if err != nil || string(data) != "unrelated bytes" {
					t.Errorf("unrelated bytes changed: %q %v", data, err)
				}
			}
			if wantState != "partial" {
				if _, err := session.ContainerDetail(t.Context(), cleanupBenchmarkContainer); !swiftcontrol.IsNotFound(err) {
					t.Errorf("settled benchmark cleanup did not remove its container: %v", err)
				}
			}
			if fault == "native_failure" || strings.HasPrefix(fault, "bulk_") {
				paths, err := filepath.Glob(filepath.Join(f.cache, "*.jsonl"))
				if err != nil || len(paths) != 1 || !strings.Contains(job.Error, paths[0]) {
					t.Fatalf("unresolved names were not retained: %v %v %s", paths, err, job.Error)
				}
				attempts := readDeletionAttempts(t, paths[0])
				wantNames := []string{"bench/000002", "bench/000003"}
				if fault == "native_failure" {
					wantNames = []string{"bench/000000"}
				} else if fault == "bulk_chunk_failure" {
					wantNames = []string{"bench/000004", "bench/000005"}
				}
				if len(attempts) != len(wantNames) {
					t.Fatalf("attempt records = %+v, want %v", attempts, wantNames)
				}
				for i, name := range wantNames {
					if attempts[i].Name != name || attempts[i].Detail == "" {
						t.Errorf("attempt identity or detail lost: %+v", attempts[i])
					}
					_, err := session.ObjectDetail(t.Context(), cleanupBenchmarkContainer, name)
					if fault == "bulk_lost_effect" {
						if !swiftcontrol.IsNotFound(err) {
							t.Errorf("applied effect was not witnessed: %v", err)
						}
					} else if err != nil {
						t.Errorf("refused name did not survive: %v", err)
					}
				}
			}
		})
	}
}

func TestBenchmarkCancelledBulkRetainsIssuedNames(t *testing.T) {
	barrier, err := swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
		Operation: t.Name(), Method: http.MethodPost, EscapedPath: "/v1/AUTH_test", RawQuery: "bulk-delete", Occurrence: 1,
	}, false, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	f := newBenchmarkCleanupFixture(t, "healthy", barrier)
	f.start(t)
	if _, err := barrier.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.cancel()
	f.join(t)
	job, err := f.h.jobsSvc.Get(t.Context(), f.h.bookmarkID(), f.jobID)
	if err != nil || job.State != "partial" || !strings.Contains(job.Error, "context canceled") {
		t.Fatalf("cancelled issued result = %+v %v", job, err)
	}
	paths, err := filepath.Glob(filepath.Join(f.cache, "*.jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("cancelled attempts = %v %v", paths, err)
	}
	attempts := readDeletionAttempts(t, paths[0])
	if len(attempts) != 2 || attempts[0].Name != "bench/000002" || attempts[1].Name != "bench/000003" {
		t.Errorf("cancelled issued identities changed: %+v", attempts)
	}
	if job.Settings != "" {
		t.Errorf("cancelled benchmark published measurements: %s", job.Settings)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, request := range f.wire {
		if request == "DELETE /v1/AUTH_test/"+cleanupBenchmarkContainer {
			t.Error("lost bulk response did not retain the container")
		}
	}
}

func TestBenchmarkReportsUnretainedDeletionEvidence(t *testing.T) {
	f := newBenchmarkCleanupFixture(t, "native_failure", nil)
	if err := os.MkdirAll(filepath.Dir(f.cache), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.cache, []byte("owned obstruction"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.start(t)
	f.join(t)
	job, err := f.h.jobsSvc.Get(t.Context(), f.h.bookmarkID(), f.jobID)
	if err != nil || job.State != "partial" || job.Counters.DeletionAccountingIncomplete ||
		!strings.Contains(job.Error, "unresolved deletion outcomes: 1") ||
		!strings.Contains(job.Error, "issued identities could not all be retained") ||
		strings.Contains(job.Error, "unresolved deletion attempts:") {
		t.Fatalf("evidence failure claimed a complete list or lost exact membership: %+v %v", job, err)
	}
	if data, err := os.ReadFile(f.cache); err != nil || string(data) != "owned obstruction" {
		t.Errorf("evidence writer replaced its obstruction: %q %v", data, err)
	}
}
