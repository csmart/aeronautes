package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
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
}

func (b *bulkIdentityServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	crafted := &bulkIdentityServer{inner: mock, reply: reply, forward: forward}
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
	// A cache location that is a file, not a directory, so the retention
	// path cannot be created.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", blocked)

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

	// The complete retained set, read back from where the run said it is.
	marker := "full list: "
	idx := strings.Index(job.Error, marker)
	if idx < 0 {
		t.Fatalf("the run advertised no retained list: %q", job.Error)
	}
	path := strings.TrimSpace(job.Error[idx+len(marker):])
	if cut := strings.Index(path, ";"); cut >= 0 {
		path = strings.TrimSpace(path[:cut])
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the advertised list cannot be read: %v", err)
	}
	got := map[string]bool{}
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
		if record.Unresolved == "" {
			t.Errorf("the record for %q carries no reason", record.Name)
		}
		got[record.Name] = true
	}
	if len(got) != seeded {
		t.Errorf("the retained list holds %d identities, want every one of the %d requested", len(got), seeded)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("%q is missing from the retained list", name)
		}
	}
	// The display sample is bounded while the retained list is not.
	if len(got) <= 200 {
		t.Errorf("this case must exceed the display cap to be worth running, got %d", len(got))
	}

	// The audit trail records the run as a destructive operation that
	// did not complete.
	events, err := h.auditSvc.Recent(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var sawDelete bool
	for _, e := range events {
		if strings.HasPrefix(e.Operation, "delete.") {
			sawDelete = true
			if e.Result == "ok" {
				t.Errorf("the audit records a clean result for a run with unresolved names: %+v", e)
			}
		}
	}
	if !sawDelete {
		t.Error("the run left no delete audit event")
	}
	if crafted.calls == 0 {
		t.Error("the crafted reply was never used")
	}
}

// TestDeleteJobEffectSubstitutionThroughTheService runs the agreed
// fault boundary at the public service: the same substituted reply,
// once with the deletions actually applied and once with them
// suppressed. The job's public facts must be identical in both, because
// the reply establishes nothing about the effect, while authoritative
// witnesses show the two worlds really did differ.
func TestDeleteJobEffectSubstitutionThroughTheService(t *testing.T) {
	type outcome struct {
		state    string
		counters string
		summary  string
		present  []string
	}
	var runs []outcome
	var replies [][]byte

	for _, forward := range []bool{false, true} {
		cache := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", cache)
		h, crafted := bulkIdentityHarnessForwarding(t, forward, map[string]any{
			"Number Deleted": 3, "Number Not Found": 0,
			"Response Status": "200 OK", "Response Body": "",
			"Errors": [][2]string{{"/media/scope/obj-0", "500 Internal Server Error"}},
		})
		h.connect(t)
		bookmark := h.transfer.connections.ActiveBookmarkID()
		service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
		ctx := context.Background()
		h.mock.AddContainer("media", "standard")
		seeded := []string{}
		for i := 0; i < 5; i++ {
			name := fmt.Sprintf("scope/obj-%d", i)
			seedObject(h.mock, "media", name, "data", nil)
			seeded = append(seeded, name)
		}
		seedObject(h.mock, "media", "kept.txt", "kept", nil)

		jobID, err := service.StartDelete(ctx, DeleteRequestDTO{
			BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "media",
		})
		if err != nil {
			t.Fatal(err)
		}
		job := awaitTerminal(t, h, jobID)

		// Authoritative witnesses, not a listing.
		session := h.transfer.connections.sessions[bookmark].session
		present := []string{}
		for _, name := range append(append([]string{}, seeded...), "kept.txt") {
			if _, err := session.ObjectDetail(ctx, "media", name); err == nil {
				present = append(present, name)
			}
		}
		// The summary's retained-list path differs per run by job id, so
		// compare the part that states the facts.
		summary := job.Error
		if idx := strings.Index(summary, "; full list: "); idx >= 0 {
			summary = summary[:idx]
		}
		runs = append(runs, outcome{
			state:    job.State,
			counters: fmt.Sprintf("copied=%d verified=%d failed=%d", job.Counters.ObjectsCopied, job.Counters.ObjectsVerified, job.Counters.ObjectsFailed),
			summary:  summary,
			present:  present,
		})
		replies = append(replies, crafted.sent)
	}

	if !bytes.Equal(replies[0], replies[1]) {
		t.Fatal("the two runs must be driven by identical reply bytes")
	}
	if runs[0].state != runs[1].state || runs[0].counters != runs[1].counters || runs[0].summary != runs[1].summary {
		t.Errorf("the same reply produced different public facts:\n suppressed %+v\n applied    %+v", runs[0], runs[1])
	}
	if runs[0].state != "partial" {
		t.Errorf("state = %q, want partial", runs[0].state)
	}
	// The worlds really did differ: nothing was deleted in the suppressed
	// run, and the named objects are gone in the applied one.
	if len(runs[0].present) != 6 {
		t.Errorf("the suppressed run changed the store: %v", runs[0].present)
	}
	if len(runs[1].present) != 1 || runs[1].present[0] != "kept.txt" {
		t.Errorf("the applied run left %v, want only the neighbour", runs[1].present)
	}
}
