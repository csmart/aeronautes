package deletion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"modernc.org/sqlite"
)

type accountingRequest struct {
	method, path, query, body string
}

type accountingTrace struct {
	next     http.Handler
	mu       sync.Mutex
	requests []accountingRequest
	problem  error
	returned chan accountingRequest
}

func (trace *accountingTrace) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	request := accountingRequest{method: r.Method, path: r.URL.EscapedPath(), query: r.URL.RawQuery}
	if r.Method == http.MethodPost && r.URL.RawQuery == "bulk-delete" {
		body, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
		if err != nil || len(body) > 64<<10 {
			trace.mu.Lock()
			trace.problem = fmt.Errorf("bounded fixture request body: bytes=%d err=%v", len(body), err)
			trace.mu.Unlock()
			http.Error(w, "fixture request body unavailable", http.StatusBadRequest)
			return
		}
		request.body = string(body)
		r.Body = io.NopCloser(strings.NewReader(request.body))
	}
	trace.mu.Lock()
	if len(trace.requests) >= 64 {
		trace.problem = errors.New("fixture request trace exceeded its bound")
	} else {
		trace.requests = append(trace.requests, request)
	}
	trace.mu.Unlock()
	defer func() {
		select {
		case trace.returned <- request:
		default:
			trace.mu.Lock()
			trace.problem = errors.New("fixture handler-return trace exceeded its bound")
			trace.mu.Unlock()
		}
	}()
	trace.next.ServeHTTP(w, r)
}

func (trace *accountingTrace) snapshot(t *testing.T) []accountingRequest {
	t.Helper()
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if trace.problem != nil {
		t.Fatal(trace.problem)
	}
	return slices.Clone(trace.requests)
}

func (trace *accountingTrace) awaitReturn(t *testing.T, method, path, query string) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case request := <-trace.returned:
			if request.method == method && request.path == path && request.query == query {
				return
			}
		case <-deadline.C:
			t.Fatalf("selected fixture handler did not return: %s %s?%s", method, path, query)
		}
	}
}

func accountingBarrier(t *testing.T, method, path, query string, occurrence int) *swifttest.HTTPResponseBarrier {
	t.Helper()
	barrier, err := swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
		Operation: t.Name(), Method: method, EscapedPath: path, RawQuery: query, Occurrence: occurrence,
	}, false, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return barrier
}

func accountingCluster(t *testing.T, failDeletes bool, barriers ...*swifttest.HTTPResponseBarrier) (*swiftcontrol.Session, *swiftcontrol.Capabilities, *swiftcontrol.Session, *accountingTrace) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	mock.FailDeletes = failDeletes
	mock.AddContainer("media", "standard")
	seedMany(mock, "media", "scope/", 6)
	seedMany(mock, "media", "kept/", 1)
	var handler http.Handler = mock
	for _, barrier := range barriers {
		handler = barrier.Wrap(handler)
	}
	trace := &accountingTrace{next: handler, returned: make(chan accountingRequest, 64)}
	actor := httptest.NewServer(trace)
	t.Cleanup(actor.Close)
	observerServer := httptest.NewServer(mock)
	t.Cleanup(observerServer.Close)
	for _, barrier := range barriers {
		t.Cleanup(barrier.Close)
	}
	connect := func(address string) *swiftcontrol.Session {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		session, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: address + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
		if err != nil {
			t.Fatal(err)
		}
		if err := session.Authenticate(ctx); err != nil {
			t.Fatal(err)
		}
		return session
	}
	session, observer := connect(actor.URL), connect(observerServer.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	capabilities, err := session.FetchCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return session, capabilities, observer, trace
}

func accountingIndexBoundary(t *testing.T, cache string, wantRows int, refuse bool) {
	t.Helper()
	dir := filepath.Join(cache, "aeronautes", "deletion-identities")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() || !strings.HasPrefix(entries[0].Name(), "delete-identities-") {
		t.Fatalf("owned index boundary: entries=%v err=%v", entries, err)
	}
	path := filepath.Join(dir, entries[0].Name(), "identities.sqlite")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("owned index file: info=%v err=%v", info, err)
	}
	connector, err := sqlite.NewConnector(identityIndexURI(path))
	if err != nil {
		t.Fatal(err)
	}
	side := sql.OpenDB(connector)
	side.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var rows int
	readErr := side.QueryRowContext(ctx, "SELECT count(*) FROM seen").Scan(&rows)
	var writeErr error
	if readErr == nil && refuse {
		_, writeErr = side.ExecContext(ctx, `CREATE TRIGGER refuse_seen BEFORE INSERT ON seen
			BEGIN SELECT RAISE(ABORT, 'fixture deletion index refusal'); END`)
	}
	closeErr := side.Close()
	if readErr != nil || rows != wantRows || writeErr != nil || closeErr != nil || side.Stats().OpenConnections != 0 {
		t.Fatalf("checked side connection: rows=%d want=%d read=%v write=%v close=%v stats=%+v", rows, wantRows, readErr, writeErr, closeErr, side.Stats())
	}
}

type accountingEvents struct {
	mu                 sync.Mutex
	failed, unresolved []Failure
	progress           [][2]int64
}

func (events *accountingEvents) options() Options {
	return Options{Workers: 2, BulkSize: 2, Passes: 3,
		OnFailure: func(name, detail string) {
			events.mu.Lock()
			defer events.mu.Unlock()
			events.failed = append(events.failed, Failure{Name: name, Err: detail})
		},
		OnUnresolved: func(name, detail string) {
			events.mu.Lock()
			defer events.mu.Unlock()
			events.unresolved = append(events.unresolved, Failure{Name: name, Err: detail})
		},
		OnProgress: func(deleted, failed int64) {
			events.mu.Lock()
			defer events.mu.Unlock()
			events.progress = append(events.progress, [2]int64{deleted, failed})
		},
	}
}

type accountingOutcome struct {
	result *Result
	err    error
}

func accountingRun(ctx context.Context, session *swiftcontrol.Session, capabilities *swiftcontrol.Capabilities, prefix bool, names []string, opts Options) <-chan accountingOutcome {
	done := make(chan accountingOutcome, 1)
	go func() {
		var result *Result
		var err error
		if prefix {
			result, err = DeletePrefix(ctx, session, capabilities, "media", "scope/", opts)
		} else {
			result, err = DeleteNames(ctx, session, capabilities, "media", names, opts)
		}
		done <- accountingOutcome{result, err}
	}()
	return done
}

func accountingAwait(t *testing.T, done <-chan accountingOutcome, cache string) accountingOutcome {
	t.Helper()
	select {
	case outcome := <-done:
		if outcome.result == nil {
			t.Fatalf("engine returned no result: %v", outcome.err)
		}
		entries, err := os.ReadDir(filepath.Join(cache, "aeronautes", "deletion-identities"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("index owner did not clean up before return: entries=%v err=%v", entries, err)
		}
		return outcome
	case <-time.After(30 * time.Second):
		t.Fatal("engine did not join admitted requests")
		return accountingOutcome{}
	}
}

func accountingNames() []string {
	names := make([]string, 6)
	for i := range names {
		names[i] = fmt.Sprintf("scope/obj-%04d", i)
	}
	return names
}

func accountingOnce(t *testing.T, got []Failure, want []string) {
	t.Helper()
	expected := map[string]bool{}
	for _, name := range want {
		expected[name] = true
	}
	if len(got) != len(expected) {
		t.Fatalf("identity records=%v want each of %v once", got, want)
	}
	for _, item := range got {
		if !expected[item.Name] || item.Err == "" {
			t.Fatalf("unexpected, repeated or detail-free identity: %+v", item)
		}
		delete(expected, item.Name)
	}
}

func accountingAttempts(t *testing.T, result *Result, want []string) {
	t.Helper()
	var got []string
	for _, item := range result.UnindexedAttempts {
		if item.Detail == "" {
			t.Fatalf("attempt lost its detail: %+v", item)
		}
		got = append(got, item.Name)
	}
	slices.Sort(got)
	expected := slices.Clone(want)
	slices.Sort(expected)
	if !slices.Equal(got, expected) {
		t.Fatalf("admitted attempts=%v want exactly %v", got, expected)
	}
}

func accountingStorageError(t *testing.T, outcome accountingOutcome) {
	t.Helper()
	var storageErr *sqlite.Error
	if !errors.Is(outcome.err, ErrIdentityAccounting) || !errors.As(outcome.err, &storageErr) || storageErr.Code() != 1811 || !strings.Contains(outcome.err.Error(), "fixture deletion index refusal") || !outcome.result.AccountingIncomplete {
		t.Fatalf("missing real index refusal and incomplete flag: result=%+v err=%v", outcome.result, outcome.err)
	}
}

func accountingWitness(t *testing.T, observer *swiftcontrol.Session, names []string, absent bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for _, name := range names {
		body, _, err := observer.OpenObject(ctx, "media", name)
		if absent {
			if !swiftcontrol.IsNotFound(err) {
				if body != nil {
					if closeErr := body.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				}
				t.Fatalf("effect witness still found %q: %v", name, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(io.LimitReader(body, 32))
		closeErr := body.Close()
		if readErr != nil || closeErr != nil || string(content) != "payload-xx" {
			t.Fatalf("survivor %q changed: body=%q read=%v close=%v", name, content, readErr, closeErr)
		}
	}
}

func TestDeletionAccountingBulkAdmittedFailure(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		for _, refused := range []bool{false, true} {
			t.Run(fmt.Sprintf("prefix_%t/refused_%t", prefix, refused), func(t *testing.T) {
				cache := ownedDeletionCache(t)
				barrier := accountingBarrier(t, http.MethodPost, "/v1/AUTH_test", "bulk-delete", 2)
				session, capabilities, observer, trace := accountingCluster(t, true, barrier)
				names := accountingNames()
				if !prefix {
					// Repeated selection names distinguish an exact index from
					// per-batch counting without needing another listing sweep.
					names[2], names[3] = names[0], names[1]
				}
				events := &accountingEvents{}
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				t.Cleanup(cancel)
				done := accountingRun(ctx, session, capabilities, prefix, names, events.options())
				if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
					t.Fatalf("second admitted bulk response: %+v %v", event, err)
				}
				accountingIndexBoundary(t, cache, 4, refused)
				if err := barrier.Release(); err != nil {
					t.Fatal(err)
				}
				outcome := accountingAwait(t, done, cache)
				if err := barrier.Err(); err != nil {
					t.Fatal(err)
				}
				want := names
				wantCalls := 3
				if refused {
					accountingStorageError(t, outcome)
					want, wantCalls = names[:2], 2
					accountingAttempts(t, outcome.result, names[2:4])
					for _, attempt := range outcome.result.UnindexedAttempts {
						if !strings.Contains(attempt.Detail, "service reported 500 Internal Server Error") {
							t.Fatalf("admitted batch lost the reported status: %+v", attempt)
						}
					}
				} else if outcome.err != nil || outcome.result.AccountingIncomplete || len(outcome.result.UnindexedAttempts) != 0 {
					t.Fatalf("healthy index: result=%+v err=%v", outcome.result, outcome.err)
				}
				accountingOnce(t, events.failed, want)
				accountingOnce(t, events.unresolved, want)
				accountingOnce(t, outcome.result.Failures, want)
				accountingOnce(t, outcome.result.Unresolveds, want)
				wantProgress := wantCalls
				if refused {
					wantProgress--
				}
				if outcome.result.Deleted != 0 || outcome.result.NotFound != 0 || outcome.result.Failed != int64(len(events.failed)) || outcome.result.Unresolved != int64(len(events.unresolved)) || len(events.progress) != wantProgress || events.progress[len(events.progress)-1] != [2]int64{0, outcome.result.Failed} {
					t.Fatalf("committed snapshots changed: result=%+v progress=%v", outcome.result, events.progress)
				}
				var bodies []string
				var initialLists int
				for _, request := range trace.snapshot(t) {
					if request.method == http.MethodPost && request.query == "bulk-delete" {
						bodies = append(bodies, request.body)
					}
					query, err := url.ParseQuery(request.query)
					if err != nil {
						t.Fatal(err)
					}
					if request.method == http.MethodGet && request.path == "/v1/AUTH_test/media" && query.Get("marker") == "" {
						initialLists++
					}
					if refused && (request.method == http.MethodHead || request.method == http.MethodDelete || query.Get("limit") == "1") {
						t.Fatalf("post-fault verification or destruction: %+v", request)
					}
				}
				if len(bodies) != wantCalls || (refused && initialLists != map[bool]int{false: 0, true: 1}[prefix]) {
					t.Fatalf("unexpected requests: bulk=%v initialLists=%d", bodies, initialLists)
				}
				for i, body := range bodies {
					wantBody := wire("media", names[2*i]) + "\n" + wire("media", names[2*i+1]) + "\n"
					if body != wantBody {
						t.Fatalf("bulk body=%q want=%q", body, wantBody)
					}
				}
				accountingWitness(t, observer, accountingNames(), false)
				accountingWitness(t, observer, []string{"kept/obj-0000"}, false)
			})
		}
	}
}

func TestDeletionAccountingParallelQueuedFailure(t *testing.T) {
	for _, refused := range []bool{false, true} {
		t.Run(fmt.Sprintf("refused_%t", refused), func(t *testing.T) {
			cache := ownedDeletionCache(t)
			names := accountingNames()
			first := accountingBarrier(t, http.MethodDelete, "/v1/AUTH_test/media/"+names[0], "", 1)
			second := accountingBarrier(t, http.MethodDelete, "/v1/AUTH_test/media/"+names[1], "", 1)
			query := url.Values{"format": {"json"}, "limit": {"10000"}, "prefix": {"scope/"}, "marker": {names[5]}}.Encode()
			page := accountingBarrier(t, http.MethodGet, "/v1/AUTH_test/media", query, 1)
			session, capabilities, observer, trace := accountingCluster(t, true, first, second, page)
			events := &accountingEvents{}
			opts := events.options()
			opts.ForceParallel = true
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			t.Cleanup(cancel)
			done := accountingRun(ctx, session, capabilities, true, names, opts)
			for _, barrier := range []*swifttest.HTTPResponseBarrier{first, second} {
				if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusInternalServerError {
					t.Fatalf("admitted single response: %+v %v", event, err)
				}
			}
			if event, err := page.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
				t.Fatalf("six accepted names did not reach the next marker: %+v %v", event, err)
			}
			// Both workers are held; accepting six names proves four are
			// buffered. End the listing before faulting the admitted outcomes.
			if err := page.Release(); err != nil {
				t.Fatal(err)
			}
			trace.awaitReturn(t, http.MethodGet, "/v1/AUTH_test/media", query)
			accountingIndexBoundary(t, cache, 0, refused)
			for _, barrier := range []*swifttest.HTTPResponseBarrier{first, second} {
				if err := barrier.Release(); err != nil {
					t.Fatal(err)
				}
			}
			outcome := accountingAwait(t, done, cache)
			for _, barrier := range []*swifttest.HTTPResponseBarrier{first, second, page} {
				if err := barrier.Err(); err != nil {
					t.Fatal(err)
				}
			}
			wantIssued := names
			if refused {
				accountingStorageError(t, outcome)
				wantIssued = names[:2]
				accountingAttempts(t, outcome.result, wantIssued)
				if outcome.result.Failed != 0 || outcome.result.Unresolved != 0 || len(events.failed) != 0 || len(events.unresolved) != 0 || len(events.progress) != 0 {
					t.Fatalf("uncommitted parallel facts escaped: result=%+v events=%+v", outcome.result, events)
				}
			} else {
				if outcome.err != nil || outcome.result.AccountingIncomplete || outcome.result.Failed != 6 || outcome.result.Unresolved != 0 {
					t.Fatalf("healthy parallel control: result=%+v err=%v", outcome.result, outcome.err)
				}
				accountingOnce(t, events.failed, names)
			}
			var issued []string
			var firstPages int
			for _, request := range trace.snapshot(t) {
				values, err := url.ParseQuery(request.query)
				if err != nil {
					t.Fatal(err)
				}
				if request.method == http.MethodDelete {
					issued = append(issued, strings.TrimPrefix(request.path, "/v1/AUTH_test/media/"))
				}
				if request.method == http.MethodGet && request.path == "/v1/AUTH_test/media" && values.Get("marker") == "" {
					firstPages++
				}
				if refused && (request.method == http.MethodHead || request.method == http.MethodPost || values.Get("limit") == "1") {
					t.Fatalf("post-latch verification or bulk request: %+v", request)
				}
			}
			slices.Sort(issued)
			if !slices.Equal(issued, wantIssued) || (refused && firstPages != 1) {
				t.Fatalf("queued identities were issued: got=%v want=%v firstPages=%d", issued, wantIssued, firstPages)
			}
			accountingWitness(t, observer, append(names, "kept/obj-0000"), false)
		})
	}
}

func TestDeletionAccountingCancelledBulkOutcome(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		for _, refused := range []bool{false, true} {
			t.Run(fmt.Sprintf("prefix_%t/refused_%t", prefix, refused), func(t *testing.T) {
				cache := ownedDeletionCache(t)
				barrier := accountingBarrier(t, http.MethodPost, "/v1/AUTH_test", "bulk-delete", 1)
				session, capabilities, observer, trace := accountingCluster(t, false, barrier)
				names := accountingNames()
				events := &accountingEvents{}
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				t.Cleanup(cancel)
				done := accountingRun(ctx, session, capabilities, prefix, names, events.options())
				if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
					t.Fatalf("admitted bulk response: %+v %v", event, err)
				}
				accountingWitness(t, observer, names[:2], true)
				accountingWitness(t, observer, names[2:], false)
				accountingIndexBoundary(t, cache, 0, refused)
				cancel()
				outcome := accountingAwait(t, done, cache)
				trace.awaitReturn(t, http.MethodPost, "/v1/AUTH_test", "bulk-delete")
				if !errors.Is(ctx.Err(), context.Canceled) || barrier.Err() == nil || !strings.Contains(barrier.Err().Error(), "holding response: context canceled") || barrier.Release() == nil {
					t.Fatal("the acknowledged response was not withheld by the deliberate cancellation")
				}
				if refused {
					accountingStorageError(t, outcome)
					accountingAttempts(t, outcome.result, names[:2])
					for _, attempt := range outcome.result.UnindexedAttempts {
						if !strings.Contains(attempt.Detail, context.Canceled.Error()) {
							t.Fatalf("unindexed admitted outcome lost caller cancellation: %+v", attempt)
						}
					}
					if outcome.result.Unresolved != 0 || len(events.unresolved) != 0 || len(events.progress) != 0 {
						t.Fatalf("unindexed cancelled outcomes became exact: result=%+v events=%+v", outcome.result, events)
					}
				} else {
					if !errors.Is(outcome.err, context.Canceled) || outcome.result.AccountingIncomplete || outcome.result.Unresolved != 2 || len(outcome.result.UnindexedAttempts) != 0 {
						t.Fatalf("caller cancellation discarded issued accounting: result=%+v err=%v", outcome.result, outcome.err)
					}
					accountingOnce(t, events.unresolved, names[:2])
					accountingOnce(t, outcome.result.Unresolveds, names[:2])
					for _, item := range events.unresolved {
						if !strings.Contains(item.Err, context.Canceled.Error()) {
							t.Fatalf("retained admitted outcome lost caller cancellation: %+v", item)
						}
					}
				}
				if outcome.result.Deleted != 0 || outcome.result.NotFound != 0 || outcome.result.Failed != 0 || len(events.failed) != 0 {
					t.Fatalf("lost reply invented an effect or error row: result=%+v events=%+v", outcome.result, events)
				}
				var bodies []string
				for _, request := range trace.snapshot(t) {
					if request.method == http.MethodPost && request.query == "bulk-delete" {
						bodies = append(bodies, request.body)
					}
					values, err := url.ParseQuery(request.query)
					if err != nil {
						t.Fatal(err)
					}
					if request.method == http.MethodHead || request.method == http.MethodDelete || values.Get("limit") == "1" {
						t.Fatalf("cancelled admitted outcome reached verification or more destruction: %+v", request)
					}
				}
				wantBody := wire("media", names[0]) + "\n" + wire("media", names[1]) + "\n"
				if !slices.Equal(bodies, []string{wantBody}) {
					t.Fatalf("cancelled job issued other names: %v", bodies)
				}
				accountingWitness(t, observer, names[:2], true)
				accountingWitness(t, observer, append(names[2:], "kept/obj-0000"), false)
			})
		}
	}
}
