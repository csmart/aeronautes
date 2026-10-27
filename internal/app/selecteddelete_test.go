package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
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

// ADR 0093 decision 3: every member is deleted exactly, with its own
// comparison HEAD, one DELETE and the literal read, the manifests
// manifest-only, and nothing beside them touched.
func TestSelectedDeleteDeletesEveryMemberExactly(t *testing.T) {
	h, service := selectedHarness(t)
	ctx := context.Background()
	names := []string{"normal/hello.txt", "empty-object", "external-dlo", "external-slo"}
	rows := rowsOf(t, h, names...)
	jobID, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Type != "delete_selected" || job.Counters.ObjectsDeleted != 4 || job.Counters.ObjectsPlanned != 4 {
		t.Fatalf("job = %+v", job)
	}
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
		if item.Execution == nil || item.Execution.Result != jobs.ItemDeleted || item.IssuedAt.IsZero() || item.Successor == nil || item.Successor.Present {
			t.Fatalf("%s: item = %+v", name, item)
		}
	}
	// The drawer reads the same facts through the service: each member's
	// issued mark and the empty successor its verifying read observed, and
	// the deletion totals beside the transfer ones.
	page, err := h.jobsSvc.Items(ctx, h.bookmarkID(), jobID, "", 100)
	if err != nil || page.Status != "available" || len(page.Items) != 4 {
		t.Fatalf("items page = %+v, %v", page, err)
	}
	for _, item := range page.Items {
		if item.IssuedAt == "" || item.Execution == nil || item.Execution.Result != "deleted" || item.Successor == nil || item.Successor.Present {
			t.Fatalf("item DTO %s = %+v", item.Key, item)
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

// Members the protocol refuses or finds changed do not stop the job: each
// lands as its own word and the others are deleted, and the job is partial.
func TestSelectedDeleteSettlesEachMemberOnItsOwn(t *testing.T) {
	h, service := selectedHarness(t)
	ctx := context.Background()
	h.mock.PutObject("fixtures", "link.txt", swifttest.Object{Bytes: 0, ContentType: "text/plain", SymlinkTarget: "fixtures/normal/hello.txt"})
	rows := rowsOf(t, h, "link.txt", "normal/hello.txt", "empty-object")
	// The listed object is replaced after the rows were taken.
	h.mock.PutObject("fixtures", "normal/hello.txt", swifttest.Object{Bytes: 40, ContentType: "text/plain", ETag: "00000000000000000000000000000040"})
	jobID, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "partial" || job.Counters.ObjectsDeleted != 1 || !strings.Contains(job.Error, "1 refused, 1 changed since listed") {
		t.Fatalf("job = %+v", job)
	}
	items := itemsOf(t, h, jobID)
	if items["link.txt"].Execution == nil || items["link.txt"].Execution.Result != jobs.ItemRefused {
		t.Fatalf("link item = %+v", items["link.txt"])
	}
	if items["normal/hello.txt"].Execution == nil || items["normal/hello.txt"].Execution.Result != jobs.ItemConflict {
		t.Fatalf("replaced item = %+v", items["normal/hello.txt"])
	}
	if items["empty-object"].Execution == nil || items["empty-object"].Execution.Result != jobs.ItemDeleted {
		t.Fatalf("deleted item = %+v", items["empty-object"])
	}
	session := h.transfer.connections.sessions[h.bookmarkID()].session
	if d, err := session.ObjectDetail(ctx, "fixtures", "normal/hello.txt"); err != nil || d.ContentLength != 40 {
		t.Fatalf("the replaced object was touched: %+v, %v", d, err)
	}
	if deletes := deletesUnder(h, "fixtures"); len(deletes) != 1 {
		t.Fatalf("DELETEs = %v, want only the deleted member's", deletes)
	}
}

// ADR 0093 decision 4: nothing deleted because every member was refused or
// changed since listed is a failed job that says so, not a cancellation and
// not a completion.
func TestSelectedDeleteFailsWhenNothingWasSent(t *testing.T) {
	h, service := selectedHarness(t)
	ctx := context.Background()
	h.mock.PutObject("fixtures", "link.txt", swifttest.Object{Bytes: 0, ContentType: "text/plain", SymlinkTarget: "fixtures/normal/hello.txt"})
	rows := rowsOf(t, h, "link.txt", "normal/hello.txt")
	h.mock.PutObject("fixtures", "normal/hello.txt", swifttest.Object{Bytes: 40, ContentType: "text/plain", ETag: "00000000000000000000000000000040"})
	jobID, err := service.StartSelectedDelete(ctx, selectedRequest(h, rows))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "failed" || !strings.Contains(job.Error, "nothing was deleted") || job.Counters.ObjectsDeleted != 0 {
		t.Fatalf("job = %+v", job)
	}
	if deletes := deletesUnder(h, "fixtures"); len(deletes) != 0 {
		t.Fatalf("DELETEs = %v, want none", deletes)
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
	if job.State != "partial" || !strings.Contains(job.Error, "1 unverified") {
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
// job is partial after a DELETE was sent and cancelled when none was.
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
	t.Run("before any DELETE", func(t *testing.T) {
		h, service := selectedHarness(t)
		h.mock.PutObject("fixtures", "link.txt", swifttest.Object{Bytes: 0, ContentType: "text/plain", SymlinkTarget: "fixtures/normal/hello.txt"})
		rows := rowsOf(t, h, "link.txt", "normal/hello.txt")
		ready := make(chan string, 1)
		cancelled := false
		h.mock.AfterRequest = func(method, path string) {
			// The link's later-baseline HEAD is the first request of the job.
			if method == "HEAD" && strings.HasSuffix(path, "/fixtures/link.txt") && !cancelled {
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
		if job.State != "cancelled" || job.Counters.ObjectsDeleted != 0 {
			t.Fatalf("job = %+v", job)
		}
		items := itemsOf(t, h, jobID)
		if items["link.txt"].Execution == nil || items["link.txt"].Execution.Result != jobs.ItemRefused {
			t.Fatalf("link item = %+v", items["link.txt"])
		}
		if items["normal/hello.txt"].Execution == nil || items["normal/hello.txt"].Execution.Result != jobs.ItemNotAttempted {
			t.Fatalf("later member = %+v", items["normal/hello.txt"])
		}
		if deletes := deletesUnder(h, "fixtures"); len(deletes) != 0 {
			t.Fatalf("DELETEs = %v, want none", deletes)
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
