package systemtest

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

func TestSelectedDeleteMock(t *testing.T)  { testSelectedDelete(t, findMockHarness(t)) }
func TestSelectedDeleteSwift(t *testing.T) { testSelectedDelete(t, New(t)) }

// listedRows are the working list's rows for the named objects, read under
// each name's own prefix as a selection would be made (ADR 0093 decision 2).
func listedRows(t *testing.T, h *Harness, container string, names ...string) []app.ListingRowDTO {
	t.Helper()
	out := make([]app.ListingRowDTO, 0, len(names))
	for _, name := range names {
		prefix := ""
		if slash := strings.LastIndex(name, "/"); slash >= 0 {
			prefix = name[:slash+1]
		}
		// The read names the bookmark the selection would be made under: the
		// active one, which a gated case connects through its proxy.
		page, err := h.Browse.Objects(context.Background(), h.ActiveScope(context.Background()).BookmarkID, container, prefix, "", 1000)
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

func selectedRequest(h *Harness, container string, rows []app.ListingRowDTO) app.SelectedDeleteRequestDTO {
	return app.SelectedDeleteRequestDTO{Scope: h.ActiveScope(context.Background()), Container: container, Members: rows, Confirm: strconv.Itoa(len(rows))}
}

// testSelectedDelete produces ADR 0093's happy, refusal, conflict and partial
// cells through the public service: the selected objects deleted exactly
// with every neighbour and segment unchanged, a prefix row refused before
// any job, a member changed since listed left untouched beside the others
// deleted, and a link member refused with the rest deleted.
func testSelectedDelete(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "selected")
	segs := h.Container(h.Primary, "selected-segs")
	h.Seed(h.Primary, segs, "part-0001", []byte("segment"), textType)
	h.Seed(h.Primary, c, "a.txt", []byte("alpha"), textType)
	h.Seed(h.Primary, c, "b.txt", []byte("bravo"), textType)
	h.Seed(h.Primary, c, "nested/c.txt", []byte("charlie"), textType)
	h.rawPut(h.Primary, "/"+c+"/video.dlo", nil, map[string]string{"X-Object-Manifest": segs + "/part-", "Content-Type": "video/mp4"})
	h.Seed(h.Primary, c, "keep.txt", []byte("a neighbour survives"), textType)
	h.Seed(h.Primary, c, "racy.txt", []byte("first"), textType)
	h.Seed(h.Primary, c, "link.txt", nil, map[string]string{"X-Symlink-Target": c + "/keep.txt"})
	h.Seed(h.Primary, c, "d.txt", []byte("delta"), textType)
	before := h.Snapshot(h.Primary, c)
	segsBefore := h.Snapshot(h.Primary, segs)

	// Happy: exactly the selected objects are gone.
	names := []string{"a.txt", "b.txt", "nested/c.txt", "video.dlo"}
	rec := h.StartSelectedDelete(ctx, selectedRequest(h, c, listedRows(t, h, c, names...)))
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	job := h.AwaitJob(rec.JobID())
	if job.State != "complete" || job.Counters.ObjectsDeleted != 4 {
		t.Fatalf("job = %+v", job)
	}
	after := h.Snapshot(h.Primary, c)
	for _, name := range names {
		if _, still := after.Containers[c].Objects[name]; still {
			t.Fatalf("%s still listed after the job", name)
		}
	}
	allowed := make([]string, 0, len(names))
	for _, name := range names {
		allowed = append(allowed, c+"/"+name)
	}
	h.AssertNoCollateral(before, after, allowed...)
	if d := diffOf(segsBefore, h.Snapshot(h.Primary, segs)); !d.Empty() {
		t.Fatalf("the manifest-only member touched its segments:\n%s", d)
	}
	if h.MockPrimary != nil {
		deletes := 0
		for _, line := range h.MockPrimary.RequestLog() {
			if strings.HasPrefix(line, "DELETE ") && strings.Contains(line, "/"+c+"/") {
				deletes++
				if strings.Contains(line, "bulk-delete") || strings.Contains(line, "multipart-manifest=delete") {
					t.Fatalf("a bulk or segment-expanding request was sent: %s", line)
				}
			}
		}
		if deletes != len(names) {
			t.Fatalf("object DELETEs under %s = %d, want one per member", c, deletes)
		}
	}
	rec.CreditHappy()

	// Refusal: a prefix row is not a member, and nothing starts.
	refused := h.StartSelectedDelete(ctx, selectedRequest(h, c, []app.ListingRowDTO{{Kind: "prefix", Name: "nested/"}}))
	if refused.Err() == nil {
		t.Fatal("a prefix row was accepted as a member")
	}
	if d := diffOf(after, h.Snapshot(h.Primary, c)); !d.Empty() {
		t.Fatalf("a refused start changed state:\n%s", d)
	}
	refused.CreditRefusal()

	// Conflict: a member changed after it was listed is left untouched and
	// recorded as changed; the other member is deleted and the job is partial.
	rows := listedRows(t, h, c, "racy.txt", "d.txt")
	h.Seed(h.Primary, c, "racy.txt", []byte("second, and longer"), textType)
	replaced := h.Snapshot(h.Primary, c)
	conflicted := h.StartSelectedDelete(ctx, selectedRequest(h, c, rows))
	if conflicted.Err() != nil {
		t.Fatal(conflicted.Err())
	}
	if job := h.AwaitJob(conflicted.JobID()); job.State != "partial" || job.Counters.ObjectsDeleted != 1 {
		t.Fatalf("conflict job = %+v", job)
	}
	afterConflict := h.Snapshot(h.Primary, c)
	if got := afterConflict.Containers[c].Objects["racy.txt"]; got.Size != replaced.Containers[c].Objects["racy.txt"].Size {
		t.Fatalf("the changed member was touched: %+v", got)
	}
	if _, still := afterConflict.Containers[c].Objects["d.txt"]; still {
		t.Fatal("the unchanged member was not deleted beside the conflict")
	}
	conflicted.CreditJobConflict()

	// Partial: a link member is refused and the rest are deleted.
	h.Seed(h.Primary, c, "e.txt", []byte("echo"), textType)
	partial := h.StartSelectedDelete(ctx, selectedRequest(h, c, listedRows(t, h, c, "link.txt", "e.txt")))
	if partial.Err() != nil {
		t.Fatal(partial.Err())
	}
	if job := h.AwaitJob(partial.JobID()); job.State != "partial" || job.Counters.ObjectsDeleted != 1 {
		t.Fatalf("partial job = %+v", job)
	}
	afterPartial := h.Snapshot(h.Primary, c)
	if _, still := afterPartial.Containers[c].Objects["link.txt"]; !still {
		t.Fatal("the link member was deleted")
	}
	if _, still := afterPartial.Containers[c].Objects["keep.txt"]; !still {
		t.Fatal("the link's target was deleted")
	}
	totals, totalsRec := h.JobItemTotals(ctx, partial.JobID())
	if totalsRec.Err() != nil || totals.Refused != 1 {
		t.Fatalf("partial totals = %+v, %v", totals, totalsRec.Err())
	}
	partial.CreditPartial()
}

func TestSelectedDeleteCancelMock(t *testing.T) {
	h := findMockHarness(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "selected-cancel")
	h.Seed(h.Primary, c, "link.txt", nil, map[string]string{"X-Symlink-Target": c + "/keep.txt"})
	h.Seed(h.Primary, c, "keep.txt", []byte("kept"), textType)
	h.Seed(h.Primary, c, "a.txt", []byte("alpha"), textType)
	ready := make(chan string, 1)
	done := false
	h.MockPrimary.AfterRequest = func(method, path string) {
		if method == "HEAD" && strings.HasSuffix(path, "/"+c+"/link.txt") && !done {
			done = true
			if err := h.Transfer.CancelJob(ctx, <-ready); err != nil {
				t.Error(err)
			}
		}
	}
	defer func() { h.MockPrimary.AfterRequest = nil }()
	testSelectedDeleteCancel(t, h, c, ready)
}

func TestSelectedDeleteCancelSwift(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "selected-cancel")
	h.Seed(h.Primary, c, "link.txt", nil, map[string]string{"X-Symlink-Target": c + "/keep.txt"})
	h.Seed(h.Primary, c, "keep.txt", []byte("kept"), textType)
	h.Seed(h.Primary, c, "a.txt", []byte("alpha"), textType)
	g := newSwiftGate(t, h.Primary.authURL)
	gatedSwift(t, h, g)
	ready := make(chan string, 1)
	done := false
	g.mu.Lock()
	g.afterAny = func(method, path string) {
		if method == "HEAD" && strings.HasSuffix(path, "/"+c+"/link.txt") && !done {
			done = true
			if err := h.Transfer.CancelJob(ctx, <-ready); err != nil {
				t.Error(err)
			}
		}
	}
	g.mu.Unlock()
	testSelectedDeleteCancel(t, h, c, ready)
}

// ADR 0093 decision 4: a cancellation landing before any DELETE was sent,
// here after the first member's later-baseline HEAD refused a link, ends the
// job cancelled with the link refused, the later member not attempted and
// nothing deleted. The cell is cancel.
func testSelectedDeleteCancel(t *testing.T, h *Harness, c string, ready chan string) {
	t.Helper()
	ctx := context.Background()
	before := h.Snapshot(h.Primary, c)
	rows := listedRows(t, h, c, "link.txt", "a.txt")
	rec := h.StartSelectedDelete(ctx, app.SelectedDeleteRequestDTO{Scope: h.ActiveScope(ctx), Container: c, Members: rows, Confirm: "2"})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	ready <- rec.JobID()
	if job := h.AwaitJob(rec.JobID()); job.State != "cancelled" || job.Counters.ObjectsDeleted != 0 {
		t.Fatalf("job = %+v", job)
	}
	if d := diffOf(before, h.Snapshot(h.Primary, c)); !d.Empty() {
		t.Fatalf("a cancelled job changed state:\n%s", d)
	}
	totals, totalsRec := h.JobItemTotals(ctx, rec.JobID())
	if totalsRec.Err() != nil || totals.Refused != 1 || totals.Unresolved != 0 {
		t.Fatalf("totals = %+v, %v", totals, totalsRec.Err())
	}
	rec.CreditCancelled()
}
