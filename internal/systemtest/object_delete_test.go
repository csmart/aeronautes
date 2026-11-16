package systemtest

import (
	"context"
	"testing"

	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

func TestObjectDeleteMock(t *testing.T)  { testObjectDelete(t, findMockHarness(t)) }
func TestObjectDeleteSwift(t *testing.T) { testObjectDelete(t, New(t)) }

// testObjectDelete produces the object deletion's two required cells through
// the public service (ADR 0122 decision 6): the deletion of one object by its
// name with every neighbour unchanged, and a refusal that destroys nothing.
// It also shows what the deletion no longer refuses: a link is deleted as
// the link it is, and an object another writer replaced after it was shown
// is deleted. The oracle is an independent before and after snapshot.
func testObjectDelete(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "delete")
	text := map[string]string{"Content-Type": "text/plain"}
	h.Seed(h.Primary, c, "target.txt", []byte("delete me"), text)
	h.Seed(h.Primary, c, "target.txt/child", []byte("a child survives"), text)
	h.Seed(h.Primary, c, "target2.txt", []byte("a sibling survives"), text)
	h.Seed(h.Primary, c, "racy.txt", []byte("first"), text)
	// A same-account link both fixtures follow on an ordinary read.
	h.Seed(h.Primary, c, "link.txt", nil, map[string]string{"X-Symlink-Target": c + "/target2.txt"})
	if h.Secondary != nil {
		h.rawPut(h.Secondary, "/"+c, nil, nil)
		t.Cleanup(func() { h.purge(h.Secondary, c) })
		h.Seed(h.Secondary, c, "target.txt", []byte("the other account's object"), text)
	}
	before := h.Snapshot(h.Primary, c)
	var secondaryBefore *oracle.AccountSnapshot
	if h.Secondary != nil {
		secondaryBefore = h.Snapshot(h.Secondary, c)
	}

	// Happy: exactly the named object is gone.
	rec := h.DeleteObject(ctx, c, "target.txt")
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	if rec.Outcome() != "applied" {
		t.Fatalf("outcome = %q", rec.Outcome())
	}
	after := h.Snapshot(h.Primary, c)
	if _, still := after.Containers[c].Objects["target.txt"]; still {
		t.Fatal("target.txt still listed after the delete")
	}
	for _, name := range []string{"target.txt/child", "target2.txt", "racy.txt", "link.txt"} {
		b, a := before.Containers[c].Objects[name], after.Containers[c].Objects[name]
		if a.Digest != b.Digest || a.Size != b.Size {
			t.Fatalf("%s changed: %+v -> %+v", name, b, a)
		}
	}
	h.AssertNoCollateral(before, after, c+"/target.txt")
	if h.Secondary != nil {
		secondaryAfter := h.Snapshot(h.Secondary, c)
		if d := diffOf(secondaryBefore, secondaryAfter); !d.Empty() {
			t.Fatalf("the other account changed:\n%s", d)
		}
	}
	// The durable record of the success: one applied fact and one audit event
	// for this resource, each carrying the DELETE's transaction identifier.
	if facts := deleteFactsFor(t, h, c+"/target.txt"); len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || distinctIDs(facts[0].TransIDs) < 1 {
		t.Fatalf("applied fact = %+v", facts)
	}
	if events := deleteEventsFor(t, h, c+"/target.txt"); len(events) != 1 || events[0].Result != "applied" || distinctIDs(events[0].TransIDs) < 1 {
		t.Fatalf("applied event = %+v", events)
	}
	rec.CreditApplied()

	// A link is deleted as the link it is, and its target stays.
	if linked := h.DeleteObject(ctx, c, "link.txt"); linked.Err() != nil || linked.Outcome() != "applied" {
		t.Fatalf("deleting the link: outcome %q err %v", linked.Outcome(), linked.Err())
	}
	afterLink := h.Snapshot(h.Primary, c)
	if _, still := afterLink.Containers[c].Objects["link.txt"]; still {
		t.Fatal("link.txt still listed after its delete")
	}
	if b, a := after.Containers[c].Objects["target2.txt"], afterLink.Containers[c].Objects["target2.txt"]; a.Digest != b.Digest {
		t.Fatalf("the link's target changed: %+v -> %+v", b, a)
	}

	// An object another writer replaced after it was shown is deleted: the
	// last request to a name wins.
	if _, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "racy.txt"); err != nil {
		t.Fatal(err)
	}
	h.Seed(h.Primary, c, "racy.txt", []byte("second, and longer"), text)
	if replaced := h.DeleteObject(ctx, c, "racy.txt"); replaced.Err() != nil || replaced.Outcome() != "applied" {
		t.Fatalf("deleting the replaced object: outcome %q err %v", replaced.Outcome(), replaced.Err())
	}
	afterReplaced := h.Snapshot(h.Primary, c)
	if _, still := afterReplaced.Containers[c].Objects["racy.txt"]; still {
		t.Fatal("racy.txt still listed after its delete")
	}

	// Refusal: an empty name would address the container itself, and is
	// refused before any request; nothing is destroyed.
	refused := h.DeleteObject(ctx, c, "")
	if refused.Err() == nil {
		t.Fatal("an empty object name was not refused")
	}
	if d := diffOf(afterReplaced, h.Snapshot(h.Primary, c)); !d.Empty() {
		t.Fatalf("a refused delete changed state:\n%s", d)
	}
	refused.CreditRefusal()
}
