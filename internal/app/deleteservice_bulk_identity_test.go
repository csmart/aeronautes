package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// bulkIdentityServer answers the bulk-delete POST from a canned reply
// and forwards everything else to the mock. It exists so this slice can
// drive a reply the mock cannot produce: one whose counts claim
// successes while a requested name gets no disposition at all.
type bulkIdentityServer struct {
	inner http.Handler
	reply map[string]any
	// forward replays the request into the service first, so the
	// deletions really happen, before the same substituted reply is
	// returned. That is the fault boundary: two runs differing only in
	// whether the effect was applied, driven by identical bytes.
	forward bool
	calls   int
	sent    []byte
	// beforeList runs once, on the server goroutine, before the first
	// prefix listing is answered. It lets a test act on facts the job
	// only publishes after it starts, without racing the job.
	beforeList func()
	listOnce   sync.Once
}

func (b *bulkIdentityServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if b.beforeList != nil && r.Method == http.MethodGet && r.URL.Query().Has("prefix") {
		b.listOnce.Do(b.beforeList)
	}
	if r.Method == http.MethodPost && r.URL.Query().Has("bulk-delete") {
		body, _ := io.ReadAll(r.Body)
		if b.forward {
			replay := httptest.NewRequest(http.MethodPost, r.URL.String(), bytes.NewReader(body))
			replay.Header = r.Header.Clone()
			b.inner.ServeHTTP(httptest.NewRecorder(), replay)
		}
		b.calls++
		payload, _ := json.Marshal(b.reply)
		b.sent = payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
		return
	}
	b.inner.ServeHTTP(w, r)
}

// bulkIdentityHarness builds the same backend the shared harness does,
// but in front of a server this slice controls. It is a local
// constructor on purpose: the shared one owns its own server and other
// slices are editing that file.
func bulkIdentityHarness(t *testing.T, reply map[string]any) (*harness, *bulkIdentityServer) {
	t.Helper()
	return bulkIdentityHarnessForwarding(t, false, reply)
}

func bulkIdentityHarnessForwarding(t *testing.T, forward bool, reply map[string]any) (*harness, *bulkIdentityServer) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	return bulkIdentityHarnessOver(t, mock, mock, forward, reply)
}

// bulkIdentityHarnessOver puts the public service in front of any
// upstream, so the same job code can be driven against the in-process
// mock and against the owned Swift fixture with one substituting layer.
func bulkIdentityHarnessOver(t *testing.T, upstream http.Handler, mock *swifttest.Server, forward bool, reply map[string]any) (*harness, *bulkIdentityServer) {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	crafted := &bulkIdentityServer{inner: upstream, reply: reply, forward: forward}
	srv := httptest.NewServer(crafted)
	t.Cleanup(srv.Close)

	store := bookmarks.NewStore(d, secrets.NewSessionStore())
	connections := NewConnections(store)
	ev := evidence.NewStore(d)
	auditStore := audit.NewStore(d)
	jobStore := jobs.NewStore(d)
	return &harness{
		bookmarks: NewBookmarkService(store, connections, jobStore),
		browse:    NewBrowseService(connections, ev),
		inspector: NewInspectorService(connections, ev),
		mutate:    NewMutateService(connections, auditStore, ev),
		auditSvc:  NewAuditService(auditStore),
		transfer:  transferService(connections, jobStore, auditStore, ev),
		jobsSvc:   NewJobService(jobStore),
		evidence:  ev,
		db:        d,
		mock:      mock,
		authURL:   srv.URL + "/auth/v1.0",
	}, crafted
}

// terminalAuditFor requires the exact terminal event this job must
// leave, identified by the job rather than by any delete-prefixed
// operation, so a run that emits only its start event fails.
func terminalAuditFor(t *testing.T, h *harness, jobID, operation, resource, result string) {
	t.Helper()
	// finish() makes the terminal state visible before it writes the
	// audit record, so a job that has just been observed as terminal may
	// not have recorded yet. Wait for the record instead of sampling
	// once; a run that never writes one still fails.
	summary := "job " + jobID
	var events []AuditEventDTO
	var found *AuditEventDTO
	deadline := time.Now().Add(10 * time.Second)
	for found == nil && time.Now().Before(deadline) {
		var err error
		events, err = h.auditSvc.RecentFor(context.Background(), h.bookmarkID(), 200)
		if err != nil {
			t.Fatal(err)
		}
		for i := range events {
			if events[i].Summary == summary && events[i].Operation == operation {
				found = &events[i]
				break
			}
		}
		if found == nil {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if found == nil {
		seen := []string{}
		for _, e := range events {
			if strings.HasPrefix(e.Operation, "delete.") {
				seen = append(seen, e.Operation+" "+e.Summary+" "+e.Result)
			}
		}
		t.Fatalf("no terminal audit event %q for %q; delete events seen: %v", operation, summary, seen)
	}
	if found.Result != result {
		t.Errorf("terminal audit result = %q, want %q", found.Result, result)
	}
	if found.Resource != resource {
		t.Errorf("terminal audit resource = %q, want %q", found.Resource, resource)
	}
}

// retainedListPath extracts the artifact the run advertised, and fails
// when the run offered none.
func retainedListPath(t *testing.T, summary string) string {
	t.Helper()
	marker := "full list: "
	idx := strings.Index(summary, marker)
	if idx < 0 {
		t.Fatalf("the run advertised no retained list: %q", summary)
	}
	path := strings.TrimSpace(summary[idx+len(marker):])
	if cut := strings.Index(path, ";"); cut >= 0 {
		path = strings.TrimSpace(path[:cut])
	}
	return path
}

// retainedRecords reads back the complete advertised list as identity
// to details, keeping every repeat so multiplicity is testable.
func retainedRecords(t *testing.T, summary string) map[string][]string {
	t.Helper()
	body, err := os.ReadFile(retainedListPath(t, summary))
	if err != nil {
		t.Fatalf("the advertised list cannot be read: %v", err)
	}
	records := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var record struct {
			Name       string `json:"name"`
			Unresolved string `json:"unresolved"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("a retained record is not readable: %v", err)
		}
		if record.Name == "" {
			t.Error("a retained record names nothing")
		}
		records[record.Name] = append(records[record.Name], record.Unresolved)
	}
	return records
}

// assertRetainedRecords is the content control. Each identity must
// appear exactly once, each blamed name must carry the reply status the
// service returned for it, every other name must carry the shared
// not-dependable reason, and every detail must say the effect is
// unknown. A run that writes one constant sentence for every name, or
// that claims an effect it cannot know, fails all three requirements.
func assertRetainedRecords(t *testing.T, records map[string][]string, want map[string]bool, blamed map[string]string) {
	t.Helper()
	if len(records) != len(want) {
		t.Errorf("the retained list holds %d identities, want every one of the %d requested", len(records), len(want))
	}
	distinct := map[string]bool{}
	for name := range want {
		details, ok := records[name]
		if !ok {
			t.Errorf("%q is missing from the retained list", name)
			continue
		}
		if len(details) != 1 {
			t.Errorf("%q is retained %d times, want exactly once", name, len(details))
			continue
		}
		detail := details[0]
		distinct[detail] = true
		if status, isBlamed := blamed[name]; isBlamed {
			// A blamed name must carry the exact status its own row
			// returned, and must still refuse to state an effect.
			if !strings.Contains(detail, status) {
				t.Errorf("the record for %q does not carry its reply status %q: %q", name, status, detail)
			}
			if !strings.Contains(detail, "no observation establishes") {
				t.Errorf("the record for %q does not refuse to state an effect: %q", name, detail)
			}
		} else if !strings.Contains(detail, "not dependable") {
			// Every other name must be explained by what was wrong with
			// the reply, which is the only thing this run learned.
			t.Errorf("the record for %q does not give the reply reason: %q", name, detail)
		}
	}
	if len(blamed) > 0 && len(distinct) < 2 {
		t.Errorf("every identity carries the same detail, so no detail is drawn from its own reply row: %v", distinct)
	}
}

// observerSession authenticates a client against the upstream directly,
// so its observations never pass through the substituting layer.
func observerSession(t *testing.T, upstream http.Handler) *swiftcontrol.Session {
	t.Helper()
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	return authenticatedSession(t, srv.URL+"/auth/v1.0")
}

func authenticatedSession(t *testing.T, authURL string) *swiftcontrol.Session {
	t.Helper()
	session, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: authURL, Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return session
}

// saioAuthURL is the owned Swift fixture, or an empty string when it is
// not configured for this run.
func saioAuthURL() string { return os.Getenv("AERONAUTES_SAIO_AUTH_URL") }

// witnessAbsent requires an authoritative 404. A transport,
// authentication or server failure is a broken observation, not a
// witnessed deletion, and must never be read as absence.
func witnessAbsent(t *testing.T, observer *swiftcontrol.Session, container, name string) {
	t.Helper()
	_, err := observer.ObjectDetail(context.Background(), container, name)
	switch {
	case err == nil:
		t.Errorf("%s/%s is still present, so nothing was witnessed as deleted", container, name)
	case !swiftcontrol.IsNotFound(err):
		t.Fatalf("%s/%s could not be observed, so absence is not established: %v", container, name, err)
	}
}

// witnessContent requires the object to be present with exactly the
// bytes it was given, which is what proves a survivor survived intact
// rather than merely still having a name.
func witnessContent(t *testing.T, observer *swiftcontrol.Session, container, name, want string) {
	t.Helper()
	body, _, err := observer.OpenObject(context.Background(), container, name)
	if err != nil {
		t.Fatalf("%s/%s is not present: %v", container, name, err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("%s/%s could not be read: %v", container, name, err)
	}
	if string(got) != want {
		t.Errorf("%s/%s content = %q, want %q", container, name, got, want)
	}
}

// TestDeleteJobDoesNotPublishClaimedDeletionsAsVerified is the
// service-level assertion for the mixed-count reply. The reply claims
// three deletions it does not identify and names one failure for five
// requested names, so it accounts for four and establishes an effect
// for none of them. The job must not publish those claimed deletions as
// verified beside the same names as failures, must not complete, and
// must say how many names are unresolved.
func TestDeleteJobDoesNotPublishClaimedDeletionsAsVerified(t *testing.T) {
	seeded := []string{}
	for i := 0; i < 5; i++ {
		seeded = append(seeded, fmt.Sprintf("scope/obj-%d", i))
	}
	h, crafted := bulkIdentityHarness(t, map[string]any{
		"Number Deleted": 3, "Number Not Found": 0,
		"Response Status": "200 OK", "Response Body": "",
		"Errors": [][2]string{{"/media/scope/obj-0", "500 Internal Server Error"}},
	})
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	for _, name := range seeded {
		seedObject(h.mock, "media", name, "data", nil)
	}
	seedObject(h.mock, "media", "kept.txt", "kept", nil)

	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "media",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)

	// The exact state matters: an unresolved effect is not zero effect,
	// so the run is partial rather than failed or cancelled.
	if job.State != "partial" {
		t.Errorf("state = %q, want partial while names are unresolved", job.State)
	}
	if job.Counters.ObjectsVerified != 0 {
		t.Errorf("claimed deletions were published as verified: ObjectsVerified = %d", job.Counters.ObjectsVerified)
	}
	if job.Counters.ObjectsCopied != 0 {
		t.Errorf("claimed deletions were published as processed: ObjectsCopied = %d", job.Counters.ObjectsCopied)
	}
	if job.Counters.ObjectsFailed != 1 {
		t.Errorf("ObjectsFailed = %d, want the one name the reply validly blamed", job.Counters.ObjectsFailed)
	}
	if !strings.Contains(job.Error, "unresolved effect") {
		t.Errorf("the terminal summary does not report unresolved names: %q", job.Error)
	}
	if !strings.Contains(job.Error, "5 names") {
		t.Errorf("the terminal summary does not count every requested name as unresolved: %q", job.Error)
	}
	if crafted.calls == 0 {
		t.Error("the crafted reply was never used")
	}
	// Nothing was forwarded, so every seeded object and the neighbour
	// must still be there: the run claimed no destruction it cannot
	// establish, and caused none either.
	session := h.transfer.connections.sessions[bookmark].session
	for _, name := range append(seeded, "kept.txt") {
		if _, err := session.ObjectDetail(ctx, "media", name); err != nil {
			t.Errorf("%q is missing after a run that deleted nothing: %v", name, err)
		}
	}
}

// TestDeleteJobDisclosesAnUnretainableUnresolvedSet drives the case
// where the retention path cannot be opened at all. The run must still
// report the exact count, and must say that the complete list was not
// retained, so nobody reads the ledger as complete when it is not.
func TestDeleteJobDisclosesAnUnretainableUnresolvedSet(t *testing.T) {
	// The index must remain healthy: this case promises exact counts
	// despite losing only the normal retained unresolved list.
	cache := deletionAccountingCache(t)
	if err := os.MkdirAll(filepath.Join(cache, "aeronautes"), 0o700); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(cache, "aeronautes", "unresolved")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	h, _ := bulkIdentityHarness(t, map[string]any{
		"Number Deleted": 1, "Number Not Found": 0,
		"Response Status": "200 OK", "Response Body": "",
		"Errors": [][2]string{{"/media/scope/obj-0", "500 Internal Server Error"}},
	})
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	for i := 0; i < 3; i++ {
		seedObject(h.mock, "media", fmt.Sprintf("scope/obj-%d", i), "data", nil)
	}

	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "media",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)

	if job.State != "partial" {
		t.Errorf("state = %q, want partial while names are unresolved", job.State)
	}
	if !strings.Contains(job.Error, "3 names have an unresolved effect") {
		t.Errorf("the exact count did not survive the retention failure: %q", job.Error)
	}
	if !strings.Contains(job.Error, "could not be retained") {
		t.Errorf("a lost unresolved list was not disclosed: %q", job.Error)
	}
	if strings.Contains(job.Error, "full list:") {
		t.Errorf("a full list was offered although none was retained: %q", job.Error)
	}
}

// TestUnresolvedRetentionReportsBufferedLoss drives the case the open
// check cannot reach: the file opens, records are accepted into the
// buffer, and the write fails only at the flush. Every buffered record
// is then lost, so the recorder must not offer the artifact as a full
// list, must keep the exact identity count, and must not claim a number
// of lost names it cannot establish.
func TestUnresolvedRetentionReportsBufferedLoss(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("/dev/full is not available to force a write failure")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	jobID := "buffered-loss"
	dir := filepath.Join(cache, "aeronautes", "unresolved")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The retention opens this exact path; a symlink to a device that
	// accepts an open and refuses every byte reproduces a full disk.
	if err := os.Symlink("/dev/full", filepath.Join(dir, jobID+".jsonl")); err != nil {
		t.Skipf("cannot link to /dev/full here: %v", err)
	}

	record, closeRetention := unresolvedRetention(jobID)
	for i := 0; i < 3; i++ {
		record(fmt.Sprintf("name-%d", i), "the reply did not establish an effect")
	}
	path, count, problem := closeRetention()

	if count != 3 {
		t.Errorf("count = %d, want the exact number of identities recorded", count)
	}
	if path != "" {
		t.Errorf("an unusable artifact was offered as a retained list: %q", path)
	}
	if problem == "" {
		t.Fatal("a lost list was not disclosed")
	}
	if !strings.Contains(problem, "incomplete") {
		t.Errorf("the problem does not say the list is incomplete: %q", problem)
	}
	if strings.Contains(problem, "3 unresolved names could not") {
		t.Errorf("the problem claims a lost-name count it cannot establish: %q", problem)
	}
	if !strings.Contains(problem, "not established") {
		t.Errorf("the problem does not admit the lost count is unknown: %q", problem)
	}
}

// TestDeleteJobRetainsEveryUnresolvedIdentity is the retained-evidence
// proof. It drives a reply that claims a few deletions and names one
// failure for a request far larger than the display cap, then reads the
// retained list back and compares the complete identity set, so a run
// that advertises a list it never wrote cannot pass.
func TestDeleteJobRetainsEveryUnresolvedIdentity(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	const seeded = 250 // deliberately beyond the 200-record display cap
	h, crafted := bulkIdentityHarness(t, map[string]any{
		"Number Deleted": 3, "Number Not Found": 0,
		"Response Status": "200 OK", "Response Body": "",
		"Errors": [][2]string{{"/media/scope/obj-0000", "500 Internal Server Error"}},
	})
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	want := map[string]bool{}
	for i := 0; i < seeded; i++ {
		name := fmt.Sprintf("scope/obj-%04d", i)
		seedObject(h.mock, "media", name, "data", nil)
		want[name] = true
	}

	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "media",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)

	if job.State != "partial" {
		t.Errorf("state = %q, want partial", job.State)
	}
	if job.Counters.ObjectsVerified != 0 || job.Counters.ObjectsCopied != 0 {
		t.Errorf("an untrusted aggregate reached the counters: %+v", job.Counters)
	}
	if job.Counters.ObjectsFailed != 1 {
		t.Errorf("ObjectsFailed = %d, want the single validly named row", job.Counters.ObjectsFailed)
	}
	if !strings.Contains(job.Error, fmt.Sprintf("%d names have an unresolved effect", seeded)) {
		t.Errorf("the summary does not count every requested name: %q", job.Error)
	}

	// The complete retained set, read back from where the run said it is:
	// every identity once, each detail drawn from its own reply row, and
	// none of them claiming an effect on the store.
	got := retainedRecords(t, job.Error)
	assertRetainedRecords(t, got, want, map[string]string{"scope/obj-0000": "500 Internal Server Error"})
	// The display sample is bounded while the retained list is not.
	if len(got) <= 200 {
		t.Errorf("this case must exceed the display cap to be worth running, got %d", len(got))
	}

	// The exact terminal audit fact for this job, not merely some delete
	// event: a partial destructive run on this scope.
	terminalAuditFor(t, h, jobID, "delete.prefix.partial", "media/scope/", "verify_mismatch")
	if crafted.calls == 0 {
		t.Error("the crafted reply was never used")
	}
}

// TestDeleteJobEffectSubstitutionThroughTheService runs the agreed
// fault boundary at the public service: the same substituted reply,
// once with the deletions actually applied and once with them
// suppressed. Every public fact must be identical in both, because the
// reply establishes nothing about the effect, while an observer that
// never passes through the substituting layer shows the two worlds
// really did differ.
func TestDeleteJobEffectSubstitutionThroughTheService(t *testing.T) {
	runEffectSubstitutionPair(t, nil)
}

// TestDeleteJobEffectSubstitutionThroughTheServiceRealSwift runs the
// same public-path boundary against the owned Swift fixture, so the
// pair is not an artefact of the in-process mock.
func TestDeleteJobEffectSubstitutionThroughTheServiceRealSwift(t *testing.T) {
	if saioAuthURL() == "" {
		t.Skip("the owned Swift fixture is not configured")
	}
	runEffectSubstitutionPair(t, &realSwiftUpstream{})
}

// realSwiftUpstream marks the pair as running against the owned
// service rather than the in-process mock.
type realSwiftUpstream struct{}

type effectOutcome struct {
	state    string
	counters string
	summary  string
	records  map[string][]string
	audit    string
}

func runEffectSubstitutionPair(t *testing.T, real *realSwiftUpstream) {
	t.Helper()
	var runs []effectOutcome
	var replies [][]byte

	container := "media"
	prefix := "scope/"
	if real != nil {
		container = fmt.Sprintf("aeronautes-effect-%d", time.Now().UnixNano())
	}
	seeded := []string{}
	for i := 0; i < 5; i++ {
		seeded = append(seeded, fmt.Sprintf("%sobj-%d", prefix, i))
	}
	neighbour := "kept.txt"
	blamed := seeded[0]
	reply := map[string]any{
		"Number Deleted": 3, "Number Not Found": 0,
		"Response Status": "200 OK", "Response Body": "",
		"Errors": [][2]string{{"/" + container + "/" + blamed, "500 Internal Server Error"}},
	}

	// The owned service keeps one container and one observer across both
	// runs; the mock gets a fresh in-process provider each time.
	var owned *swiftcontrol.Session
	if real != nil {
		owned = authenticatedSession(t, saioAuthURL())
		if _, err := owned.CreateContainer(context.Background(), swiftcontrol.ContainerCreateRequest{Name: container}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			background := context.Background()
			for _, name := range append(append([]string{}, seeded...), neighbour) {
				_ = owned.DeleteObject(background, container, name)
			}
			if err := owned.DeleteContainer(background, container); err != nil {
				t.Errorf("cleanup left %q behind: %v", container, err)
			}
		})
	}

	for _, forward := range []bool{false, true} {
		cache := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", cache)

		var h *harness
		var crafted *bulkIdentityServer
		var observer *swiftcontrol.Session
		if real != nil {
			base := saioAuthURL()
			base = base[:strings.Index(base, "/auth/")]
			upstream := &swiftReverseProxy{target: base, client: &http.Client{}}
			h, crafted = bulkIdentityHarnessOver(t, upstream, swifttest.New("test:tester", "testing"), forward, reply)
			// The storage URL the service is handed must point back at
			// the substituting layer, not past it, or the injected reply
			// never reaches the job.
			upstream.self = strings.TrimSuffix(h.authURL, "/auth/v1.0")
			observer = owned
			for _, name := range append(append([]string{}, seeded...), neighbour) {
				body := "data"
				if name == neighbour {
					body = "kept"
				}
				if _, err := observer.PutObject(context.Background(), container, name, strings.NewReader(body), "application/octet-stream", nil); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			h, crafted = bulkIdentityHarnessForwarding(t, forward, reply)
			observer = observerSession(t, h.mock)
			h.mock.AddContainer(container, "standard")
			for _, name := range seeded {
				seedObject(h.mock, container, name, "data", nil)
			}
			seedObject(h.mock, container, neighbour, "kept", nil)
		}
		h.connect(t)
		bookmark := h.transfer.connections.ActiveBookmarkID()
		service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
		ctx := context.Background()

		jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
			BookmarkID: bookmark, Container: container, Prefix: prefix, Mode: ModePrefix, Confirm: container,
		})
		if err != nil {
			t.Fatal(err)
		}
		job := awaitTerminal(t, h, jobID)

		// The complete retained records, compared in both worlds.
		want := map[string]bool{}
		for _, name := range seeded {
			want[name] = true
		}
		records := retainedRecords(t, job.Error)
		assertRetainedRecords(t, records, want, map[string]string{blamed: "500 Internal Server Error"})
		terminalAuditFor(t, h, jobID, "delete.prefix.partial", container+"/"+prefix, "verify_mismatch")

		// Authoritative witnesses, taken outside the fault boundary.
		if forward {
			for _, name := range seeded {
				witnessAbsent(t, observer, container, name)
			}
		} else {
			for _, name := range seeded {
				witnessContent(t, observer, container, name, "data")
			}
		}
		// Collateral: the neighbour outside the prefix keeps its bytes in
		// both worlds.
		witnessContent(t, observer, container, neighbour, "kept")

		// The summary's retained-list path differs per run by job id, so
		// compare the part that states the facts.
		summary := job.Error
		if idx := strings.Index(summary, "; full list: "); idx >= 0 {
			summary = summary[:idx]
		}
		runs = append(runs, effectOutcome{
			state:    job.State,
			counters: fmt.Sprintf("copied=%d verified=%d failed=%d", job.Counters.ObjectsCopied, job.Counters.ObjectsVerified, job.Counters.ObjectsFailed),
			summary:  summary,
			records:  records,
			audit:    "delete.prefix.partial/verify_mismatch",
		})
		replies = append(replies, crafted.sent)
	}

	if !bytes.Equal(replies[0], replies[1]) {
		t.Fatal("the two runs must be driven by identical reply bytes")
	}
	if runs[0].state != runs[1].state || runs[0].counters != runs[1].counters || runs[0].summary != runs[1].summary {
		t.Errorf("the same reply produced different public facts:\n suppressed %s | %s | %s\n applied    %s | %s | %s",
			runs[0].state, runs[0].counters, runs[0].summary, runs[1].state, runs[1].counters, runs[1].summary)
	}
	if runs[0].state != "partial" {
		t.Errorf("state = %q, want partial", runs[0].state)
	}
	if runs[0].counters != "copied=0 verified=0 failed=1" {
		t.Errorf("counters = %q, want no credited deletions and the one validly named row", runs[0].counters)
	}
	// The retained ledgers agree name for name and detail for detail
	// although the stores ended in opposite states.
	if !reflect.DeepEqual(runs[0].records, runs[1].records) {
		t.Errorf("the retained records differ between the two worlds:\n suppressed %v\n applied    %v", runs[0].records, runs[1].records)
	}
}

// swiftReverseProxy forwards every request to the owned service and
// rewrites the storage URL so the client keeps talking to this process.
type swiftReverseProxy struct {
	target string
	self   string
	client *http.Client
}

func (p *swiftReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	out, err := http.NewRequest(r.Method, p.target+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	out.Header = r.Header.Clone()
	resp, err := p.client.Do(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	for key, values := range resp.Header {
		for _, v := range values {
			if strings.EqualFold(key, "X-Storage-Url") {
				v = strings.Replace(v, p.target, p.self, 1)
			}
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(payload)
}

// TestDeleteJobPublishesBufferedRetentionLoss carries the buffered
// write failure through a public job. The retention file opens, records
// are accepted, and every byte is refused at the flush, so the job must
// keep the exact identity count, must not offer the artifact as a full
// list, and must say the list is incomplete without claiming how many
// identities were lost.
func TestDeleteJobPublishesBufferedRetentionLoss(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("/dev/full is not available to force a write failure")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	h, crafted := bulkIdentityHarness(t, map[string]any{
		"Number Deleted": 1, "Number Not Found": 0,
		"Response Status": "200 OK", "Response Body": "",
		"Errors": [][2]string{{"/media/scope/obj-0", "500 Internal Server Error"}},
	})
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	for i := 0; i < 3; i++ {
		seedObject(h.mock, "media", fmt.Sprintf("scope/obj-%d", i), "data", nil)
	}

	// The job id is only known once StartDelete returns, but the job is
	// already running by then and may reach its first listing before this
	// goroutine has done anything with the id. The hook therefore blocks
	// until the id is delivered over a channel, and only then links the
	// exact retention path to a device that accepts an open and refuses
	// every byte, which reproduces a full disk before the job opens the
	// file and without any assumption about who runs first.
	idReady := make(chan string, 1)
	linked := make(chan error, 1)
	crafted.beforeList = func() {
		jobID, ok := <-idReady
		if !ok {
			linked <- errors.New("no job id reached the listing hook")
			return
		}
		dir := filepath.Join(cache, "aeronautes", "unresolved")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			linked <- err
			return
		}
		linked <- os.Symlink("/dev/full", filepath.Join(dir, jobID+".jsonl"))
	}
	// Closing the channel on every exit keeps a blocked hook from holding
	// the server open if StartDelete fails before an id exists; a value
	// already sent is still received after the close.
	t.Cleanup(func() { close(idReady) })

	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "media",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The delayed-start control: the caller is deliberately descheduled
	// after StartDelete returns and before it delivers the id, which is
	// the ordering a shared variable got wrong. The outcome must not
	// depend on how long this takes.
	time.Sleep(250 * time.Millisecond)
	idReady <- jobID
	job := awaitTerminal(t, h, jobID)
	select {
	case err := <-linked:
		if err != nil {
			t.Skipf("cannot link to /dev/full here: %v", err)
		}
	default:
		t.Fatal("the listing hook never ran, so no retention failure was forced")
	}

	if job.State != "partial" {
		t.Errorf("state = %q, want partial while names are unresolved", job.State)
	}
	if !strings.Contains(job.Error, "3 names have an unresolved effect") {
		t.Errorf("the exact count did not survive the retention failure: %q", job.Error)
	}
	if strings.Contains(job.Error, "full list:") {
		t.Errorf("an unusable artifact was offered as a full list: %q", job.Error)
	}
	if !strings.Contains(job.Error, "incomplete") {
		t.Errorf("the job does not say the retained list is incomplete: %q", job.Error)
	}
	if !strings.Contains(job.Error, "not established") {
		t.Errorf("the job does not admit the lost count is unknown: %q", job.Error)
	}
	if strings.Contains(job.Error, "3 unresolved names could not") {
		t.Errorf("the job claims a lost-name count it cannot establish: %q", job.Error)
	}
	terminalAuditFor(t, h, jobID, "delete.prefix.partial", "media/scope/", "verify_mismatch")
}
