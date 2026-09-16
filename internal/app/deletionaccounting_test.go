package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/deletion"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"modernc.org/sqlite"
)

func deletionAccountingCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("LocalAppData", root)
	t.Setenv("HOME", root)
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if relative, err := filepath.Rel(root, cache); err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("cache is outside the owned fixture: %s: %v", cache, err)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	return cache
}

func TestDeleteAccountingStorageLifecycle(t *testing.T) {
	for _, fault := range []string{"control", "setup", "cleanup"} {
		t.Run(fault, func(t *testing.T) {
			if fault == "cleanup" && runtime.GOOS == "windows" {
				t.Skip("owned open-directory rename fault is exercised on Unix")
			}
			cache := deletionAccountingCache(t)
			h := newHarnessWithInfo(t, `{"bulk_delete":{"max_deletes_per_request":2}}`)
			h.mock.AddContainer("selected", "standard")
			for _, name := range []string{"a", "b"} {
				seedObject(h.mock, "selected", name, "owned data", nil)
			}
			var barrier *swifttest.HTTPResponseBarrier
			if fault == "cleanup" {
				var err error
				barrier, err = swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
					Operation: "delete-index-cleanup", Method: http.MethodGet,
					EscapedPath: "/v1/AUTH_test/selected", RawQuery: "format=json&limit=1", Occurrence: 1,
				}, false, 30*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(barrier.Wrap(h.mock))
				t.Cleanup(server.Close)
				t.Cleanup(barrier.Close)
				h.authURL = server.URL + "/auth/v1.0"
			}
			h.connect(t)
			if fault == "setup" {
				if err := os.WriteFile(filepath.Join(cache, "aeronautes"), []byte("owned cache obstruction"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			bookmark := h.transfer.connections.ActiveBookmarkID()
			service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
			id, err := service.StartDelete(t.Context(), DeleteRequestDTO{
				BookmarkID: bookmark, Container: "selected", Confirm: "selected", Mode: ModeDeleteContainer,
			})
			if err != nil {
				t.Fatal(err)
			}
			if barrier != nil {
				if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
					t.Fatalf("final verification response: %+v %v", event, err)
				}
				// All outcomes are committed before this final verification
				// response. Obstruct only removal, not outcome accounting.
				parent := filepath.Join(cache, "aeronautes", "deletion-identities")
				if err := os.Rename(parent, parent+"-held"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(parent, []byte("owned removal obstruction"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := barrier.Release(); err != nil {
					t.Fatal(err)
				}
			}
			awaitJobBindingsReleased(t, h, []string{bookmark})
			job, err := h.jobsSvc.Get(t.Context(), h.bookmarkID(), id)
			if err != nil {
				t.Fatal(err)
			}
			wantState, wantDeleted := "complete", int64(2)
			if fault == "setup" {
				wantState, wantDeleted = "failed", 0
			} else if fault == "cleanup" {
				wantState = "partial"
			}
			if job.State != wantState || job.Counters.ObjectsCopied != wantDeleted || job.Counters.ObjectsVerified != wantDeleted || job.Counters.ObjectsFailed != 0 || job.Counters.DeletionAccountingIncomplete {
				t.Fatalf("storage lifecycle lost known totals: %+v", job)
			}
			if fault != "control" && (!strings.Contains(job.Error, "deletion accounting storage unavailable: "+fault) || strings.Contains(job.Error, "final failure and unresolved totals are unavailable")) {
				t.Errorf("storage failure misqualified the outcome: %s", job.Error)
			}
			bulkRequests, containerDeletes := 0, 0
			for _, request := range h.mock.RequestLog() {
				if strings.HasPrefix(request, "POST ") && strings.Contains(request, "bulk-delete") {
					bulkRequests++
				}
				if request == "DELETE /v1/AUTH_test/selected" {
					containerDeletes++
				}
			}
			wantBulk, wantContainer := 1, 0
			if fault == "setup" {
				wantBulk = 0
			} else if fault == "control" {
				wantContainer = 1
			}
			if bulkRequests != wantBulk || containerDeletes != wantContainer {
				t.Errorf("unexpected destruction: bulk=%d container=%d", bulkRequests, containerDeletes)
			}
			if barrier != nil && barrier.Err() != nil {
				t.Fatal(barrier.Err())
			}
			session, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: h.authURL, Username: "test:tester", APIKey: "testing"})
			if err != nil {
				t.Fatal(err)
			}
			if err := session.Authenticate(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"a", "b"} {
				_, err := session.ObjectDetail(t.Context(), "selected", name)
				if (fault == "setup" && err != nil) || (fault != "setup" && !swiftcontrol.IsNotFound(err)) {
					t.Errorf("wrong observed effect for %s: %v", name, err)
				}
			}
		})
	}
}

func startAccountingDelete(t *testing.T, failDeletes bool) (*harness, string, *swifttest.HTTPResponseBarrier) {
	t.Helper()
	h := newHarnessWithInfo(t, `{"bulk_delete":{"max_deletes_per_request":2}}`)
	h.mock.AddContainer("selected", "standard")
	for i := 0; i < 4; i++ {
		seedObject(h.mock, "selected", fmt.Sprintf("name-%d", i), "owned data", nil)
	}
	h.mock.FailDeletes = failDeletes
	barrier, err := swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
		Operation: "delete-completion", Method: http.MethodPost, EscapedPath: "/v1/AUTH_test",
		RawQuery: "bulk-delete", Occurrence: 1,
	}, false, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(barrier.Wrap(h.mock))
	t.Cleanup(server.Close)
	t.Cleanup(barrier.Close)
	h.authURL = server.URL + "/auth/v1.0"
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	jobID, err := service.StartDelete(t.Context(), DeleteRequestDTO{
		BookmarkID: bookmark, Container: "selected", Confirm: "selected", Mode: ModeDeleteContainer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
		t.Fatalf("admitted response: %+v %v", event, err)
	}
	return h, jobID, barrier
}

func reopenedAccountingJob(t *testing.T, h *harness, id string) jobs.Job {
	t.Helper()
	var sequence int
	var name, path string
	if err := h.db.Reader().QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	row, err := jobs.NewStore(reopened).Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestDeleteAccountingCompletionIsAtomic(t *testing.T) {
	for _, fault := range []string{"control", "counters", "reason", "state", "audit"} {
		t.Run(fault, func(t *testing.T) {
			cache := deletionAccountingCache(t)
			h, id, barrier := startAccountingDelete(t, true)
			bookmark := h.transfer.connections.ActiveBookmarkID()
			before, err := h.jobsSvc.Get(t.Context(), h.bookmarkID(), id)
			if err != nil {
				t.Fatal(err)
			}
			query := ""
			switch fault {
			case "counters":
				query = `CREATE TRIGGER refuse_completion BEFORE UPDATE OF counters_json ON jobs
					WHEN json_extract(NEW.counters_json,'$.deletionAccountingIncomplete')=1
					BEGIN SELECT RAISE(ABORT,'fixture final counters refusal'); END`
			case "reason":
				query = `CREATE TRIGGER refuse_completion BEFORE UPDATE OF error ON jobs
					BEGIN SELECT RAISE(ABORT,'fixture final reason refusal'); END`
			case "state":
				query = `CREATE TRIGGER refuse_completion BEFORE UPDATE OF state ON jobs WHEN NEW.state='partial'
					BEGIN SELECT RAISE(ABORT,'fixture terminal refusal'); END`
			case "audit":
				query = `CREATE TRIGGER refuse_completion BEFORE INSERT ON audit_events WHEN NEW.operation='delete.delete_container.partial'
					BEGIN SELECT RAISE(ABORT,'fixture terminal audit refusal'); END`
			}
			if query != "" {
				if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
					_, err := tx.ExecContext(t.Context(), query)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			refuseDeletionAccounting(t, cache)
			if err := barrier.Release(); err != nil {
				t.Fatal(err)
			}
			// The successful binding edit acknowledges exit from the whole
			// launch, including failed terminal publication and pin release.
			awaitJobBindingsReleased(t, h, []string{bookmark})
			if err := barrier.Err(); err != nil {
				t.Fatal(err)
			}
			visible, getErr := h.jobsSvc.Get(t.Context(), h.bookmarkID(), id)
			list, listErr := h.jobsSvc.List(t.Context(), bookmark, false, 10)
			history, historyErr := h.jobsSvc.History(t.Context(), bookmark, "", 10)
			row := reopenedAccountingJob(t, h, id)
			events, err := h.auditSvc.Search(t.Context(), AuditQueryDTO{OperationPrefix: "delete.delete_container.partial", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if fault == "control" {
				if getErr != nil || visible == nil || visible.State != "partial" || !visible.Counters.DeletionAccountingIncomplete || !reflect.DeepEqual(jobDTO(row), *visible) {
					t.Fatalf("final state was not stored with its qualifier: %+v %v %+v", visible, getErr, row)
				}
				if listErr != nil || historyErr != nil || len(list) != 1 || len(history.Jobs) != 1 || !reflect.DeepEqual(list[0], *visible) || !reflect.DeepEqual(history.Jobs[0], *visible) {
					t.Fatalf("terminal public projections differ: %+v %v %+v %v", list, listErr, history, historyErr)
				}
				if len(events) != 1 || events[0].Summary != "job "+id+"; deletion accounting incomplete" || events[0].Result != "verify_mismatch" {
					t.Fatalf("terminal audit differs: %+v", events)
				}
			} else {
				if visible != nil || getErr == nil || list != nil || listErr == nil || historyErr == nil || len(history.Jobs) != 0 {
					t.Errorf("partial persistence remained publicly available: %+v %v %+v %v %+v %v", visible, getErr, list, listErr, history, historyErr)
				}
				if getErr != nil && !strings.Contains(getErr.Error(), "final result could not be saved") {
					t.Errorf("wrong unavailable diagnostic: %v", getErr)
				}
				if row.State != jobs.StateRunning || row.Counters != before.Counters || row.Error != before.Error || !row.FinishedAt.IsZero() || len(events) != 0 {
					t.Errorf("terminal transaction leaked: before=%+v reopened=%+v audit=%+v", before, row, events)
				}
			}
			if _, active := h.transfer.runner.cancel(id); active {
				t.Error("finished launch remains cancel-addressable")
			}
		})
	}
}

func TestDeleteCancellationRetainsAdmittedOutcomes(t *testing.T) {
	for _, failedIndex := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed_index_%t", failedIndex), func(t *testing.T) {
			cache := deletionAccountingCache(t)
			h, id, barrier := startAccountingDelete(t, false)
			observerServer := httptest.NewServer(h.mock)
			t.Cleanup(observerServer.Close)
			observer, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: observerServer.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
			if err != nil {
				t.Fatal(err)
			}
			if err := observer.Authenticate(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"name-0", "name-1"} {
				if _, err := observer.ObjectDetail(t.Context(), "selected", name); !swiftcontrol.IsNotFound(err) {
					t.Fatalf("admitted deletion was not witnessed outside the actor: %s: %v", name, err)
				}
			}
			if failedIndex {
				refuseDeletionAccounting(t, cache)
			}
			beforeCancel := len(h.mock.RequestLog())
			if err := h.transfer.CancelJob(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			awaitJobBindingsReleased(t, h, []string{h.transfer.connections.ActiveBookmarkID()})
			job, err := h.jobsSvc.Get(t.Context(), h.bookmarkID(), id)
			if err != nil {
				t.Fatal(err)
			}
			if job.State != "partial" || job.Counters.ObjectsCopied != 0 || job.Counters.ObjectsFailed != 0 || job.Counters.DeletionAccountingIncomplete != failedIndex {
				t.Fatalf("cancel discarded admitted accounting: %+v", job)
			}
			if err := barrier.Err(); !errors.Is(err, context.Canceled) {
				t.Errorf("lost response was not caller cancellation: %v", err)
			}
			if after := h.mock.RequestLog()[beforeCancel:]; len(after) != 0 {
				t.Errorf("cancellation admitted follow-up HTTP: %v", after)
			}
			if failedIndex {
				paths, err := filepath.Glob(filepath.Join(cache, "aeronautes", "deletion-attempts", "*.jsonl"))
				if err != nil || len(paths) != 1 || !strings.Contains(job.Error, paths[0]) {
					t.Fatalf("cancelled attempted identities not retained: %v %v %s", paths, err, job.Error)
				}
				attempts := readDeletionAttempts(t, paths[0])
				if len(attempts) != 2 || attempts[0].Name != "name-0" || attempts[1].Name != "name-1" {
					t.Errorf("cancelled attempts = %+v", attempts)
				}
			} else {
				records := retainedRecords(t, job.Error)
				if len(records) != 2 || len(records["name-0"]) != 1 || len(records["name-1"]) != 1 || !strings.Contains(job.Error, "2 names have an unresolved effect") {
					t.Errorf("healthy cancelled accounting lost the admitted set: %v %s", records, job.Error)
				}
			}
			for _, name := range []string{"name-2", "name-3"} {
				if _, err := observer.ObjectDetail(t.Context(), "selected", name); err != nil {
					t.Errorf("unissued object did not survive: %s: %v", name, err)
				}
			}
		})
	}
}

func refuseDeletionAccounting(t *testing.T, cache string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(cache, "aeronautes", "deletion-identities", "delete-identities-*", "identities.sqlite"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("one owned active index required: %v %v", paths, err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(paths[0])}
	u.RawQuery = "mode=rw"
	connector, err := sqlite.NewConnector(u.String())
	if err != nil {
		t.Fatal(err)
	}
	side := sql.OpenDB(connector)
	side.SetMaxOpenConns(1)
	_, writeErr := side.ExecContext(t.Context(), `CREATE TRIGGER refuse_seen BEFORE INSERT ON seen
		BEGIN SELECT RAISE(ABORT, 'fixture deletion index refusal'); END`)
	closeErr := side.Close()
	if writeErr != nil || closeErr != nil || side.Stats().OpenConnections != 0 {
		t.Fatalf("closed side trigger: write=%v close=%v stats=%+v", writeErr, closeErr, side.Stats())
	}
}

func TestDeleteAccountingFailureIsPublic(t *testing.T) {
	for _, occurrence := range []int{1, 2} {
		for _, blockedAttempts := range []bool{false, true} {
			t.Run(fmt.Sprintf("batch_%d/attempt_writer_blocked_%t", occurrence, blockedAttempts), func(t *testing.T) {
				cache := deletionAccountingCache(t)
				h := newHarnessWithInfo(t, `{"bulk_delete":{"max_deletes_per_request":2}}`)
				h.mock.AddContainer("selected", "standard")
				for i := 0; i < 6; i++ {
					seedObject(h.mock, "selected", fmt.Sprintf("name-%d", i), "owned data", nil)
				}
				h.mock.FailDeletes = true
				barrier, err := swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
					Operation: "delete-accounting", Method: http.MethodPost, EscapedPath: "/v1/AUTH_test",
					RawQuery: "bulk-delete", Occurrence: occurrence,
				}, false, 30*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(barrier.Wrap(h.mock))
				t.Cleanup(server.Close)
				t.Cleanup(barrier.Close)
				h.authURL = server.URL + "/auth/v1.0"
				h.connect(t)
				bookmark := h.transfer.connections.ActiveBookmarkID()
				service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
				jobID, err := service.StartDelete(t.Context(), DeleteRequestDTO{
					BookmarkID: bookmark, Container: "selected", Confirm: "selected", Mode: ModeDeleteContainer,
				})
				if err != nil {
					t.Fatal(err)
				}
				if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
					t.Fatalf("admitted response: %+v %v", event, err)
				}
				refuseDeletionAccounting(t, cache)
				if blockedAttempts {
					if err := os.WriteFile(filepath.Join(cache, "aeronautes", "deletion-attempts"), []byte("owned obstruction"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := barrier.Release(); err != nil {
					t.Fatal(err)
				}
				job := awaitTerminal(t, h, jobID)
				if err := barrier.Err(); err != nil {
					t.Fatal(err)
				}
				body, err := json.Marshal(job.Counters)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]any
				if err := json.Unmarshal(body, &fields); err != nil {
					t.Fatal(err)
				}
				if job.State != "partial" || fields["deletionAccountingIncomplete"] != true || job.Counters.ObjectsCopied != 0 || job.Counters.ObjectsFailed != int64(2*(occurrence-1)) {
					t.Errorf("unindexable admitted outcomes were published as final counts: %+v", job)
				}
				for _, text := range []string{"final failure and unresolved totals are unavailable", "some requests may have taken effect", "fixture deletion index refusal"} {
					if !strings.Contains(job.Error, text) {
						t.Errorf("terminal reason omits %q: %s", text, job.Error)
					}
				}
				if strings.Contains(job.Error, "full list:") {
					t.Errorf("partial evidence advertised as a full list: %s", job.Error)
				}
				paths, err := filepath.Glob(filepath.Join(cache, "aeronautes", "deletion-attempts", "*.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				if blockedAttempts {
					if len(paths) != 0 || !strings.Contains(job.Error, "issued identities could not all be retained") {
						t.Errorf("attempt retention loss hidden: %v %s", paths, job.Error)
					}
				} else if len(paths) != 1 || !strings.Contains(job.Error, paths[0]) {
					t.Errorf("admitted-attempt evidence unavailable: %v %s", paths, job.Error)
				} else {
					body, err := os.ReadFile(paths[0])
					if err != nil {
						t.Fatal(err)
					}
					var names []string
					for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
						var attempt deletion.UnindexedAttempt
						if err := json.Unmarshal([]byte(line), &attempt); err != nil {
							t.Fatal(err)
						}
						names = append(names, attempt.Name)
						if !strings.Contains(attempt.Detail, "500 Internal Server Error") {
							t.Errorf("lost admitted reply detail: %+v", attempt)
						}
					}
					want := []string{fmt.Sprintf("name-%d", 2*(occurrence-1)), fmt.Sprintf("name-%d", 2*(occurrence-1)+1)}
					if !reflect.DeepEqual(names, want) {
						t.Errorf("attempt records = %v, want %v", names, want)
					}
				}
				posts := 0
				for _, request := range h.mock.RequestLog() {
					if strings.Contains(request, "bulk-delete") {
						posts++
					}
					if strings.HasPrefix(request, "DELETE ") || strings.Contains(request, "limit=1&") {
						t.Errorf("follow-up destruction or verification after accounting failure: %s", request)
					}
				}
				if posts != occurrence {
					t.Errorf("issued %d bulk requests, want %d", posts, occurrence)
				}
				list, err := h.jobsSvc.List(t.Context(), bookmark, false, 10)
				if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], job) {
					t.Errorf("Get/List disagree: %+v %v", list, err)
				}
				history, err := h.jobsSvc.History(t.Context(), bookmark, "", 10)
				if err != nil || len(history.Jobs) != 1 || !reflect.DeepEqual(history.Jobs[0], job) {
					t.Errorf("Get/History disagree: %+v %v", history, err)
				}
				events, err := h.auditSvc.RecentFor(context.Background(), h.bookmarkID(), 10)
				if err != nil {
					t.Fatal(err)
				}
				terminals := 0
				for _, event := range events {
					if event.Operation == "delete.delete_container.partial" {
						terminals++
						if event.Summary != "job "+jobID+"; deletion accounting incomplete" || event.Result != "verify_mismatch" || event.Resource != "selected/" {
							t.Errorf("terminal audit omits controlled accounting fact: %+v", event)
						}
					}
				}
				if terminals != 1 {
					t.Errorf("terminal audit count=%d, want 1 at terminal visibility", terminals)
				}
			})
		}
	}
}
