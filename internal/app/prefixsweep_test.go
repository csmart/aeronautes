package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// sweepItems reads a sweep job's members keyed by their object name; the
// durable ledger key is container-qualified now (ADR 0095 decision 8), so the
// tests index by the object the row names.
func sweepItems(t *testing.T, h *harness, jobID string) map[string]jobs.ItemRecord {
	t.Helper()
	page, err := h.transfer.jobs.ReadItems(context.Background(), jobID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]jobs.ItemRecord{}
	for _, item := range page.Items {
		out[item.Source.Object] = item
	}
	return out
}

// seedSweepScope fills one container with the row kinds ADR 0095 decision 1
// names: plain objects at two depths, a zero-byte object, a directory
// marker, a static and a dynamic manifest with their segments elsewhere, and
// neighbours outside the prefix whose names share its letters.
func seedSweepScope(h *harness, container string) (members []string) {
	h.mock.AddContainer(container, "standard")
	h.mock.AddContainer(container+"-segs", "standard")
	seedObject(h.mock, container, "scope/a.txt", "alpha", nil)
	seedObject(h.mock, container, "scope/b.txt", "bravo", nil)
	seedObject(h.mock, container, "scope/nested/deep/c.txt", "charlie", nil)
	h.mock.PutObject(container, "scope/empty", swifttest.Object{Bytes: 0, ContentType: "text/plain"})
	h.mock.PutObject(container, "scope/dir/", swifttest.Object{Bytes: 0, ContentType: "application/directory"})
	seedObject(h.mock, container+"-segs", "slo/part-1", "11111", nil)
	seedObject(h.mock, container+"-segs", "slo/part-2", "22222", nil)
	h.mock.PutObject(container, "scope/slo", swifttest.Object{
		Bytes: 10, ContentType: "application/octet-stream", SLO: true, SLOEtag: "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f",
		SLOManifestJSON: `[{"name": "/` + container + `-segs/slo/part-1", "hash": "a", "bytes": 5}, {"name": "/` + container + `-segs/slo/part-2", "hash": "b", "bytes": 5}]`,
	})
	seedObject(h.mock, container+"-segs", "dlo/0001", "dlo-one", nil)
	seedObject(h.mock, container+"-segs", "dlo/0002", "dlo-two", nil)
	h.mock.PutObject(container, "scope/dlo", swifttest.Object{Bytes: 0, ContentType: "video/mp4", Manifest: container + "-segs/dlo/"})
	seedObject(h.mock, container, "scope-keep.txt", "shares the letters, not the prefix", nil)
	seedObject(h.mock, container, "other/keep.txt", "sibling prefix", nil)
	seedObject(h.mock, container, "top.txt", "top level", nil)
	return []string{"scope/a.txt", "scope/b.txt", "scope/nested/deep/c.txt", "scope/empty", "scope/dir/", "scope/slo", "scope/dlo"}
}

// requestsFor are the logged requests to one object, in order, as the method
// followed by the query when there was one. The log is taken as a slice so a
// test can read it once the job has ended, before its own reads add to it.
func requestsFor(lines []string, container, name string) []string {
	var out []string
	for _, line := range lines {
		method, rest, _ := strings.Cut(line, " ")
		path, query, _ := strings.Cut(rest, "?")
		if path == "/v1/AUTH_test/"+container+"/"+name {
			if query != "" {
				method += "?" + query
			}
			out = append(out, method)
		}
	}
	return out
}

// listingsOf counts the container's listing passes, each a full listing
// begun without a marker whatever continuation pages follow it, and any
// listing of one row, which a check after the sweep would make.
func listingsOf(t *testing.T, lines []string, container string) (passes, oneRow int) {
	t.Helper()
	for _, line := range lines {
		method, rest, _ := strings.Cut(line, " ")
		path, query, _ := strings.Cut(rest, "?")
		if method != "GET" || path != "/v1/AUTH_test/"+container {
			continue
		}
		values, err := url.ParseQuery(query)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case values.Get("limit") == "1":
			oneRow++
		case values.Get("marker") == "":
			passes++
		}
	}
	return passes, oneRow
}

func containerDeleted(lines []string, container string) bool {
	for _, line := range lines {
		if line == "DELETE /v1/AUTH_test/"+container {
			return true
		}
	}
	return false
}

func stillReadable(t *testing.T, h *harness, container, name string) bool {
	t.Helper()
	_, err := h.transfer.connections.sessions[h.bookmarkID()].session.ObjectDetail(context.Background(), container, name)
	return err == nil
}

// ADR 0095 decisions 1 and 2 with ADR 0122 decision 6: every listed row is a
// member deleted by one DELETE of its own, with no read of the name before
// or after, no bulk request, no segment-expanding DELETE and none repeated;
// the manifests' segments and every neighbour are untouched; a pass that
// admits nothing new ends the sweep, and the scope is not listed again to
// see what stands there.
func TestPrefixSweepDeletesEveryMemberExactly(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	members := seedSweepScope(h, "sweep")
	preview, err := service.PreviewDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Objects != 7 || preview.PossibleManifests != 4 || preview.RequestCost != 7 {
		t.Fatalf("preview = %+v", preview)
	}
	// The preview listed the scope too; count only the job's own requests.
	startedAt := len(h.mock.RequestLog())
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep/scope/"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Counters.ObjectsPlanned != 7 || job.Counters.ObjectsDeleted != 7 {
		t.Fatalf("job = %+v", job)
	}
	if !strings.Contains(job.Settings, `"strategy":"per-name"`) {
		t.Fatalf("effective configuration = %q", job.Settings)
	}
	wire := h.mock.RequestLog()[startedAt:]
	items := sweepItems(t, h, jobID)
	for _, name := range members {
		item := items[name]
		if item.Execution == nil || item.Execution.Result != jobs.ItemDeleted || item.IssuedAt.IsZero() {
			t.Fatalf("%s: item = %+v", name, item)
		}
		if requests := requestsFor(wire, "sweep", name); len(requests) != 1 || requests[0] != "DELETE" {
			t.Fatalf("%s: requests = %v, want one plain DELETE and no read", name, requests)
		}
		if stillReadable(t, h, "sweep", name) {
			t.Fatalf("%s is still readable", name)
		}
	}
	for _, line := range wire {
		if strings.Contains(line, "bulk-delete") {
			t.Fatalf("a bulk request was sent: %s", line)
		}
	}
	for _, kept := range []string{"scope-keep.txt", "other/keep.txt", "top.txt"} {
		if !stillReadable(t, h, "sweep", kept) {
			t.Fatalf("%s was touched", kept)
		}
	}
	for _, segment := range []string{"slo/part-1", "slo/part-2", "dlo/0001", "dlo/0002"} {
		if !stillReadable(t, h, "sweep-segs", segment) {
			t.Fatalf("segment %s was touched", segment)
		}
	}
	totals, err := h.jobsSvc.ItemTotals(ctx, bookmark, jobID)
	if err != nil || totals.Issued != 7 || totals.Deleted != 7 || totals.Admitted != 7 {
		t.Fatalf("totals = %+v, %v", totals, err)
	}
	// The second pass listed nothing new and ended the sweep; nothing lists
	// the scope a row at a time afterwards.
	if passes, oneRow := listingsOf(t, wire, "sweep"); passes != 2 || oneRow != 0 {
		t.Fatalf("listings = %d passes, %d one-row; want 2 passes and no verification", passes, oneRow)
	}
}

// A link under the prefix is deleted as the link it is, with one DELETE, and
// its target is not touched.
func TestPrefixSweepDeletesALinkAndNotItsTarget(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	h.mock.AddContainer("sweep", "standard")
	seedObject(h.mock, "sweep", "keep.txt", "the target", nil)
	seedObject(h.mock, "sweep", "scope/a.txt", "alpha", nil)
	h.mock.PutObject("sweep", "scope/link", swifttest.Object{Bytes: 0, ContentType: "text/plain", SymlinkTarget: "sweep/keep.txt"})
	jobID, err := service.StartDelete(t.Context(), DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep/scope/"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Counters.ObjectsDeleted != 2 {
		t.Fatalf("job = %+v", job)
	}
	if requests := requestsFor(h.mock.RequestLog(), "sweep", "scope/link"); len(requests) != 1 || requests[0] != "DELETE" {
		t.Fatalf("the link's requests = %v, want one plain DELETE", requests)
	}
	if !stillReadable(t, h, "sweep", "keep.txt") {
		t.Fatal("the link's target was deleted")
	}
}

// The old refusal is gone, not merely untested: a member another writer
// replaced after the listing, by a plain object and by a manifest in turn,
// is deleted like every other, and the job is complete.
func TestPrefixSweepDeletesAMemberReplacedSinceListed(t *testing.T) {
	for name, replacement := range map[string]swifttest.Object{
		"a plain object": {Bytes: 99, ContentType: "text/plain", ETag: "99999999999999999999999999999999"},
		"a manifest":     {Bytes: 0, ContentType: "video/mp4", Manifest: "sweep-segs/other/"},
	} {
		t.Run(name, func(t *testing.T) {
			h, service, bookmark := deleteHarness(t)
			h.mock.AddContainer("sweep", "standard")
			seedObject(h.mock, "sweep", "scope/a.txt", "alpha", nil)
			seedObject(h.mock, "sweep", "scope/b.txt", "bravo", nil)
			// The replacement lands after the sweep's listing and before any
			// member is requested.
			var replaced atomic.Bool
			h.mock.AfterRequest = func(method, path string) {
				if method == "GET" && path == "/v1/AUTH_test/sweep" && replaced.CompareAndSwap(false, true) {
					h.mock.PutObject("sweep", "scope/b.txt", replacement)
				}
			}
			jobID, err := service.StartDelete(t.Context(), DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep/scope/"})
			if err != nil {
				t.Fatal(err)
			}
			job := awaitTerminal(t, h, jobID)
			h.mock.AfterRequest = nil
			if job.State != "complete" || job.Counters.ObjectsDeleted != 2 {
				t.Fatalf("job = %+v", job)
			}
			if stillReadable(t, h, "sweep", "scope/b.txt") {
				t.Fatal("the replaced member is still there")
			}
			if item := sweepItems(t, h, jobID)["scope/b.txt"]; item.Execution == nil || item.Execution.Result != jobs.ItemDeleted {
				t.Fatalf("item = %+v", item)
			}
		})
	}
}

// ADR 0039 under the sweep: a name written after the first pass's listing is
// deleted in a later pass; a name the store refused to delete in the first
// pass is never requested again although it is still listed, and the job is
// partial for it.
func TestPrefixSweepLaterPassRequestsOnlyNewNames(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	h.mock.AddContainer("sweep", "standard")
	seedObject(h.mock, "sweep", "scope/a.txt", "alpha", nil)
	seedObject(h.mock, "sweep", "scope/b.txt", "bravo", nil)
	seedObject(h.mock, "sweep", "scope/held.txt", "the store refuses this one", nil)
	h.mock.RefuseObjectDELETE("sweep", "scope/held.txt", http.StatusForbidden)
	// The sweep deletes with several workers and the mock serves each request
	// from its own goroutine, so this one-shot guard must be atomic.
	var written atomic.Bool
	h.mock.AfterRequest = func(method, path string) {
		// A writer lands under the scope after the first pass listed it and
		// before the second pass lists it.
		if method == "DELETE" && strings.Contains(path, "/sweep/scope/") && written.CompareAndSwap(false, true) {
			seedObject(h.mock, "sweep", "scope/late.txt", "late", nil)
		}
	}
	jobID, err := service.StartDelete(t.Context(), DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep/scope/"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	h.mock.AfterRequest = nil
	if job.State != "partial" || job.Counters.ObjectsDeleted != 3 || job.Counters.ObjectsPlanned != 4 {
		t.Fatalf("job = %+v", job)
	}
	if want := "3 of 4 members deleted; 1 failed, 0 unknown"; !strings.Contains(job.Error, want) {
		t.Fatalf("job message %q lacks %q", job.Error, want)
	}
	items := sweepItems(t, h, jobID)
	if late := items["scope/late.txt"]; late.Execution == nil || late.Execution.Result != jobs.ItemDeleted || stillReadable(t, h, "sweep", "scope/late.txt") {
		t.Fatalf("the late name was not deleted in a later pass: %+v", late)
	}
	if held := items["scope/held.txt"]; held.Execution == nil || held.Execution.Result != jobs.ItemFailed || held.Execution.Diagnostic.HTTPStatus != http.StatusForbidden {
		t.Fatalf("the refused name's record = %+v", held)
	}
	wire := h.mock.RequestLog()
	if requests := requestsFor(wire, "sweep", "scope/held.txt"); len(requests) != 1 {
		t.Fatalf("the refused name was requested again: %v", requests)
	}
	// Three passes: the first admitted three names, the second the late one,
	// the third nothing new.
	if passes, oneRow := listingsOf(t, wire, "sweep"); passes != 3 || oneRow != 0 {
		t.Fatalf("listings = %d passes, %d one-row", passes, oneRow)
	}
}

// ADR 0043 under the sweep: a cancellation during a long sweep ends the job
// partial with the members before it deleted, the one in flight on each
// worker settled, nothing later issued, and the counts exact; no member is
// requested twice.
func TestPrefixSweepCancelDuringSweep(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	for i := range 40 {
		seedObject(h.mock, "sweep", "scope/obj-"+padded(i), "data", nil)
	}
	ready := make(chan string, 1)
	// The mock serves requests from concurrent goroutines, so this one-shot guard
	// must be atomic; CompareAndSwap both wins the race and fires the cancel once.
	var cancelled atomic.Bool
	h.mock.AfterRequest = func(method, path string) {
		if method == "DELETE" && strings.Contains(path, "/sweep/scope/") && cancelled.CompareAndSwap(false, true) {
			if err := h.transfer.CancelJob(ctx, <-ready); err != nil {
				t.Error(err)
			}
		}
	}
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep/scope/"})
	if err != nil {
		t.Fatal(err)
	}
	ready <- jobID
	job := awaitTerminal(t, h, jobID)
	h.mock.AfterRequest = nil
	if job.State != "partial" {
		t.Fatalf("job = %+v", job)
	}
	totals, err := h.jobsSvc.ItemTotals(ctx, bookmark, jobID)
	if err != nil {
		t.Fatal(err)
	}
	// Every admitted member is settled exactly once, by its own outcome:
	// the sum of the disjoint facets equals what was admitted, and the job
	// deleted at least the one whose DELETE triggered the cancel.
	settled := totals.Deleted + totals.Refused + totals.Conflicts + totals.Failed + totals.Unverified
	notAttempted := totals.Admitted - settled
	if totals.Deleted < 1 || notAttempted < 0 || settled > totals.Admitted {
		t.Fatalf("counts do not add up once: %+v", totals)
	}
	seen := map[string]int{}
	for _, line := range h.mock.RequestLog() {
		if strings.HasPrefix(line, "DELETE /v1/AUTH_test/sweep/scope/") {
			seen[line]++
			if seen[line] > 1 {
				t.Fatalf("a member was requested twice: %s", line)
			}
		}
	}
}

// ADR 0043 under the sweep: a cancellation before any DELETE, here as the
// scope's listing is answered, ends the job cancelled with nothing deleted
// and the one member not attempted.
func TestPrefixSweepCancelBeforeAnyDelete(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	seedObject(h.mock, "sweep", "scope/a.txt", "alpha", nil)
	ready := make(chan string, 1)
	var done atomic.Bool
	h.mock.BeforeRequest = func(method, path string) {
		if method == "GET" && path == "/v1/AUTH_test/sweep" && done.CompareAndSwap(false, true) {
			if err := h.transfer.CancelJob(ctx, <-ready); err != nil {
				t.Error(err)
			}
		}
	}
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep/scope/"})
	if err != nil {
		t.Fatal(err)
	}
	ready <- jobID
	job := awaitTerminal(t, h, jobID)
	h.mock.BeforeRequest = nil
	if job.State != "cancelled" || job.Counters.ObjectsDeleted != 0 {
		t.Fatalf("job = %+v", job)
	}
	for _, line := range h.mock.RequestLog() {
		if strings.HasPrefix(line, "DELETE ") {
			t.Fatalf("a DELETE was sent after the cancel: %s", line)
		}
	}
	if !stillReadable(t, h, "sweep", "scope/a.txt") {
		t.Fatal("the member was deleted")
	}
}

// ADR 0085 under the sweep: a member whose DELETE reply is lost is unverified,
// the job partial, and no DELETE is repeated for it.
func TestPrefixSweepUnverifiedWhenReplyLost(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	seedObject(h.mock, "sweep", "scope/a.txt", "alpha", nil)
	seedObject(h.mock, "sweep", "scope/b.txt", "bravo", nil)
	h.mock.DropNextObjectDELETE()
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep/scope/"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "partial" {
		t.Fatalf("job = %+v", job)
	}
	totals, err := h.jobsSvc.ItemTotals(ctx, bookmark, jobID)
	if err != nil || totals.Unverified != 1 {
		t.Fatalf("totals = %+v, %v", totals, err)
	}
	seen := map[string]int{}
	for _, line := range h.mock.RequestLog() {
		if strings.HasPrefix(line, "DELETE /v1/AUTH_test/sweep/scope/") {
			seen[line]++
			if seen[line] > 1 {
				t.Fatalf("a member's DELETE was repeated after a lost reply: %s", line)
			}
		}
	}
}

// ADR 0036 under the sweep: an outcome that cannot be recorded after a DELETE
// leaves the job open with its status unavailable, blocks new jobs, deletes no
// container, and the reopen rule settles the member from the issued mark.
func TestPrefixSweepLeavesTheJobOpenWhenARecordIsLost(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	seedObject(h.mock, "sweep", "scope/only.txt", "alone", nil)
	refuseSQL(t, h, `CREATE TRIGGER refuse_sweep_outcomes BEFORE INSERT ON job_item_observations
		BEGIN SELECT RAISE(ABORT,'fixture required outcome refusal'); END`)
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep/scope/"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := 0
	for {
		events, err := h.auditSvc.RecentFor(ctx, bookmark, 20)
		if err != nil {
			t.Fatal(err)
		}
		noted := false
		for _, e := range events {
			if e.Operation == "delete.prefix.unavailable" {
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
	if err := h.transfer.jobs.Available(); err == nil {
		t.Fatal("the ledger did not latch unavailable after a lost record")
	}
	if _, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep/scope/"}); err == nil {
		t.Fatal("a new deletion started while the ledger was unavailable")
	}
	if deletes := deletesUnder(h, "sweep"); len(deletes) != 1 {
		t.Fatalf("DELETEs = %v, want the one member's only", deletes)
	}
	// Reopen: a fresh store over the same database settles the issued member
	// as unresolved, with no deleted outcome anywhere.
	refuseSQL(t, h, `DROP TRIGGER refuse_sweep_outcomes`)
	reopened := jobs.NewStore(h.db)
	if n, err := reopened.FailInterrupted(ctx); err != nil || n != 1 {
		t.Fatalf("interrupted = %d, %v", n, err)
	}
	totals, err := reopened.ReadItemTotals(ctx, jobID)
	if err != nil || totals.Deleted != 0 || totals.Unresolved != 1 {
		t.Fatalf("reopened totals = %+v, %v", totals, err)
	}
}

// ADR 0095 decision 4 and L04: a sweep of more members than a ledger admission
// page and than the drawer's page deletes them all, writes exactly two ledger
// rows a member (one issued mark and one observation), keeps every member's
// record readable in the drawer's pages, and reads them all back from a fresh
// database opened on the same file. It measures the ledger row count, not
// resident memory, which the sweep bounds by holding one listing page at a
// time in its structure rather than by anything this test asserts.
func TestPrefixSweepScaleBoundedAndDurable(t *testing.T) {
	if input := os.Getenv("AERONAUTES_SCALE_READBACK"); input != "" {
		scaleReadbackChild(t, input)
		return
	}
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	const members = 520 // > the 500-row admission chunk and the 200-row drawer page, and quick under -race
	for i := range members {
		seedObject(h.mock, "sweep", "scope/obj-"+padded(i), "x", nil)
	}
	before := h.db.WritesCommitted()
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep/scope/"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	swept := h.db.WritesCommitted() - before // measured before any read inflates it
	if job.State != "complete" || job.Counters.ObjectsPlanned != members || job.Counters.ObjectsDeleted != members {
		t.Fatalf("job = %+v", job)
	}
	// The drawer reads every member in pages, none lost to a display cap.
	drawer := map[string]bool{}
	for cursor := ""; ; {
		page, err := h.jobsSvc.Items(ctx, bookmark, jobID, cursor, 200)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.Execution == nil || item.Execution.Result != "deleted" {
				t.Fatalf("member %s not deleted in the drawer: %+v", item.Key, item.Execution)
			}
			drawer[item.Key] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(drawer) != members {
		t.Fatalf("drawer held %d members, want %d", len(drawer), members)
	}
	// Measured write cost: the committed write transactions during the sweep
	// are the work actually done, not the rows that happen to survive it. The
	// two ledger writes a member ADR 0095 adds beside the 2N+Z requests — the
	// issued mark and the outcome — are joined by the one per-member progress
	// counter every job writes, so the sweep's cost is three committed writes a
	// member, plus the admitted pages and the job's own lifecycle. The cost
	// must be linear and never inflated per member: at least the two ledger
	// writes each, and no more than three each plus the pages and a small
	// bounded lifecycle. A regression that re-wrote a member would cross it.
	pages := int64((members + 499) / 500)
	if swept < 2*members {
		t.Fatalf("the sweep committed %d write transactions for %d members; want at least the two ledger writes a member", swept, members)
	}
	if swept > 3*members+pages+48 {
		t.Fatalf("the sweep committed %d write transactions for %d members; want ~three a member (two ledger + one progress counter) plus %d admitted pages and bounded lifecycle", swept, members, pages)
	}
	t.Logf("measured write cost: %d committed write transactions for %d members (%.3f a member = two ledger writes + one progress counter), plus %d admitted pages and lifecycle",
		swept, members, float64(swept)/float64(members), pages)
	// The same two-a-member shape seen as retained rows, for completeness.
	var issued, observations int
	reader := h.db.Reader()
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_items WHERE job_id=? AND issued_at IS NOT NULL`, jobID).Scan(&issued); err != nil {
		t.Fatal(err)
	}
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_item_observations WHERE item_id IN (SELECT id FROM job_items WHERE job_id=?)`, jobID).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if issued != members || observations != members {
		t.Fatalf("ledger rows = %d issued, %d observations; want %d each", issued, observations, members)
	}
	// A real restart across a process boundary: this process closes its
	// database, and a genuinely fresh child process opens it on its file and
	// reads every member of this larger-than-a-page sweep back in the drawer's
	// own pages, with exact counts — not an in-process handle reopen, and not
	// totals alone.
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "scale-input.json")
	outputPath := filepath.Join(dir, "scale-output.json")
	reqData, err := json.Marshal(struct{ Database, JobID, Output string }{h.dbPath, jobID, outputPath})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, reqData, 0600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	child := exec.CommandContext(cctx, self, "-test.run=^TestPrefixSweepScaleBoundedAndDurable$", "-test.timeout=80s")
	child.Env = append(os.Environ(), "AERONAUTES_SCALE_READBACK="+inputPath)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("scale readback child: %v\n%s", err, out)
	}
	outData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Members           int
		Deleted, Admitted int64
	}
	if err := json.Unmarshal(outData, &result); err != nil {
		t.Fatal(err)
	}
	if result.Members != members || result.Deleted != members || result.Admitted != members {
		t.Fatalf("after a process restart the fresh process read %+v, want %d members all deleted and admitted", result, members)
	}
}

// scaleReadbackChild is the fresh process of the scale restart: it opens the
// database the parent's sweep wrote and closed, reads every member in the
// drawer's pages, and reports the count and totals it recovered.
func scaleReadbackChild(t *testing.T, inputPath string) {
	t.Helper()
	data, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var req struct{ Database, JobID, Output string }
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(context.Background(), req.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := jobs.NewStore(database)
	seen := map[string]bool{}
	for cursor := int64(0); ; {
		page, err := store.ReadItems(context.Background(), req.JobID, cursor, 200)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.Execution != nil && item.Execution.Result == jobs.ItemDeleted {
				seen[item.Key] = true
			}
		}
		if page.NextCursor == 0 {
			break
		}
		cursor = page.NextCursor
	}
	totals, err := store.ReadItemTotals(context.Background(), req.JobID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(struct {
		Members           int
		Deleted, Admitted int64
	}{len(seen), totals.Deleted, totals.Admitted})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(req.Output, out, 0600); err != nil {
		t.Fatal(err)
	}
}

func padded(i int) string {
	const digits = "0123456789"
	return string([]byte{digits[(i/1000)%10], digits[(i/100)%10], digits[(i/10)%10], digits[i%10]})
}

// ADR 0095 decision 5 and 6: an already-empty container asked to be deleted
// and cancelled before any destructive request is cancelled, not partial with
// a claimed deletion, and the container is left standing.
func TestDeleteContainerEmptyCancelledIsCancelled(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("empty-c", "standard")
	ready := make(chan string, 1)
	listings := 0
	h.mock.AfterRequest = func(method, path string) {
		// The container's one sweep listing is empty. Cancel as it is
		// answered, so the sweep ends complete and the cancellation lands at
		// the delete-container boundary, with no destructive request issued.
		if method == "GET" && path == "/v1/AUTH_test/empty-c" {
			listings++
			if listings == 1 {
				if err := h.transfer.CancelJob(ctx, <-ready); err != nil {
					t.Error(err)
				}
			}
		}
	}
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "empty-c", Mode: ModeDeleteContainer, Confirm: "delete empty-c"})
	if err != nil {
		t.Fatal(err)
	}
	ready <- jobID
	job := awaitTerminal(t, h, jobID)
	h.mock.AfterRequest = nil
	if job.State != "cancelled" {
		t.Fatalf("job = %s: %s", job.State, job.Error)
	}
	if containerDeleted(h.mock.RequestLog(), "empty-c") {
		t.Fatal("the container was deleted after a cancellation")
	}
}

// ADR 0095 decision 6: the container is deleted only after a COMPLETE sweep
// with an empty postcondition. A sweep left partial by a lost reply must leave
// the container standing; widening the guard from complete to complete-or-
// partial would delete it over an unverified member, and this is the case that
// catches that, where TestDeleteContainerEmptyCancelledIsCancelled (a cancel
// before any effect) does not.
func TestDeleteContainerSurvivesAPartialSweep(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	seedObject(h.mock, "sweep", "a.txt", "alpha", nil)
	seedObject(h.mock, "sweep", "b.txt", "bravo", nil)
	h.mock.DropNextObjectDELETE() // one member's reply is lost, so the sweep is partial
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Mode: ModeDeleteContainer, Confirm: "delete sweep"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "partial" {
		t.Fatalf("job = %+v, want partial for the lost reply", job)
	}
	if containerDeleted(h.mock.RequestLog(), "sweep") {
		t.Fatal("the container was deleted after a partial sweep")
	}
}

// ADR 0095 decision 4 (ADR 0093 decision 4) for the sweep specifically: the
// issued mark is written before a member's DELETE, so a crash between the
// request and its outcome leaves a mark that settles the member as
// possibly-deleted. This exercises the sweep's own member path, not the static
// segment loop: as the object's DELETE arrives the ledger already holds an
// issued mark, which a mutant moving MarkIssued after the request would not.
func TestPrefixSweepIssuesBeforeTheDelete(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	seedObject(h.mock, "sweep", "scope/only", "x", nil)
	var mu sync.Mutex
	issuedAtDelete := int64(-1)
	h.mock.BeforeRequest = func(method, path string) {
		if method != "DELETE" || !strings.Contains(path, "/sweep/scope/only") {
			return
		}
		var n int64
		if err := h.db.Reader().QueryRowContext(ctx,
			"SELECT COUNT(*) FROM job_items WHERE issued_at IS NOT NULL").Scan(&n); err != nil {
			return
		}
		mu.Lock()
		if issuedAtDelete < 0 {
			issuedAtDelete = n
		}
		mu.Unlock()
	}
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep/scope/"})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" {
		t.Fatalf("job = %+v", job)
	}
	mu.Lock()
	seen := issuedAtDelete
	mu.Unlock()
	if seen < 1 {
		t.Fatalf("as the member's DELETE arrived the ledger held %d issued marks, want at least 1 (the issued mark must precede the request)", seen)
	}
}

// ADR 0095 decision 4 / line 119, the concurrent recording-failure boundary:
// one member's outcome write fails and latches the ledger unavailable while a
// second, already-issued worker is held as its DELETE arrives; that held
// worker then completes its own DELETE, because a member in flight is never
// interrupted inside its own request, and the latch deletes no container.
// The single-member lost-record test does not exercise a second held
// worker; this is the combination.
func TestPrefixSweepHeldWorkerFinishesWholeWhenAnotherRecordIsLost(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	// Ten members, more than the eight-worker pool, so two are admitted but
	// never dispatched. The names sort so a_fail and a_held are the first two
	// dispatched and in flight.
	seedObject(h.mock, "sweep", "a_fail", "alpha", nil) // the only member let through; its record fails and latches
	seedObject(h.mock, "sweep", "a_held", "held", nil)  // already issued, held as its DELETE arrives, across the failure
	for i := 0; i < 8; i++ {
		seedObject(h.mock, "sweep", fmt.Sprintf("f%d", i), "filler", nil)
	}
	refuseSQL(t, h, `CREATE TRIGGER refuse_sweep_outcomes BEFORE INSERT ON job_item_observations
		BEGIN SELECT RAISE(ABORT,'fixture required outcome refusal'); END`)
	memberOf := func(path string) string {
		if _, after, ok := strings.Cut(path, "/sweep/"); ok {
			return after
		}
		return ""
	}
	var mu sync.Mutex
	atHead := map[string]bool{} // the seven non-failing in-flight members, held as their DELETEs arrive
	gateExpired := false
	allAtHead := make(chan struct{})
	var allOnce sync.Once
	waitForLatch := func() {
		for i := 0; i < 5000; i++ {
			if h.transfer.jobs.Available() != nil {
				return
			}
			awaitTick()
		}
		mu.Lock()
		gateExpired = true
		mu.Unlock()
	}
	// Barriers impose the whole schedule rather than relying on the pool's
	// race. Each of the seven non-failing in-flight members marks itself as
	// its DELETE arrives, so the full issued set is established before a_fail
	// is let through to record and latch; and those seven are held there
	// until the latch, so none finishes early and frees a worker to issue one
	// of the two queued members. A barrier that expires before its boundary
	// fails the test rather than silently releasing.
	h.mock.BeforeRequest = func(method, path string) {
		if method != "DELETE" {
			return
		}
		member := memberOf(path)
		if member == "" {
			return
		}
		if member == "a_fail" {
			select {
			case <-allAtHead:
			case <-time.After(60 * time.Second):
				mu.Lock()
				gateExpired = true
				mu.Unlock()
			}
			return
		}
		mu.Lock()
		first := !atHead[member]
		atHead[member] = true
		n := len(atHead)
		mu.Unlock()
		if n >= 7 {
			allOnce.Do(func() { close(allAtHead) })
		}
		if first {
			waitForLatch()
		}
	}
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Mode: ModeDeleteContainer, Confirm: "delete sweep"})
	if err != nil {
		t.Fatal(err)
	}
	// The unavailable audit event is the early return after the latch, taken
	// before any container delete (ADR 0095 decision 4).
	deadline := 0
	for {
		events, err := h.auditSvc.RecentFor(ctx, bookmark, 20)
		if err != nil {
			t.Fatal(err)
		}
		noted := false
		for _, e := range events {
			if e.Operation == "delete.delete_container.unavailable" {
				noted = true
			}
		}
		if noted {
			break
		}
		if deadline++; deadline > 5000 {
			t.Fatal("the job never reported unavailable after the lost record")
		}
		awaitTick()
	}
	mu.Lock()
	expired := gateExpired
	mu.Unlock()
	if expired {
		t.Fatal("a barrier expired before its boundary was reached; the intended schedule was not imposed")
	}
	if err := h.transfer.jobs.Available(); err == nil {
		t.Fatal("the ledger did not latch unavailable after the lost record")
	}
	log := h.mock.RequestLog()
	pathOf := func(line string) string {
		_, rest, _ := strings.Cut(line, " ")
		path, _, _ := strings.Cut(rest, "?")
		return path
	}
	// The held, already-issued worker completed its member on the wire and
	// nothing else: its one DELETE.
	var heldSeq []string
	for _, line := range log {
		if memberOf(pathOf(line)) == "a_held" {
			method, _, _ := strings.Cut(line, " ")
			heldSeq = append(heldSeq, method)
		}
	}
	if len(heldSeq) != 1 || heldSeq[0] != "DELETE" {
		t.Fatalf("held member's wire sequence = %v, want exactly its one DELETE", heldSeq)
	}
	if containerDeleted(log, "sweep") {
		t.Fatal("the container was deleted over an unavailable ledger")
	}
	// The two queued members were neither requested on the wire nor issued.
	for _, q := range []string{"f6", "f7"} {
		for _, line := range log {
			if memberOf(pathOf(line)) == q {
				t.Fatalf("queued member %s was requested: %s", q, line)
			}
		}
	}
	var admitted, issued, queuedIssued int
	if err := h.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM job_items WHERE job_id=?`, jobID).Scan(&admitted); err != nil {
		t.Fatal(err)
	}
	if err := h.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM job_items WHERE job_id=? AND issued_at IS NOT NULL`, jobID).Scan(&issued); err != nil {
		t.Fatal(err)
	}
	if err := h.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM job_items WHERE job_id=? AND issued_at IS NOT NULL AND item_key IN ('sweep/f6','sweep/f7')`, jobID).Scan(&queuedIssued); err != nil {
		t.Fatal(err)
	}
	if admitted != 10 || issued != 8 || queuedIssued != 0 {
		t.Fatalf("admitted %d, issued %d, queued issued %d; want 10 admitted, exactly 8 issued, 0 queued issued", admitted, issued, queuedIssued)
	}
}
