package app

import (
	"context"
	"encoding/json"
	"fmt"
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
	calls int
}

func (b *bulkIdentityServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Query().Has("bulk-delete") {
		b.calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(b.reply)
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
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	crafted := &bulkIdentityServer{inner: mock, reply: reply}
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
