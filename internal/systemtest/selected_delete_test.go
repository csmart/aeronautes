package systemtest

import (
	"context"
	"net/http"
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

// testSelectedDelete produces the selected-set deletion's happy and refusal
// cells through the public service (ADR 0093, ADR 0122 decision 6): the
// selected objects deleted exactly, a manifest among them manifest-only and a
// link as the link it is, with every neighbour and segment unchanged; a
// prefix row refused before any job; and a member another writer replaced
// after it was listed deleted like the rest.
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
	names := []string{"a.txt", "b.txt", "nested/c.txt", "video.dlo", "link.txt"}
	rec := h.StartSelectedDelete(ctx, selectedRequest(h, c, listedRows(t, h, c, names...)))
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	job := h.AwaitJob(rec.JobID())
	if job.State != "complete" || job.Counters.ObjectsDeleted != int64(len(names)) {
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

	// A member another writer replaced after it was listed is deleted like
	// the other: the last request to a name wins.
	rows := listedRows(t, h, c, "racy.txt", "d.txt")
	h.Seed(h.Primary, c, "racy.txt", []byte("second, and longer"), textType)
	replaced := h.StartSelectedDelete(ctx, selectedRequest(h, c, rows))
	if replaced.Err() != nil {
		t.Fatal(replaced.Err())
	}
	if job := h.AwaitJob(replaced.JobID()); job.State != "complete" || job.Counters.ObjectsDeleted != 2 {
		t.Fatalf("job = %+v", job)
	}
	afterReplaced := h.Snapshot(h.Primary, c)
	for _, name := range []string{"racy.txt", "d.txt"} {
		if _, still := afterReplaced.Containers[c].Objects[name]; still {
			t.Fatalf("%s still listed after the job", name)
		}
	}
	if _, still := afterReplaced.Containers[c].Objects["keep.txt"]; !still {
		t.Fatal("the neighbour was deleted")
	}
}

// selectedDeletePartial deletes the member the store accepts and records the
// one it refuses, so the job is partial. The refusal is armed on the mock or
// on the owned Swift's gate, and lifted with a status of 0 so that the
// fixture can be removed. The cell is partial.
func selectedDeletePartial(t *testing.T, h *Harness, refuse func(container, name string, status int)) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "selected-partial")
	h.Seed(h.Primary, c, "held.txt", []byte("the store refuses this one"), textType)
	h.Seed(h.Primary, c, "e.txt", []byte("echo"), textType)
	rows := listedRows(t, h, c, "held.txt", "e.txt")
	refuse(c, "held.txt", http.StatusForbidden)
	t.Cleanup(func() { refuse(c, "held.txt", 0) })
	partial := h.StartSelectedDelete(ctx, selectedRequest(h, c, rows))
	if partial.Err() != nil {
		t.Fatal(partial.Err())
	}
	if job := h.AwaitJob(partial.JobID()); job.State != "partial" || job.Counters.ObjectsDeleted != 1 {
		t.Fatalf("partial job = %+v", job)
	}
	after := h.Snapshot(h.Primary, c)
	if _, still := after.Containers[c].Objects["held.txt"]; !still {
		t.Fatal("the refused member is gone")
	}
	if _, still := after.Containers[c].Objects["e.txt"]; still {
		t.Fatal("the accepted member is still there")
	}
	totals, totalsRec := h.JobItemTotals(ctx, partial.JobID())
	if totalsRec.Err() != nil || totals.Failed != 1 || totals.Deleted != 1 {
		t.Fatalf("partial totals = %+v, %v", totals, totalsRec.Err())
	}
	partial.CreditPartial()
}

func TestSelectedDeletePartialMock(t *testing.T) {
	h := findMockHarness(t)
	selectedDeletePartial(t, h, h.MockPrimary.RefuseObjectDELETE)
}

func TestSelectedDeletePartialSwift(t *testing.T) {
	h := New(t)
	g := newSwiftGate(t, h.Primary.authURL)
	gatedSwift(t, h, g)
	selectedDeletePartial(t, h, g.refuseObjectDELETE)
}

func TestSelectedDeleteCancelMock(t *testing.T) {
	h := findMockHarness(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "selected-cancel")
	h.Seed(h.Primary, c, "a.txt", []byte("alpha"), textType)
	h.Seed(h.Primary, c, "b.txt", []byte("bravo"), textType)
	ready := make(chan string, 1)
	done := false
	h.MockPrimary.BeforeRequest = func(method, path string) {
		if method == "DELETE" && strings.HasSuffix(path, "/"+c+"/a.txt") && !done {
			done = true
			if err := h.Transfer.CancelJob(ctx, <-ready); err != nil {
				t.Error(err)
			}
		}
	}
	defer func() { h.MockPrimary.BeforeRequest = nil }()
	testSelectedDeleteCancel(t, h, c, ready)
}

func TestSelectedDeleteCancelSwift(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "selected-cancel")
	h.Seed(h.Primary, c, "a.txt", []byte("alpha"), textType)
	h.Seed(h.Primary, c, "b.txt", []byte("bravo"), textType)
	g := newSwiftGate(t, h.Primary.authURL)
	gatedSwift(t, h, g)
	ready := make(chan string, 1)
	done := false
	g.mu.Lock()
	g.beforeAny = func(method, path string) {
		if method == "DELETE" && strings.HasSuffix(path, "/"+c+"/a.txt") && !done {
			done = true
			if err := h.Transfer.CancelJob(ctx, <-ready); err != nil {
				t.Error(err)
			}
		}
	}
	g.mu.Unlock()
	testSelectedDeleteCancel(t, h, c, ready)
}

// ADR 0093 decision 4: a cancellation lands between members. Here it lands
// as the first member's DELETE arrives: that member, already issued, is
// deleted, the later member is not attempted, and the job is partial and
// says it was cancelled. A selected-set deletion sends nothing before its
// first DELETE, so it has no earlier moment to be cancelled at. The cell is
// cancel.
func testSelectedDeleteCancel(t *testing.T, h *Harness, c string, ready chan string) {
	t.Helper()
	ctx := context.Background()
	rows := listedRows(t, h, c, "a.txt", "b.txt")
	rec := h.StartSelectedDelete(ctx, app.SelectedDeleteRequestDTO{Scope: h.ActiveScope(ctx), Container: c, Members: rows, Confirm: "2"})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	ready <- rec.JobID()
	if job := h.AwaitJob(rec.JobID()); job.State != "partial" || job.Counters.ObjectsDeleted != 1 {
		t.Fatalf("job = %+v", job)
	}
	after := h.Snapshot(h.Primary, c)
	if _, still := after.Containers[c].Objects["a.txt"]; still {
		t.Fatal("the member in flight was not deleted")
	}
	if _, still := after.Containers[c].Objects["b.txt"]; !still {
		t.Fatal("the member after the cancellation was deleted")
	}
	rec.CreditCancelledBetweenMembers()
}
