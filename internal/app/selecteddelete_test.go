package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func selectedHarness(t *testing.T) (*harness, *DeleteService) {
	t.Helper()
	h := newHarness(t)
	h.connect(t)
	return h, NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
}

// rowsOf are the working list's rows for the named objects, the facts a
// selected-set deletion carries (ADR 0093 decision 2).
func rowsOf(t *testing.T, h *harness, names ...string) []ListingRowDTO {
	t.Helper()
	out := make([]ListingRowDTO, 0, len(names))
	for _, name := range names {
		// The working list is delimited: an object under a prefix is listed
		// in that prefix's view, which is where it would be selected.
		prefix := ""
		if slash := strings.LastIndex(name, "/"); slash >= 0 {
			prefix = name[:slash+1]
		}
		page, err := h.browse.Objects(context.Background(), h.bookmarkID(), "fixtures", prefix, "", 1000)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range page.Rows {
			if row.Kind == "object" && row.Name == name {
				out = append(out, row)
				found = true
			}
		}
		if !found {
			t.Fatalf("%s is not listed as an object under %q", name, prefix)
		}
	}
	return out
}

func selectedRequest(h *harness, members []ListingRowDTO) SelectedDeleteRequestDTO {
	return SelectedDeleteRequestDTO{Scope: h.portrait(), Container: "fixtures", Members: members, Confirm: itoa(len(members))}
}

func itoa(n int) string { return strconv.Itoa(n) }

func itemsOf(t *testing.T, h *harness, jobID string) map[string]jobs.ItemRecord {
	t.Helper()
	page, err := h.transfer.jobs.ReadItems(context.Background(), jobID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]jobs.ItemRecord{}
	for _, item := range page.Items {
		out[item.Key] = item
	}
	return out
}

func deletesUnder(h *harness, container string) []string {
	var out []string
	for _, line := range h.mock.RequestLog() {
		if strings.HasPrefix(line, "DELETE ") && strings.Contains(line, "/"+container+"/") {
			out = append(out, line)
		}
	}
	return out
}

// ADR 0093 decision 2: a request that cannot be a selected-set deletion is
// refused before any job row, with no request to the store.
func TestSelectedDeleteRefusesBeforeAnyJobRow(t *testing.T) {
	h, service := selectedHarness(t)
	ctx := context.Background()
	rows := rowsOf(t, h, "normal/hello.txt", "empty-object")
	current := h.portrait()
	requests := h.mock.Requests()
	one := rows[:1]
	cases := map[string]SelectedDeleteRequestDTO{
		"empty scope":        {Container: "fixtures", Members: one, Confirm: "1"},
		"moved generation":   {Scope: ListingScopeDTO{BookmarkID: current.BookmarkID, Generation: current.Generation + 7}, Container: "fixtures", Members: one, Confirm: "1"},
		"another bookmark":   {Scope: ListingScopeDTO{BookmarkID: "not-the-active-one", Generation: current.Generation}, Container: "fixtures", Members: one, Confirm: "1"},
		"no members":         {Scope: current, Container: "fixtures", Confirm: "0"},
		"a prefix member":    {Scope: current, Container: "fixtures", Members: []ListingRowDTO{{Kind: "prefix", Name: "implied/"}}, Confirm: "1"},
		"an empty name":      {Scope: current, Container: "fixtures", Members: []ListingRowDTO{{Kind: "object", Name: ""}}, Confirm: "1"},
		"a repeated name":    {Scope: current, Container: "fixtures", Members: []ListingRowDTO{rows[0], rows[0]}, Confirm: "2"},
		"wrong confirmation": {Scope: current, Container: "fixtures", Members: rows, Confirm: "1"},
		"no container":       {Scope: current, Members: one, Confirm: "1"},
	}
	for name, req := range cases {
		if _, err := service.StartSelectedDelete(ctx, req); err == nil {
			t.Fatalf("%s: not refused", name)
		}
	}
	service.ceiling = func(context.Context) int { return 1 }
	if _, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows)); err == nil || !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("over the ceiling: %v", err)
	}
	if list, _ := h.jobsSvc.List(ctx, "", false, 10); len(list) != 0 {
		t.Fatalf("refused starts created jobs: %+v", list)
	}
	if got := h.mock.Requests(); got != requests {
		t.Fatalf("refused starts sent %d requests to the store", got-requests)
	}
}

// ADR 0093 decision 3 with ADR 0122 decision 6: every member is deleted by
// one DELETE of its own, with no read of the name before or after, the
// manifests manifest-only, and nothing beside them touched.
func TestSelectedDeleteDeletesEveryMemberExactly(t *testing.T) {
	h, service := selectedHarness(t)
	ctx := context.Background()
	names := []string{"normal/hello.txt", "empty-object", "external-dlo", "external-slo"}
	rows := rowsOf(t, h, names...)
	startedAt := len(h.mock.RequestLog())
	jobID, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Type != "delete_selected" || job.Counters.ObjectsDeleted != 4 || job.Counters.ObjectsPlanned != 4 {
		t.Fatalf("job = %+v", job)
	}
	// The job's own requests, before this test reads anything back.
	wire := h.mock.RequestLog()[startedAt:]
	session := h.transfer.connections.sessions[h.bookmarkID()].session
	for _, name := range names {
		if _, err := session.ObjectDetail(ctx, "fixtures", name); err == nil {
			t.Fatalf("%s is still readable", name)
		}
	}
	for _, kept := range []string{"implied/child.txt", "fake/", "unicode/日本語.txt"} {
		if _, err := session.ObjectDetail(ctx, "fixtures", kept); err != nil {
			t.Fatalf("%s was touched: %v", kept, err)
		}
	}
	for store, prefix := range map[string]string{"external-dlo-segments": "wedding-video/", "slo-parts": "backup/"} {
		page, err := h.browse.Objects(ctx, h.bookmarkID(), store, prefix, "", 100)
		if err != nil || len(page.Rows) != 3 {
			t.Fatalf("%s: segments = %d, %v; want 3 untouched", store, len(page.Rows), err)
		}
	}
	items := itemsOf(t, h, jobID)
	for _, name := range names {
		item := items[name]
		if item.Execution == nil || item.Execution.Result != jobs.ItemDeleted || item.IssuedAt.IsZero() || item.Successor != nil || item.Baseline != nil {
			t.Fatalf("%s: item = %+v", name, item)
		}
		if requests := requestsFor(wire, "fixtures", name); len(requests) != 1 || requests[0] != "DELETE" {
			t.Fatalf("%s: requests = %v, want one plain DELETE and no read", name, requests)
		}
	}
	// The drawer reads the same facts through the service: each member's
	// issued mark, its outcome and what it was listed with, and the deletion
	// totals beside the transfer ones.
	page, err := h.jobsSvc.Items(ctx, h.bookmarkID(), jobID, "", 100)
	if err != nil || page.Status != "available" || len(page.Items) != 4 {
		t.Fatalf("items page = %+v, %v", page, err)
	}
	for _, item := range page.Items {
		if item.IssuedAt == "" || item.Execution == nil || item.Execution.Result != "deleted" || item.Successor != nil || item.Baseline != nil {
			t.Fatalf("item DTO %s = %+v", item.Key, item)
		}
		if item.ListedETag == "" {
			t.Fatalf("item DTO %s lost its listed facts: %+v", item.Key, item)
		}
	}
	if totals, err := h.jobsSvc.ItemTotals(ctx, h.bookmarkID(), jobID); err != nil || totals.Issued != 4 || totals.Deleted != 4 {
		t.Fatalf("totals DTO = %+v, %v", totals, err)
	}
	deletes := deletesUnder(h, "fixtures")
	if len(deletes) != 4 {
		t.Fatalf("DELETEs under fixtures = %d, want one per member: %v", len(deletes), deletes)
	}
	seen := map[string]bool{}
	for _, line := range deletes {
		if strings.Contains(line, "bulk-delete") || strings.Contains(line, "multipart-manifest=delete") {
			t.Fatalf("a bulk or segment-expanding request was sent: %s", line)
		}
		if seen[line] {
			t.Fatalf("a DELETE was repeated: %s", line)
		}
		seen[line] = true
	}
}

// The old refusals are gone, not merely untested: a member another writer
// replaced after it was listed, and one that is a link, are deleted like
// every other, the link as the link it is, and the job is complete.
func TestSelectedDeleteDeletesWhateverTheNamesHold(t *testing.T) {
	h, service := selectedHarness(t)
	ctx := context.Background()
	h.mock.PutObject("fixtures", "link.txt", swifttest.Object{Bytes: 0, ContentType: "text/plain", SymlinkTarget: "fixtures/empty-object"})
	rows := rowsOf(t, h, "link.txt", "normal/hello.txt")
	// The listed object is replaced after the rows were taken.
	h.mock.PutObject("fixtures", "normal/hello.txt", swifttest.Object{Bytes: 40, ContentType: "text/plain", ETag: "00000000000000000000000000000040"})
	jobID, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Counters.ObjectsDeleted != 2 {
		t.Fatalf("job = %+v", job)
	}
	session := h.transfer.connections.sessions[h.bookmarkID()].session
	for _, name := range []string{"link.txt", "normal/hello.txt"} {
		if _, err := session.LiteralObjectHead(ctx, "fixtures", name); !swiftcontrol.IsNotFound(err) {
			t.Fatalf("%s is still there: %v", name, err)
		}
	}
	if _, err := session.ObjectDetail(ctx, "fixtures", "empty-object"); err != nil {
		t.Fatalf("the link's target was touched: %v", err)
	}
	if deletes := deletesUnder(h, "fixtures"); len(deletes) != 2 {
		t.Fatalf("DELETEs = %v, want one for each member", deletes)
	}
}

// ADR 0093 decision 4 and the drawer's words: a completed job keeps the
// count of members that ran to an outcome beside its deleted count.
func TestSelectedDeleteKeepsItsSettledCountWhenComplete(t *testing.T) {
	h, service := selectedHarness(t)
	rows := rowsOf(t, h, "normal/hello.txt", "empty-object")
	jobID, err := service.StartSelectedDelete(t.Context(), selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Counters.ObjectsDeleted != 2 || job.Counters.ObjectsCopied != 2 || job.Counters.ObjectsPlanned != 2 {
		t.Fatalf("the completed job lost its counts: %+v", job)
	}
}

// ADR 0093 decision 4: a DELETE the store answered with a status outside
// success, a redirect here, is a refusal recorded with that status at the
// request; with every member refused, the job failed and deleted nothing.
func TestSelectedDeleteCountsARedirectedDeleteAsARequest(t *testing.T) {
	h := newHarness(t)
	var deletes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/fixtures/normal/hello.txt") {
			deletes.Add(1)
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		h.mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	h.authURL = server.URL + "/auth/v1.0"
	h.connect(t)
	service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	rows := rowsOf(t, h, "normal/hello.txt")
	jobID, err := service.StartSelectedDelete(t.Context(), selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	item := itemsOf(t, h, jobID)[rows[0].Name]
	if deletes.Load() != 1 {
		t.Fatalf("the response gate saw %d DELETEs, want exactly one", deletes.Load())
	}
	if job.State != "failed" || !strings.Contains(job.Error, "the store refused all 1 members") || item.Execution == nil || item.Execution.Result != jobs.ItemFailed || item.Execution.Diagnostic.Stage != jobs.StageCommit || item.Execution.Diagnostic.HTTPStatus != 307 {
		t.Fatalf("job=%+v execution=%+v, want a failed job and the member failed at its request with HTTP 307", job, item.Execution)
	}
}

// ADR 0093 decision 4: the members requested are counted once each by their
// own outcome. A cancellation after one DELETE whose reply was lost leaves
// one unverified member and one not attempted, and says one was requested.
func TestSelectedDeleteCountsAnUnverifiedMemberOnceWhenCancelled(t *testing.T) {
	h, service := selectedHarness(t)
	rows := rowsOf(t, h, "normal/hello.txt", "empty-object")
	h.mock.DropNextObjectDELETE()
	ready := make(chan string, 1)
	h.mock.BeforeRequest = func(method, path string) {
		// The cancellation lands as the first member's DELETE arrives, whose
		// reply is then lost, and before the next member.
		if method == "DELETE" && strings.HasSuffix(path, "/fixtures/normal/hello.txt") {
			if err := h.transfer.CancelJob(context.Background(), <-ready); err != nil {
				t.Error(err)
			}
		}
	}
	jobID, err := service.StartSelectedDelete(t.Context(), selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	ready <- jobID
	job := awaitTerminal(t, h, jobID)
	items := itemsOf(t, h, jobID)
	first, later := items[rows[0].Name], items[rows[1].Name]
	if first.Execution == nil || first.Execution.Result != jobs.ItemUnverified || later.Execution == nil || later.Execution.Result != jobs.ItemNotAttempted {
		t.Fatalf("fixture did not settle one unverified DELETE and one untouched member: first=%+v later=%+v", first, later)
	}
	if got := len(deletesUnder(h, "fixtures")); got != 1 {
		t.Fatalf("sent %d DELETEs, want one", got)
	}
	if job.State != "partial" || !strings.Contains(job.Error, "cancelled after 1 of 2 members were requested") {
		t.Fatalf("the uncertain member was counted more than once: job=%+v", job)
	}
}

// ADR 0093 decision 2 reuses ADR 0072 decision 4's selection ceiling, the
// download object ceiling setting, not the browser's retention ceiling.
func TestSelectedDeleteUsesTheSelectionCeilingSetting(t *testing.T) {
	h, service := selectedHarness(t)
	settings := NewSettingsService(settingsFile(t), h.transfer.runner)
	current, err := settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current.DownloadObjectCeiling = 1000
	current.BrowserRetentionCeiling = 2000
	if _, err := settings.Set(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	WireDeleteSettings(service, settings)
	if got := service.selectionCeiling(t.Context()); got != 1000 {
		t.Fatalf("the selection ceiling is %d, want the download object ceiling 1000 rather than the retention ceiling 2000", got)
	}
	if service.ceiling = nil; service.selectionCeiling(t.Context()) != defaultDownloadObjectCeiling {
		t.Fatalf("the default is not the download object ceiling's: %d", service.selectionCeiling(t.Context()))
	}
}

// A member the store refuses does not stop the job: it is recorded failed
// with the store's status, the others are deleted, and the job is partial.
func TestSelectedDeleteSettlesEachMemberOnItsOwn(t *testing.T) {
	h, service := selectedHarness(t)
	ctx := context.Background()
	rows := rowsOf(t, h, "normal/hello.txt", "empty-object", "implied/child.txt")
	h.mock.RefuseObjectDELETE("fixtures", "empty-object", http.StatusForbidden)
	jobID, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "partial" || job.Counters.ObjectsDeleted != 2 || !strings.Contains(job.Error, "2 of 3 members deleted; 1 failed, 0 unknown") {
		t.Fatalf("job = %+v", job)
	}
	items := itemsOf(t, h, jobID)
	if refused := items["empty-object"].Execution; refused == nil || refused.Result != jobs.ItemFailed || refused.Diagnostic.Code != jobs.CodeForbidden || refused.Diagnostic.HTTPStatus != http.StatusForbidden {
		t.Fatalf("refused item = %+v", items["empty-object"])
	}
	for _, name := range []string{"normal/hello.txt", "implied/child.txt"} {
		if items[name].Execution == nil || items[name].Execution.Result != jobs.ItemDeleted {
			t.Fatalf("%s: item = %+v", name, items[name])
		}
	}
	session := h.transfer.connections.sessions[h.bookmarkID()].session
	if _, err := session.ObjectDetail(ctx, "fixtures", "empty-object"); err != nil {
		t.Fatalf("the refused object is gone: %v", err)
	}
}

// Nothing deleted because the store refused every member is a failed job
// that says so, not a cancellation and not a completion.
func TestSelectedDeleteFailsWhenTheStoreRefusesEveryMember(t *testing.T) {
	h, service := selectedHarness(t)
	ctx := context.Background()
	rows := rowsOf(t, h, "normal/hello.txt", "empty-object")
	for _, row := range rows {
		h.mock.RefuseObjectDELETE("fixtures", row.Name, http.StatusForbidden)
	}
	jobID, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "failed" || !strings.Contains(job.Error, "nothing was deleted: the store refused all 2 members") || job.Counters.ObjectsDeleted != 0 {
		t.Fatalf("job = %+v", job)
	}
}

// A reply lost before the store answered leaves the member unverified and
// the job partial; a DELETE whose effect is unproven is never presented as
// no effect.
func TestSelectedDeleteLostReplyIsUnverifiedAndPartial(t *testing.T) {
	h, service := selectedHarness(t)
	ctx := context.Background()
	rows := rowsOf(t, h, "normal/hello.txt", "empty-object")
	h.mock.DropNextObjectDELETE()
	jobID, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "partial" || !strings.Contains(job.Error, "1 unknown") {
		t.Fatalf("job = %+v", job)
	}
	items := itemsOf(t, h, jobID)
	lost := items["normal/hello.txt"]
	if lost.Execution == nil || lost.Execution.Result != jobs.ItemUnverified || lost.Execution.Effect != jobs.EffectUnresolved {
		t.Fatalf("lost item = %+v", lost)
	}
	if items["empty-object"].Execution == nil || items["empty-object"].Execution.Result != jobs.ItemDeleted {
		t.Fatalf("later member = %+v", items["empty-object"])
	}
}

// ADR 0093 decision 4: cancellation lands between members. The member in
// flight finishes, the later ones are recorded as not attempted, and the
// job is partial because a DELETE was sent.
func TestSelectedDeleteCancelsBetweenMembers(t *testing.T) {
	ctx := context.Background()
	t.Run("after a DELETE", func(t *testing.T) {
		h, service := selectedHarness(t)
		rows := rowsOf(t, h, "normal/hello.txt", "empty-object", "implied/child.txt", "unicode/日本語.txt")
		ready := make(chan string, 1)
		cancelled := false
		h.mock.AfterRequest = func(method, path string) {
			if method == "DELETE" && !cancelled {
				cancelled = true
				if err := h.transfer.CancelJob(ctx, <-ready); err != nil {
					t.Error(err)
				}
			}
		}
		jobID, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows))
		if err != nil {
			t.Fatal(err)
		}
		ready <- jobID
		job := awaitTerminal(t, h, jobID)
		if job.State != "partial" || job.Counters.ObjectsDeleted != 1 || !strings.Contains(job.Error, "cancelled after 1 of 4") {
			t.Fatalf("job = %+v", job)
		}
		items := itemsOf(t, h, jobID)
		if items["normal/hello.txt"].Execution == nil || items["normal/hello.txt"].Execution.Result != jobs.ItemDeleted {
			t.Fatalf("the member in flight did not finish: %+v", items["normal/hello.txt"])
		}
		for _, name := range []string{"empty-object", "implied/child.txt", "unicode/日本語.txt"} {
			item := items[name]
			if item.Execution == nil || item.Execution.Result != jobs.ItemNotAttempted || !item.IssuedAt.IsZero() {
				t.Fatalf("%s: item = %+v, want not attempted and never issued", name, item)
			}
		}
		if deletes := deletesUnder(h, "fixtures"); len(deletes) != 1 {
			t.Fatalf("DELETEs = %v, want one", deletes)
		}
	})
}

// A refused scope after a reconnect that moved the generation: nothing is
// recorded and nothing is sent, as for every bound write.
func TestSelectedDeleteRefusesAStalePortrait(t *testing.T) {
	h, service := selectedHarness(t)
	ctx := context.Background()
	rows := rowsOf(t, h, "normal/hello.txt")
	stale := h.portrait()
	h.connect(t)
	if _, err := service.StartSelectedDelete(ctx, SelectedDeleteRequestDTO{Scope: stale, Container: "fixtures", Members: rows, Confirm: "1"}); !errors.Is(err, ErrMutationScopeChanged) {
		t.Fatalf("err = %v, want the scope refusal", err)
	}
	if list, _ := h.jobsSvc.List(ctx, "", false, 10); len(list) != 0 {
		t.Fatalf("a refused start created a job: %+v", list)
	}
}

// ADR 0093 decision 4: an outcome that cannot be recorded after a DELETE may
// have been sent leaves the member unresolved, touches no later member, and
// leaves the job open with its status unavailable rather than writing a
// terminal snapshot that could not say which members were never requested;
// the reopen rule settles the records.
func TestSelectedDeleteLeavesTheJobOpenWhenARecordIsLost(t *testing.T) {
	h, service := selectedHarness(t)
	ctx := context.Background()
	rows := rowsOf(t, h, "normal/hello.txt", "empty-object", "implied/child.txt")
	// The members are admitted before the trigger matters: it refuses the
	// outcome records only.
	refuseSQL(t, h, `CREATE TRIGGER refuse_outcomes BEFORE INSERT ON job_item_observations
		BEGIN SELECT RAISE(ABORT,'fixture required outcome refusal'); END`)
	jobID, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	// The body ends without a terminal state; its audit note is the sign.
	deadline := 0
	for {
		events, err := h.auditSvc.RecentFor(ctx, h.bookmarkID(), 20)
		if err != nil {
			t.Fatal(err)
		}
		noted := false
		for _, e := range events {
			if e.Operation == "delete.selected.unavailable" {
				noted = true
			}
		}
		if noted {
			break
		}
		if deadline++; deadline > 200 {
			t.Fatal("the job never reported its records unavailable")
		}
		awaitTick()
	}
	// The job's status is unavailable: the ledger latched, so the job cannot
	// be read and no new job starts, which is what the operator is told.
	if _, err := h.jobsSvc.Get(ctx, h.bookmarkID(), jobID); err == nil {
		t.Fatal("the job's status was readable while its records were unavailable")
	}
	if err := h.transfer.jobs.Available(); err == nil {
		t.Fatal("the ledger did not latch unavailable after a lost record")
	}
	if _, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows[2:])); err == nil {
		t.Fatal("a new job started while the ledger was unavailable")
	}
	if deletes := deletesUnder(h, "fixtures"); len(deletes) != 1 {
		t.Fatalf("DELETEs = %v, want the first member's only", deletes)
	}
	// Reopen: a fresh store over the same database settles the records.
	refuseSQL(t, h, `DROP TRIGGER refuse_outcomes`)
	reopened := jobs.NewStore(h.db)
	if n, err := reopened.FailInterrupted(ctx); err != nil || n != 1 {
		t.Fatalf("interrupted = %d, %v", n, err)
	}
	page, err := reopened.ReadItems(ctx, jobID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]jobs.ItemRecord{}
	for _, item := range page.Items {
		items[item.Key] = item
	}
	if first := items["normal/hello.txt"]; first.Execution != nil || first.IssuedAt.IsZero() {
		t.Fatalf("the member in flight is not left unresolved: %+v", first)
	}
	for _, name := range []string{"empty-object", "implied/child.txt"} {
		if item := items[name]; item.Execution == nil || item.Execution.Result != jobs.ItemNotAttempted || !item.IssuedAt.IsZero() {
			t.Fatalf("%s: item = %+v, want not attempted and never issued", name, item)
		}
	}
	if totals, err := reopened.ReadItemTotals(ctx, jobID); err != nil || totals.Unresolved != 1 || totals.Deleted != 0 {
		t.Fatalf("totals = %+v, %v", totals, err)
	}
}

func awaitTick() { time.Sleep(10 * time.Millisecond) }
