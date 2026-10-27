package systemtest

import (
	"context"
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

func TestObjectDeleteMock(t *testing.T)  { testObjectDelete(t, findMockHarness(t)) }
func TestObjectDeleteSwift(t *testing.T) { testObjectDelete(t, New(t)) }

// testObjectDelete produces ADR 0092's three required cells through the public
// service: the deletion of one plain object with every neighbour unchanged, a
// refusal that destroys nothing, and an identity conflict that destroys
// nothing. The oracle is an independent before and after snapshot.
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

	// Happy: exactly the inspected object is gone.
	shown, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "target.txt")
	if err != nil {
		t.Fatal(err)
	}
	rec := h.DeleteObject(ctx, c, "target.txt", shown.ContentFingerprint)
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
	// for this resource, each carrying the transaction identifiers it saw.
	if facts := deleteFactsFor(t, h, c+"/target.txt"); len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || len(facts[0].TransIDs) == 0 {
		t.Fatalf("applied fact = %+v", facts)
	}
	if events := deleteEventsFor(t, h, c+"/target.txt"); len(events) != 1 || events[0].Result != "applied" || len(events[0].TransIDs) == 0 {
		t.Fatalf("applied event = %+v", events)
	}
	rec.CreditApplied()

	// Refusal: a symlink is refused in the form this fixture produces, and a
	// blank identity is refused before anything is read; neither destroys.
	linkShown, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "link.txt")
	if err != nil {
		t.Fatal(err)
	}
	refusedLink := h.DeleteObject(ctx, c, "link.txt", linkShown.ContentFingerprint)
	if refusedLink.Err() == nil {
		t.Fatal("deleting through a symlink was not refused")
	}
	refusedBlank := h.DeleteObject(ctx, c, "target2.txt", "")
	if refusedBlank.Err() == nil {
		t.Fatal("a blank identity was not refused")
	}
	afterRefusals := h.Snapshot(h.Primary, c)
	if d := diffOf(after, afterRefusals); !d.Empty() {
		t.Fatalf("a refused delete changed state:\n%s", d)
	}
	refusedLink.CreditRefusal()

	// Conflict: the name changed between the display and the request.
	racyShown, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "racy.txt")
	if err != nil {
		t.Fatal(err)
	}
	h.Seed(h.Primary, c, "racy.txt", []byte("second, and longer"), text)
	replaced := h.Snapshot(h.Primary, c)
	conflicted := h.DeleteObject(ctx, c, "racy.txt", racyShown.ContentFingerprint)
	var conflict *swiftcontrol.IdentityConflictError
	if !errors.As(conflicted.Err(), &conflict) || conflicted.Outcome() != "conflict" {
		t.Fatalf("err = %v outcome = %q, want an identity conflict", conflicted.Err(), conflicted.Outcome())
	}
	afterConflict := h.Snapshot(h.Primary, c)
	if d := diffOf(replaced, afterConflict); !d.Empty() {
		t.Fatalf("a conflicting delete changed state:\n%s", d)
	}
	conflicted.CreditConflict()
}
