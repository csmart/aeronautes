package systemtest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	_ "modernc.org/sqlite"
)

func TestDeleteAccountingFailureRetainsIssuedNamesMock(t *testing.T) {
	deleteAccountingPublic(t, true, true, false)
}

func TestDeleteAccountingFailureReportsLostEvidenceMock(t *testing.T) {
	deleteAccountingPublic(t, true, true, true)
}

func TestDeleteAccountingHealthyIndexRetainsUnresolvedMock(t *testing.T) {
	deleteAccountingPublic(t, false, true, false)
}

func TestDeleteAccountingHealthyContainerDeleteMock(t *testing.T) {
	deleteAccountingPublic(t, false, false, false)
}

type accountingRequest struct {
	Method string `json:"method"`
	URI    string `json:"uri"`
	Body   string `json:"body,omitempty"`
}

type accountingWire struct {
	mu            sync.Mutex
	requests      []accountingRequest
	bulkRequests  int
	firstResponse string
	indexPath     string
	problem       error
}

// The wrapper withholds a real response while changing only the private
// index. The app must account for a request that already changed the store;
// substituting an accounting return would miss this public failure path.
func deletionAccountingFront(t *testing.T, h *Harness, cache string, failIndex, untrustedReply bool) *accountingWire {
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
	wire := &accountingWire{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		isBulk := r.Method == http.MethodPost && r.URL.EscapedPath() == storage.EscapedPath() && r.URL.RawQuery == "bulk-delete"
		if isBulk {
			var err error
			body, err = io.ReadAll(io.LimitReader(r.Body, 16*1024+1))
			if err != nil || len(body) > 16*1024 {
				wire.mu.Lock()
				wire.problem = fmt.Errorf("read bounded fixture bulk body: length %d: %w", len(body), err)
				wire.mu.Unlock()
				http.Error(w, "fixture body failure", http.StatusInternalServerError)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		wire.mu.Lock()
		wire.requests = append(wire.requests, accountingRequest{Method: r.Method, URI: r.URL.RequestURI(), Body: string(body)})
		if isBulk {
			wire.bulkRequests++
		}
		firstBulk := isBulk && wire.bulkRequests == 1
		wire.mu.Unlock()
		if r.URL.Path != "/info" && !firstBulk {
			proxy.ServeHTTP(w, r)
			return
		}
		response := httptest.NewRecorder()
		proxy.ServeHTTP(response, r)
		data := response.Body.Bytes()
		var problem error
		if r.URL.Path == "/info" {
			var info map[string]any
			if problem = json.Unmarshal(data, &info); problem == nil {
				// One-name batches expose queued versus issued identities
				// without a large fixture or a new executor test seam.
				info["bulk_delete"] = map[string]any{"max_deletes_per_request": 1}
				data, problem = json.Marshal(info)
			}
		} else {
			indexPath, err := installAccountingTrigger(r.Context(), cache, failIndex)
			problem = err
			wire.mu.Lock()
			wire.firstResponse, wire.indexPath = string(data), indexPath
			wire.mu.Unlock()
			if untrustedReply {
				data = []byte(`{}`)
			}
		}
		if problem != nil {
			wire.mu.Lock()
			wire.problem = errors.Join(wire.problem, problem)
			wire.mu.Unlock()
			http.Error(w, "fixture setup failure", http.StatusInternalServerError)
			return
		}
		for name, values := range response.Header() {
			w.Header()[name] = append([]string(nil), values...)
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.WriteHeader(response.Code)
		if _, err := w.Write(data); err != nil {
			wire.mu.Lock()
			wire.problem = errors.Join(wire.problem, fmt.Errorf("send fixture response: %w", err))
			wire.mu.Unlock()
		}
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
		if _, err := h.Browse.Connect(t.Context(), bookmark.ID); err != nil {
			t.Fatal(err)
		}
		wire.mu.Lock()
		wire.requests = nil
		wire.mu.Unlock()
		return wire
	}
	t.Fatal("primary bookmark not found")
	return nil
}

func installAccountingTrigger(ctx context.Context, cache string, fail bool) (path string, err error) {
	paths, err := filepath.Glob(filepath.Join(cache, "aeronautes", "deletion-identities", "delete-identities-*", "identities.sqlite"))
	if err != nil {
		return "", fmt.Errorf("find owned deletion index: %w", err)
	}
	if len(paths) != 1 {
		return "", fmt.Errorf("fixture needs exactly one active deletion index: %v", paths)
	}
	connection, err := sql.Open("sqlite", paths[0])
	if err != nil {
		return "", fmt.Errorf("open owned index: %w", err)
	}
	defer func() { err = errors.Join(err, connection.Close()) }()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	query := `CREATE TRIGGER refuse_system_identity BEFORE INSERT ON seen
		WHEN 0 BEGIN SELECT RAISE(ABORT,'owned system accounting refusal'); END`
	if fail {
		query = `CREATE TRIGGER refuse_system_identity BEFORE INSERT ON seen
			BEGIN SELECT RAISE(ABORT,'owned system accounting refusal'); END`
	}
	_, err = connection.ExecContext(ctx, query)
	if err != nil {
		return "", fmt.Errorf("install owned index trigger: %w", err)
	}
	return paths[0], nil
}

func deleteAccountingPublic(t *testing.T, failIndex, untrustedReply, loseEvidence bool) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("this owned cache-index fixture currently uses Linux XDG_CACHE_HOME")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	resolved, err := os.UserCacheDir()
	if err != nil || resolved != cache {
		t.Fatalf("cache is not the owned fixture: %q %v", resolved, err)
	}
	attemptDir := filepath.Join(cache, "aeronautes", "deletion-attempts")
	if loseEvidence {
		if err := os.MkdirAll(filepath.Dir(attemptDir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(attemptDir, []byte("owned evidence obstruction"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := findMockHarness(t)
	container := h.Container(h.Primary, "accounting-selected")
	primarySentinel := h.Container(h.Primary, "accounting-other")
	secondarySentinel := h.Container(h.Secondary, "accounting-other")
	names := []string{"00 %2F + issued", "01 queued", "02 queued"}
	for _, name := range names {
		h.Seed(h.Primary, container, name, []byte("owned "+name), map[string]string{"X-Object-Meta-Keep": "yes"})
	}
	h.Seed(h.Primary, primarySentinel, names[0], []byte("primary sentinel"), nil)
	h.Seed(h.Secondary, secondarySentinel, names[0], []byte("secondary sentinel"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	wire := deletionAccountingFront(t, h, cache, failIndex, untrustedReply)
	receipt := h.StartDelete(t.Context(), app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: container, Mode: app.ModeDeleteContainer, Confirm: container,
	})
	if receipt.Err() != nil {
		t.Fatal(receipt.Err())
	}
	job := h.AwaitJob(receipt.JobID())
	// Raw oracle reads bypass the front proxy, so the trace below includes
	// only the app's requests, not assertions or harness cleanup.
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	wire.mu.Lock()
	requests := append([]accountingRequest(nil), wire.requests...)
	bulkRequests, firstResponse, indexPath, fixtureErr := wire.bulkRequests, wire.firstResponse, wire.indexPath, wire.problem
	wire.mu.Unlock()
	if fixtureErr != nil || indexPath == "" {
		t.Fatalf("fixture did not reach the owned index: %q %v", indexPath, fixtureErr)
	}
	traceJSON, err := json.Marshal(requests)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("app request trace: %s; first actual response: %s", traceJSON, firstResponse)
	jobJSON, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("public terminal job: %s", jobJSON)
	if job.Counters.ObjectsPlanned != int64(len(names)) {
		t.Errorf("planned count = %d, want %d", job.Counters.ObjectsPlanned, len(names))
	}
	h.AssertNoCollateral(beforeB, afterB)
	attempts, err := filepath.Glob(filepath.Join(attemptDir, "attempts-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !untrustedReply {
		if job.State != "complete" || job.Error != "" || job.Counters.DeletionAccountingIncomplete || job.Counters.ObjectsCopied != 3 || job.Counters.ObjectsVerified != 3 || job.Counters.ObjectsFailed != 0 {
			t.Errorf("healthy deletion did not complete truthfully: %+v", job)
		}
		if _, present := afterA.Containers[container]; present || bulkRequests != len(names) || len(attempts) != 0 {
			t.Errorf("healthy container cleanup: present=%t bulk=%d attempts=%v", present, bulkRequests, attempts)
		}
		allowed := []string{container}
		for _, name := range names {
			allowed = append(allowed, container+"/"+name)
		}
		h.AssertNoCollateral(beforeA, afterA, allowed...)
		receipt.CreditHappy()
		return
	}
	if job.State != "partial" || job.Counters.ObjectsCopied != 0 || job.Counters.ObjectsVerified != 0 || job.Counters.ObjectsFailed != 0 {
		t.Errorf("issued but uncredited deletion must be partial: %+v", job)
	}
	if job.Counters.DeletionAccountingIncomplete != failIndex {
		t.Errorf("accounting qualifier = %t, want %t", job.Counters.DeletionAccountingIncomplete, failIndex)
	}
	selected, present := afterA.Containers[container]
	if !present {
		t.Fatal("container disappeared after the first uncredited request")
	}
	if _, present := selected.Objects[names[0]]; present || len(selected.Objects) != len(names)-1 {
		t.Errorf("first actual effect and queued survivors differ: %+v", selected.Objects)
	}
	h.AssertNoCollateral(beforeA, afterA, container+"/"+names[0])
	if bulkRequests != 1 {
		t.Errorf("later bulk requests were issued: %d", bulkRequests)
	}
	for i, request := range requests {
		if request.Method == http.MethodPost {
			want := "/" + url.PathEscape(container) + "/" + url.PathEscape(names[0]) + "\n"
			if request.Body != want {
				t.Errorf("issued body = %q, want exact first name %q", request.Body, want)
			}
			if i != len(requests)-1 {
				t.Errorf("app issued more requests after failed accounting/reply: %s", traceJSON)
			}
		}
	}
	if failIndex {
		for _, detail := range []string{"final failure and unresolved totals are unavailable", "some requests may have taken effect", "0 credited deletions", "owned system accounting refusal"} {
			if !strings.Contains(job.Error, detail) {
				t.Errorf("incomplete reason lacks %q: %s", detail, job.Error)
			}
		}
		if strings.Contains(job.Error, "full list:") {
			t.Errorf("attempt evidence is advertised as a complete distinct set: %s", job.Error)
		}
		if loseEvidence {
			if !strings.Contains(job.Error, "issued identities could not all be retained") || strings.Contains(job.Error, "admitted-attempt records (not distinct totals):") || len(attempts) != 0 {
				t.Errorf("lost attempt evidence was not disclosed: %s; files=%v", job.Error, attempts)
			}
		} else {
			if len(attempts) != 1 || !strings.Contains(job.Error, "admitted-attempt records (not distinct totals): "+attempts[0]) {
				t.Fatalf("separate admitted-attempt evidence unavailable: %s; files=%v", job.Error, attempts)
			}
			data, err := os.ReadFile(attempts[0])
			if err != nil {
				t.Fatal(err)
			}
			var attempt struct {
				Name   string `json:"name"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(data, &attempt); err != nil || attempt.Name != names[0] || attempt.Detail == "" {
				t.Errorf("attempt evidence does not retain the exact issued identity: %q: %v", data, err)
			}
			t.Logf("separate admitted-attempt evidence: %s", data)
		}
	} else if !strings.Contains(job.Error, "1 names have an unresolved effect") || !strings.Contains(job.Error, "full list:") || len(attempts) != 0 {
		t.Errorf("healthy index lost the known unresolved identity: %s; attempts=%v", job.Error, attempts)
	}
	receipt.CreditPartial()
}
