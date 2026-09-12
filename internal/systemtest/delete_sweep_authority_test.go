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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
)

// A deletion job sends at most one destructive request per name. After a
// valid partial bulk reply that credits one deletion and blames one name,
// the next pass may list that name again, but the job never requests it
// again: a replacement written at the blamed name after the first request
// survives, and the job says how many listed names it did not request
// again. A name first listed in a later pass is still requested, because
// the prefix scope is the operator's consent, and that open hazard is
// witnessed here rather than implied. Each case forwards every request to
// the real store through a front that records the wire, substitutes only
// the first bulk reply, and installs the replacement and the late name
// through a separate fixture client before that reply is released. The
// archived before probe under docs/experiments/probes/
// 2026-09-12-delete-sweep-authority* recorded the replacement being
// deleted by a second request on both providers.

type sweepAuthorityCase struct {
	name        string
	partial     bool
	replacement bool
	// late seeds a fresh object under the prefix before the first reply is
	// released, so the second pass lists a name this job never requested.
	late bool
	// refuseIssued installs a trigger that refuses the issued record before
	// any request, so the job must stop without sending one.
	refuseIssued bool
}

func sweepAuthorityCases() []sweepAuthorityCase {
	return []sweepAuthorityCase{
		{name: "partial_reply_replacement", partial: true, replacement: true},
		{name: "partial_reply_replacement_and_late_name", partial: true, replacement: true, late: true},
		{name: "partial_reply_no_replacement", partial: true},
		{name: "issued_record_refused", refuseIssued: true},
		{name: "healthy_control"},
	}
}

func TestDeleteSweepAuthorityMock(t *testing.T) {
	for _, c := range sweepAuthorityCases() {
		t.Run(c.name, func(t *testing.T) { deleteSweepAuthority(t, findMockHarness(t), c) })
	}
}

func TestDeleteSweepAuthorityRealSwift(t *testing.T) {
	for _, c := range sweepAuthorityCases() {
		t.Run(c.name, func(t *testing.T) { deleteSweepAuthority(t, New(t), c) })
	}
}

type sweepWire struct {
	mu               sync.Mutex
	bulkBodies       []string
	firstReply       string
	substituted      string
	replacedAt       string
	problem          error
	requestCount     int
	listings         int
	triggerInstalled bool
	indexPath        string
}

// sweepFront forwards every request to the store. On the first bulk request
// it lets the store apply the deletions, then installs the replacement
// through the harness's own fixture client, then answers with a valid
// partial reply that credits one deletion and blames the second name.
func sweepFront(t *testing.T, h *Harness, cache, container, blamed, replacementBody, lateName string, c sweepAuthorityCase) *sweepWire {
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
	wire := &sweepWire{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire.mu.Lock()
		wire.requestCount++
		wire.mu.Unlock()
		isBulk := r.Method == http.MethodPost && r.URL.EscapedPath() == storage.EscapedPath() && r.URL.RawQuery == "bulk-delete"
		isListing := r.Method == http.MethodGet && r.URL.EscapedPath() == storage.EscapedPath()+"/"+url.PathEscape(container)
		if isListing && c.refuseIssued {
			// The planning count lists before the job's index exists; the
			// sweep's own first listing comes after it. Refuse the issued
			// record from the first listing that finds the index, which is
			// before any request could be sent.
			wire.mu.Lock()
			wire.listings++
			installed := wire.triggerInstalled
			wire.mu.Unlock()
			if !installed {
				path, err := installIssuedTrigger(r.Context(), cache)
				if err == nil {
					wire.mu.Lock()
					wire.triggerInstalled, wire.indexPath = true, path
					wire.mu.Unlock()
				} else if !strings.Contains(err.Error(), "exactly one active deletion index") {
					wire.mu.Lock()
					wire.problem = errors.Join(wire.problem, err)
					wire.mu.Unlock()
					http.Error(w, "fixture setup failure", http.StatusInternalServerError)
					return
				}
			}
		}
		if !isBulk {
			proxy.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024+1))
		if err != nil || len(body) > 16*1024 {
			wire.mu.Lock()
			wire.problem = errors.Join(wire.problem, fmt.Errorf("read bounded bulk body: %d bytes: %w", len(body), err))
			wire.mu.Unlock()
			http.Error(w, "fixture body failure", http.StatusInternalServerError)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		wire.mu.Lock()
		wire.bulkBodies = append(wire.bulkBodies, string(body))
		first := len(wire.bulkBodies) == 1
		wire.mu.Unlock()
		if !first || !c.partial {
			proxy.ServeHTTP(w, r)
			return
		}
		// Let the store apply the first request exactly as sent.
		recorded := httptest.NewRecorder()
		proxy.ServeHTTP(recorded, r)
		wire.mu.Lock()
		wire.firstReply = recorded.Body.String()
		wire.mu.Unlock()
		if c.replacement {
			// A separate writer puts new content at the blamed name before
			// the sweep learns anything about the first request.
			h.Seed(h.Primary, container, blamed, []byte(replacementBody), map[string]string{"X-Object-Meta-Writer": "replacement"})
			wire.mu.Lock()
			wire.replacedAt = time.Now().UTC().Format(time.RFC3339Nano)
			wire.mu.Unlock()
		}
		if c.late {
			// A name this job never listed before appears under the prefix.
			h.Seed(h.Primary, container, lateName, []byte("late object under the prefix"), nil)
		}
		blamedLine := ""
		want := strings.TrimSuffix(bulkPathLine(container, blamed), "\n")
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			if line == want {
				blamedLine = line
			}
		}
		if blamedLine == "" {
			wire.mu.Lock()
			wire.problem = errors.Join(wire.problem, fmt.Errorf("first bulk body did not carry the blamed name: %q", body))
			wire.mu.Unlock()
			http.Error(w, "fixture body failure", http.StatusInternalServerError)
			return
		}
		reply, err := json.Marshal(map[string]any{
			"Number Deleted":   1,
			"Number Not Found": 0,
			"Response Status":  "400 Bad Request",
			"Response Body":    "",
			"Errors":           [][]string{{blamedLine, "500 Internal Error"}},
		})
		if err != nil {
			t.Error(err)
		}
		wire.mu.Lock()
		wire.substituted = string(reply)
		wire.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", fmt.Sprint(len(reply)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(reply)
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
		return wire
	}
	t.Fatal("primary bookmark not found")
	return nil
}

// installIssuedTrigger makes the job's private index refuse every issued
// record, so the next request cannot be recorded before it is sent.
func installIssuedTrigger(ctx context.Context, cache string) (path string, err error) {
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
	if _, err := connection.ExecContext(ctx, `CREATE TRIGGER refuse_issued_record BEFORE INSERT ON seen
		WHEN NEW.kind = 3 BEGIN SELECT RAISE(ABORT,'owned issued record refusal'); END`); err != nil {
		return "", fmt.Errorf("install owned issued trigger: %w", err)
	}
	return paths[0], nil
}

// bulkPathLine spells a bulk request line as the client does: each path
// segment escaped on its own, so slashes inside the object name survive.
func bulkPathLine(container, name string) string {
	segments := strings.Split(name, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return "/" + url.PathEscape(container) + "/" + strings.Join(segments, "/") + "\n"
}

func deleteSweepAuthority(t *testing.T, h *Harness, c sweepAuthorityCase) {
	t.Helper()
	// The private accounting index lives under the user cache directory;
	// this owned fixture keeps it away from any real cache.
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if resolved, err := os.UserCacheDir(); err != nil || resolved != cache {
		t.Fatalf("cache is not the owned fixture: %q %v", resolved, err)
	}
	container := h.Container(h.Primary, "sweep-authority")
	other := h.Container(h.Primary, "sweep-authority-other")
	secondary := h.Container(h.Secondary, "sweep-authority-other")
	names := []string{"scope/00 first", "scope/01 blamed"}
	for _, name := range names {
		h.Seed(h.Primary, container, name, []byte("planned "+name), nil)
	}
	h.Seed(h.Primary, container, "outside/keep", []byte("outside the prefix"), nil)
	h.Seed(h.Primary, other, names[1], []byte("other container sentinel"), nil)
	h.Seed(h.Secondary, secondary, names[1], []byte("secondary sentinel"), nil)
	replacementBody := "replacement written after the first request"
	lateName := "scope/02 late"
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	wire := sweepFront(t, h, cache, container, names[1], replacementBody, lateName, c)
	receipt := h.StartDelete(t.Context(), app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "scope/", Mode: app.ModePrefix, Confirm: container,
	})
	if receipt.Err() != nil {
		t.Fatal(receipt.Err())
	}
	job := h.AwaitJob(receipt.JobID())
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	wire.mu.Lock()
	bodies := append([]string(nil), wire.bulkBodies...)
	firstReply, substituted, replacedAt, problem := wire.firstReply, wire.substituted, wire.replacedAt, wire.problem
	wire.mu.Unlock()
	if problem != nil {
		t.Fatalf("front problem: %v", problem)
	}
	blamedLine := bulkPathLine(container, names[1])
	issuedBlamed := 0
	for _, body := range bodies {
		if strings.Contains(body, blamedLine) {
			issuedBlamed++
		}
	}
	survivor, present := afterA.Containers[container].Objects[names[1]]
	observation := map[string]any{"case": c.name, "state": job.State, "error": job.Error, "counters": job.Counters,
		"bulkRequests": len(bodies), "bulkBodies": bodies, "firstActualReply": firstReply, "substitutedReply": substituted,
		"replacementInstalledAt": replacedAt, "blamedNameIssued": issuedBlamed, "blamedNamePresentAfter": present,
		"survivorSize": survivor.Size, "survivorDigest": survivor.Digest, "replacementSize": len(replacementBody)}
	encoded, _ := json.Marshal(observation)
	t.Logf("sweep authority observation: %s", encoded)

	// Nothing outside the prefix, the other container or the other account changed.
	if _, ok := afterA.Containers[container].Objects["outside/keep"]; !ok {
		t.Errorf("object outside the prefix was deleted")
	}
	allowed := []string{}
	for _, name := range names {
		allowed = append(allowed, container+"/"+name)
	}
	if c.late {
		allowed = append(allowed, container+"/"+lateName)
	}
	h.AssertNoCollateral(beforeA, afterA, allowed...)
	h.AssertNoCollateral(beforeB, afterB)

	if c.refuseIssued {
		wire.mu.Lock()
		installed := wire.triggerInstalled
		wire.mu.Unlock()
		if !installed {
			t.Fatal("the issued-record trigger was never installed, so this case proves nothing")
		}
		// No issued record, no request: both planned objects survive and
		// the job stops with incomplete accounting before any bulk request.
		if len(bodies) != 0 {
			t.Errorf("a request was sent without its issued record: %q", bodies)
		}
		for _, name := range names {
			if _, ok := afterA.Containers[container].Objects[name]; !ok {
				t.Errorf("planned object %q was deleted without an issued record", name)
			}
		}
		if job.State == "complete" || !job.Counters.DeletionAccountingIncomplete || job.Counters.ObjectsCopied != 0 {
			t.Errorf("refused issued record did not stop the job truthfully: %+v", job)
		}
		return
	}
	// The first request carried both planned names, as one batch.
	if len(bodies) == 0 || !strings.Contains(bodies[0], blamedLine) || !strings.Contains(bodies[0], bulkPathLine(container, names[0])) {
		t.Fatalf("first bulk request did not carry both planned names: %q", bodies)
	}
	if issuedBlamed != 1 {
		t.Errorf("the blamed name was requested %d times, want exactly once: %q", issuedBlamed, bodies)
	}
	if c.name == "healthy_control" {
		if job.State != "complete" || job.Error != "" || len(bodies) != 1 || present {
			t.Errorf("healthy control differs: state=%s error=%q bulk=%d present=%v", job.State, job.Error, len(bodies), present)
		}
		return
	}
	// A partial reply leaves the blamed name unresolved and the job partial.
	if job.State != "partial" || !strings.Contains(job.Error, "unresolved effect") {
		t.Errorf("partial reply did not end partial with the unresolved facet: state=%s error=%q", job.State, job.Error)
	}
	if c.replacement {
		// The replacement survives with exact bytes, and the job says the
		// name was listed again but not requested again.
		if !present || survivor.Size != int64(len(replacementBody)) {
			t.Errorf("replacement at the blamed name did not survive: present=%v size=%d want=%d", present, survivor.Size, len(replacementBody))
		}
		if !strings.Contains(job.Error, "were not requested again (first: "+names[1]+")") {
			t.Errorf("job did not report the name it did not request again: %q", job.Error)
		}
	} else if strings.Contains(job.Error, "not requested again") {
		t.Errorf("no name was listed again, yet the job reports one: %q", job.Error)
	}
	wantBulk := 1
	if c.late {
		// The late name was never requested before, so the second pass
		// requests it and only it; this is the open membership hazard.
		wantBulk = 2
		if len(bodies) == 2 && strings.TrimSpace(bodies[1]) != strings.TrimSpace(bulkPathLine(container, lateName)) {
			t.Errorf("second request did not carry exactly the late name: %q", bodies[1])
		}
		if _, ok := afterA.Containers[container].Objects[lateName]; ok {
			t.Errorf("the late name survived although it was never requested before this pass")
		}
	}
	if len(bodies) != wantBulk {
		t.Errorf("bulk requests = %d, want %d: %q", len(bodies), wantBulk, bodies)
	}
}
