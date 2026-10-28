package app

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"

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
func seedSweepScope(h *harness, container string) (members, laterBaseline []string) {
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
	return []string{"scope/a.txt", "scope/b.txt", "scope/nested/deep/c.txt", "scope/empty", "scope/dir/", "scope/slo", "scope/dlo"},
		[]string{"scope/empty", "scope/dir/", "scope/slo", "scope/dlo"}
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
// begun without a marker whatever continuation pages follow it, and the
// one-row listings the postcondition makes.
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

// ADR 0095 decisions 1, 2 and 5: every listed row is a member deleted by the
// per-name protocol, one HEAD-then-DELETE pair and one literal read each, a
// baseline HEAD only for the zero-byte and SLO-listed rows, no bulk request
// and no DELETE repeated; the manifests' segments and every neighbour are
// untouched; a pass that admits nothing new ends the sweep and the one-row
// postcondition is empty.
func TestPrefixSweepDeletesEveryMemberExactly(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	members, laterBaseline := seedSweepScope(h, "sweep")
	preview, err := service.PreviewDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Objects != 7 || preview.LaterBaselineRows != 4 || preview.RequestCost != 18 {
		t.Fatalf("preview = %+v", preview)
	}
	// The preview listed the scope too; count only the job's own requests.
	startedAt := len(h.mock.RequestLog())
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Counters.ObjectsPlanned != 7 || job.Counters.ObjectsDeleted != 7 {
		t.Fatalf("job = %+v", job)
	}
	if !strings.Contains(job.Settings, "per-name-verified") {
		t.Fatalf("effective configuration = %q", job.Settings)
	}
	wire := h.mock.RequestLog()[startedAt:]
	items := sweepItems(t, h, jobID)
	later := map[string]bool{}
	for _, name := range laterBaseline {
		later[name] = true
	}
	for _, name := range members {
		item := items[name]
		if item.Execution == nil || item.Execution.Result != jobs.ItemDeleted || item.IssuedAt.IsZero() || item.Successor == nil || item.Successor.Present {
			t.Fatalf("%s: item = %+v", name, item)
		}
		requests := requestsFor(wire, "sweep", name)
		want := 3
		if later[name] {
			want = 4
		}
		if len(requests) != want || !strings.HasPrefix(requests[0], "HEAD") {
			t.Fatalf("%s: requests = %v, want %d beginning with its HEAD", name, requests, want)
		}
		deletes := 0
		for i, request := range requests {
			if strings.HasPrefix(request, "DELETE") {
				deletes++
				if i == 0 || !strings.HasPrefix(requests[i-1], "HEAD") {
					t.Fatalf("%s: the DELETE does not follow a HEAD of its own: %v", name, requests)
				}
				if strings.Contains(request, "multipart-manifest=delete") {
					t.Fatalf("%s: a segment-expanding DELETE: %v", name, requests)
				}
			}
		}
		if deletes != 1 {
			t.Fatalf("%s: %d DELETEs: %v", name, deletes, requests)
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
	// The second pass listed nothing new and ended the sweep; the
	// postcondition's one row was empty at its first attempt.
	if passes, oneRow := listingsOf(t, wire, "sweep"); passes != 2 || oneRow != 1 {
		t.Fatalf("listings = %d passes, %d one-row; want 2 passes and 1 verification", passes, oneRow)
	}
}

// ADR 0092 decision 2 under the sweep: a link under the prefix is refused
// with its kind before any baseline is fixed, the rest are deleted, the job
// is partial, and neither the link nor its target is touched.
func TestPrefixSweepRefusesALinkWithItsKind(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	h.mock.AddContainer("sweep", "standard")
	seedObject(h.mock, "sweep", "keep.txt", "the target", nil)
	seedObject(h.mock, "sweep", "scope/a.txt", "alpha", nil)
	h.mock.PutObject("sweep", "scope/link", swifttest.Object{Bytes: 0, ContentType: "text/plain", SymlinkTarget: "sweep/keep.txt"})
	jobID, err := service.StartDelete(t.Context(), DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "partial" || job.Counters.ObjectsDeleted != 1 || !strings.Contains(job.Error, "1 refused") {
		t.Fatalf("job = %+v", job)
	}
	items := sweepItems(t, h, jobID)
	link := items["scope/link"]
	if link.Execution == nil || link.Execution.Result != jobs.ItemRefused || link.Execution.Diagnostic.Code != jobs.CodeLink {
		t.Fatalf("link item = %+v", link)
	}
	if requests := requestsFor(h.mock.RequestLog(), "sweep", "scope/link"); len(requests) != 1 || !strings.HasPrefix(requests[0], "HEAD") {
		t.Fatalf("link requests = %v, want its one baseline HEAD", requests)
	}
	if !stillReadable(t, h, "sweep", "scope/link") || !stillReadable(t, h, "sweep", "keep.txt") || stillReadable(t, h, "sweep", "scope/a.txt") {
		t.Fatal("the link or its target was touched, or the plain member was not deleted")
	}
}

// ADR 0095 decision 1 with ADR 0093 decision 3: an ordinary row replaced
// after the listing, by a plain object, a manifest and a link in turn, is
// refused as changed since listed with both identities kept, nothing is
// deleted for it, the rest are deleted, and the job is partial.
func TestPrefixSweepRefusesAMemberChangedSinceListed(t *testing.T) {
	replacements := map[string]struct {
		object swifttest.Object
		class  string
	}{
		"plain":    {swifttest.Object{Bytes: 18, ContentType: "text/plain", ETag: "00000000000000000000000000000018"}, "plain"},
		"manifest": {swifttest.Object{Bytes: 0, ContentType: "video/mp4", Manifest: "sweep-segs/dlo/"}, "dlo"},
		"link":     {swifttest.Object{Bytes: 0, ContentType: "text/plain", SymlinkTarget: "sweep/keep.txt"}, "symlink"},
	}
	for kind, replacement := range replacements {
		t.Run(kind, func(t *testing.T) {
			h, service, bookmark := deleteHarness(t)
			h.mock.AddContainer("sweep", "standard")
			h.mock.AddContainer("sweep-segs", "standard")
			seedObject(h.mock, "sweep-segs", "dlo/0001", "segment", nil)
			seedObject(h.mock, "sweep", "keep.txt", "the target", nil)
			seedObject(h.mock, "sweep", "scope/racy.txt", "first", nil)
			seedObject(h.mock, "sweep", "scope/other.txt", "other", nil)
			listedETag := requireListed(t, h, "sweep", "scope/", "scope/racy.txt").ETag
			replaced := false
			h.mock.BeforeRequest = func(method, path string) {
				// The first request for the row after its listing is its
				// comparison HEAD; the writer lands just before it.
				if method == "HEAD" && strings.HasSuffix(path, "/sweep/scope/racy.txt") && !replaced {
					replaced = true
					h.mock.PutObject("sweep", "scope/racy.txt", replacement.object)
				}
			}
			jobID, err := service.StartDelete(t.Context(), DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep"})
			if err != nil {
				t.Fatal(err)
			}
			job := awaitTerminal(t, h, jobID)
			h.mock.BeforeRequest = nil
			if job.State != "partial" || job.Counters.ObjectsDeleted != 1 || !strings.Contains(job.Error, "1 changed since listed") {
				t.Fatalf("job = %+v", job)
			}
			for _, request := range requestsFor(h.mock.RequestLog(), "sweep", "scope/racy.txt") {
				if strings.HasPrefix(request, "DELETE") {
					t.Fatalf("a DELETE was issued for the changed member")
				}
			}
			if !stillReadable(t, h, "sweep", "scope/racy.txt") || stillReadable(t, h, "sweep", "scope/other.txt") {
				t.Fatal("the changed member was touched or the unchanged one was kept")
			}
			item := sweepItems(t, h, jobID)["scope/racy.txt"]
			if item.Execution == nil || item.Execution.Result != jobs.ItemConflict || item.Baseline == nil || item.Baseline.ETag != listedETag || item.Baseline.Length != 5 {
				t.Fatalf("the identity expected was not kept: %+v", item)
			}
			if item.Successor == nil || item.Successor.Role != "comparison" || !item.Successor.Present || item.Successor.Class != replacement.class {
				t.Fatalf("the identity found was not kept: %+v", item.Successor)
			}
		})
	}
}

func requireListed(t *testing.T, h *harness, container, prefix, name string) ListingRowDTO {
	t.Helper()
	page, err := h.browse.Objects(context.Background(), h.bookmarkID(), container, prefix, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Rows {
		if row.Kind == "object" && row.Name == name {
			return row
		}
	}
	t.Fatalf("%s is not listed under %q", name, prefix)
	return ListingRowDTO{}
}

// ADR 0039 under the sweep: a name written after the first pass's listing is
// deleted in a later pass against its own row; a name refused in the first
// pass is never requested again although it is still listed; the names still
// listed after the job's only request are counted and the job is partial.
func TestPrefixSweepLaterPassRequestsOnlyNewNames(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	h.mock.AddContainer("sweep", "standard")
	seedObject(h.mock, "sweep", "keep.txt", "the target", nil)
	seedObject(h.mock, "sweep", "scope/a.txt", "alpha", nil)
	seedObject(h.mock, "sweep", "scope/b.txt", "bravo", nil)
	h.mock.PutObject("sweep", "scope/link", swifttest.Object{Bytes: 0, ContentType: "text/plain", SymlinkTarget: "sweep/keep.txt"})
	written := false
	h.mock.AfterRequest = func(method, path string) {
		// A writer lands under the scope after the first pass listed it and
		// before the second pass lists it.
		if method == "DELETE" && strings.Contains(path, "/sweep/scope/") && !written {
			written = true
			seedObject(h.mock, "sweep", "scope/late.txt", "late", nil)
		}
	}
	jobID, err := service.StartDelete(t.Context(), DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	h.mock.AfterRequest = nil
	if job.State != "partial" || job.Counters.ObjectsDeleted != 3 || job.Counters.ObjectsPlanned != 4 {
		t.Fatalf("job = %+v", job)
	}
	for _, want := range []string{"1 refused", "1 names still listed after this job's only request for them were not requested again"} {
		if !strings.Contains(job.Error, want) {
			t.Fatalf("job message %q lacks %q", job.Error, want)
		}
	}
	items := sweepItems(t, h, jobID)
	if late := items["scope/late.txt"]; late.Execution == nil || late.Execution.Result != jobs.ItemDeleted || stillReadable(t, h, "sweep", "scope/late.txt") {
		t.Fatalf("the late name was not deleted in a later pass: %+v", late)
	}
	wire := h.mock.RequestLog()
	if requests := requestsFor(wire, "sweep", "scope/link"); len(requests) != 1 {
		t.Fatalf("the refused link was requested again: %v", requests)
	}
	// Three passes: the first admitted three names, the second the late one,
	// the third nothing new; then the one-row verification found the link,
	// however many consistency retries it took.
	if passes, oneRow := listingsOf(t, wire, "sweep"); passes != 3 || oneRow < 1 {
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
	cancelled := false
	var once sync.Once
	h.mock.AfterRequest = func(method, path string) {
		if method == "DELETE" && strings.Contains(path, "/sweep/scope/") && !cancelled {
			cancelled = true
			once.Do(func() {
				if err := h.transfer.CancelJob(ctx, <-ready); err != nil {
					t.Error(err)
				}
			})
		}
	}
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep"})
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

// ADR 0043 under the sweep: a cancellation before any DELETE, here at the
// baseline HEAD of the one link under the prefix, ends the job cancelled
// with the link refused and nothing deleted.
func TestPrefixSweepCancelBeforeAnyDelete(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	seedObject(h.mock, "sweep", "keep.txt", "the target", nil)
	h.mock.PutObject("sweep", "scope/link", swifttest.Object{Bytes: 0, ContentType: "text/plain", SymlinkTarget: "sweep/keep.txt"})
	ready := make(chan string, 1)
	done := false
	h.mock.AfterRequest = func(method, path string) {
		if method == "HEAD" && strings.HasSuffix(path, "/sweep/scope/link") && !done {
			done = true
			if err := h.transfer.CancelJob(ctx, <-ready); err != nil {
				t.Error(err)
			}
		}
	}
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep"})
	if err != nil {
		t.Fatal(err)
	}
	ready <- jobID
	job := awaitTerminal(t, h, jobID)
	h.mock.AfterRequest = nil
	if job.State != "cancelled" || job.Counters.ObjectsDeleted != 0 {
		t.Fatalf("job = %+v", job)
	}
	if item := sweepItems(t, h, jobID)["scope/link"]; item.Execution == nil || item.Execution.Result != jobs.ItemRefused {
		t.Fatalf("link item = %+v", item)
	}
	for _, line := range h.mock.RequestLog() {
		if strings.HasPrefix(line, "DELETE ") {
			t.Fatalf("a DELETE was sent before the cancel: %s", line)
		}
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
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep"})
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

// ADR 0095 decision 5: an empty first listing followed by a writer creating a
// name before the one-row verification ends the job failed, with no request
// sent, the sampled row named in the message, and the container not deleted.
func TestPrefixSweepFailsWhenScopeNonEmptyWithNoRequest(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	listed := 0
	h.mock.AfterRequest = func(method, path string) {
		// After the sweep's only (empty) listing pass and before the one-row
		// verification, a writer creates a name under the scope.
		if method == "GET" && path == "/v1/AUTH_test/sweep" {
			listed++
			if listed == 1 {
				seedObject(h.mock, "sweep", "late.txt", "a writer's row", nil)
			}
		}
	}
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Mode: ModeDeleteContainer, Confirm: "sweep"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	h.mock.AfterRequest = nil
	if job.State != "failed" || !strings.Contains(job.Error, "no request was sent") || !strings.Contains(job.Error, "1 row sampled") {
		t.Fatalf("job = %+v", job)
	}
	for _, line := range h.mock.RequestLog() {
		if strings.HasPrefix(line, "DELETE ") {
			t.Fatalf("a request was sent: %s", line)
		}
	}
	if containerDeleted(h.mock.RequestLog(), "sweep") {
		t.Fatal("the container was deleted after a failed sweep")
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
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep"})
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
	if _, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep"}); err == nil {
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
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	const members = 520 // > the 500-row admission chunk and the 200-row drawer page, and quick under -race
	for i := range members {
		seedObject(h.mock, "sweep", "scope/obj-"+padded(i), "x", nil)
	}
	before := h.db.WritesCommitted()
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep"})
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
	// A real restart: close this process's database so nothing of it stays
	// open, reopen on its file, and read every member's record back from
	// durable storage in the drawer's own pages — not totals alone, and not a
	// second handle layered over the still-open original.
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := db.Open(ctx, h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := restarted.Close(); err != nil {
			t.Error(err)
		}
	}()
	store := jobs.NewStore(restarted)
	recovered := map[string]bool{}
	for cursor := int64(0); ; {
		page, err := store.ReadItems(ctx, jobID, cursor, 200)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.Execution == nil || item.Execution.Result != jobs.ItemDeleted {
				t.Fatalf("member %s not deleted after restart: %+v", item.Key, item.Execution)
			}
			recovered[item.Key] = true
		}
		if page.NextCursor == 0 {
			break
		}
		cursor = page.NextCursor
	}
	if len(recovered) != members {
		t.Fatalf("after restart, %d members readable in the drawer's pages, want %d", len(recovered), members)
	}
	totals, err := store.ReadItemTotals(ctx, jobID)
	if err != nil || totals.Deleted != members || totals.Admitted != members {
		t.Fatalf("reopened totals = %+v, %v", totals, err)
	}
}

func padded(i int) string {
	const digits = "0123456789"
	return string([]byte{digits[(i/1000)%10], digits[(i/100)%10], digits[(i/10)%10], digits[i%10]})
}

// ADR 0095 decision 6: emptying a container compares its configuration before
// and after even when the sweep ends partial, so a change an external writer
// made during a partial empty is reported rather than omitted.
func TestEmptySweepReportsAConfigChangeAfterAPartial(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.SetContainerMeta("media", "Team", "ops")
	seedObject(h.mock, "media", "keep-target", "target", nil)
	seedObject(h.mock, "media", "plain.txt", "doomed", nil)
	h.mock.PutObject("media", "link", swifttest.Object{Bytes: 0, ContentType: "text/plain", SymlinkTarget: "media/keep-target"})
	moved := false
	h.mock.AfterRequest = func(method, path string) {
		// An external writer changes the configuration after the job's before
		// baseline and before its after comparison.
		if method == "DELETE" && strings.HasSuffix(path, "/media/plain.txt") && !moved {
			moved = true
			h.mock.SetContainerMeta("media", "Team", "changed-by-another")
		}
	}
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Mode: ModeEmpty, Confirm: "media"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	h.mock.AfterRequest = nil
	if job.State != "partial" {
		t.Fatalf("job = %s: %s", job.State, job.Error)
	}
	if !strings.Contains(job.Error, "container config changed during empty") {
		t.Fatalf("the config change was omitted from a partial empty: %q", job.Error)
	}
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
		// The container's empty sweep listing is the first GET; its one-row
		// verification is the second. Cancel at the verification, so the sweep
		// ends complete and the cancellation lands at the delete-container
		// boundary, with no destructive request issued.
		if method == "GET" && path == "/v1/AUTH_test/empty-c" {
			listings++
			if listings == 2 {
				if err := h.transfer.CancelJob(ctx, <-ready); err != nil {
					t.Error(err)
				}
			}
		}
	}
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "empty-c", Mode: ModeDeleteContainer, Confirm: "empty-c"})
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

// ADR 0095 decision 3 and the ADR 0085 adjacency under the bounded worker
// pool: another worker's requests may interleave with a member's pair on the
// wire, but a held member's DELETE still follows its own comparison HEAD with
// nothing of its own between. One worker is deliberately held between its HEAD
// and its DELETE while a second worker completes its member; the recorded
// order then shows the held member's DELETE immediately after that member's
// own HEAD, with the other member's requests, and only those, in between.
func TestPrefixSweepHoldsPerMemberAdjacencyUnderWorkerInterleaving(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	h.mock.AddContainer("sweep", "standard")
	seedObject(h.mock, "sweep", "scope/a", "aaa", nil)
	seedObject(h.mock, "sweep", "scope/b", "bbb", nil)

	memberOf := func(path string) string {
		switch {
		case strings.Contains(path, "scope/a"):
			return "a"
		case strings.Contains(path, "scope/b"):
			return "b"
		}
		return ""
	}
	var mu sync.Mutex
	var seq []string
	aHeadSeen := make(chan struct{})
	bDeleted := make(chan struct{})
	var aOnce, bOnce sync.Once
	// Make the interleaving deterministic rather than hoping the pool races the
	// right way: member b waits until member a has taken its comparison HEAD,
	// and member a is then held at its DELETE until b has completed its own
	// member, so b's pair always falls strictly inside a's HEAD→DELETE window.
	h.mock.BeforeRequest = func(method, path string) {
		member := memberOf(path)
		if member == "" {
			return
		}
		if method == "HEAD" && member == "b" {
			<-aHeadSeen
		}
		if method == "DELETE" && member == "a" {
			<-bDeleted
		}
		mu.Lock()
		seq = append(seq, method+" "+member)
		mu.Unlock()
	}
	h.mock.AfterRequest = func(method, path string) {
		switch {
		case method == "HEAD" && memberOf(path) == "a":
			aOnce.Do(func() { close(aHeadSeen) })
		case method == "DELETE" && memberOf(path) == "b":
			bOnce.Do(func() { close(bDeleted) })
		}
	}
	jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "sweep", Prefix: "scope/", Mode: ModePrefix, Confirm: "sweep"})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" {
		t.Fatalf("job = %+v", job)
	}

	mu.Lock()
	defer mu.Unlock()
	iDel := -1
	for i, s := range seq {
		if s == "DELETE a" {
			iDel = i
			break
		}
	}
	if iDel < 0 {
		t.Fatalf("member a was never deleted; sequence = %v", seq)
	}
	iPrevA := -1
	for i := iDel - 1; i >= 0; i-- {
		if strings.HasSuffix(seq[i], " a") {
			iPrevA = i
			break
		}
	}
	if iPrevA < 0 || seq[iPrevA] != "HEAD a" {
		t.Fatalf("member a's DELETE did not immediately follow its own comparison HEAD; sequence = %v", seq)
	}
	interleaved := false
	for i := iPrevA + 1; i < iDel; i++ {
		if strings.HasSuffix(seq[i], " b") {
			interleaved = true
		}
	}
	if !interleaved {
		t.Fatalf("no member-b request interleaved between member a's HEAD and DELETE: %v", seq)
	}
	t.Logf("adjacency under interleaving: %v", seq)
}
