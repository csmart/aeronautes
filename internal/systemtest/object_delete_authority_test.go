package systemtest

import (
	"context"
	"net/http"
	"testing"

	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func TestObjectDeleteEmptyNameCannotDeleteTheContainerMock(t *testing.T) {
	testObjectDeleteEmptyName(t, findMockHarness(t))
}

func TestObjectDeleteEmptyNameCannotDeleteTheContainerSwift(t *testing.T) {
	testObjectDeleteEmptyName(t, New(t))
}

// An empty object name is the container's own path, and a deployed Swift routes
// a DELETE of "container/" to its container controller. The public action must
// refuse it before any request, whatever identity the caller supplies, and the
// fresh empty container must still answer afterwards.
func testObjectDeleteEmptyName(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "empty-name")
	headStatus := func() int {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, h.Primary.storageURL+"/"+c, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Auth-Token", h.Primary.token)
		resp, err := h.Primary.rawClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if status := headStatus(); status/100 != 2 {
		t.Fatalf("fresh container HEAD = %d", status)
	}
	if _, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, ""); err == nil {
		t.Fatal("the inspector answered an empty object name")
	}
	// The identity a container HEAD would have supplied: plain, zero bytes,
	// empty entity tag. It is detection input, never a capability.
	identity := (&swiftcontrol.ObjectDetail{Class: swiftcontrol.ClassPlain}).ContentFingerprint()
	// The three shown-object deletions share one service path, and each is
	// refused the same way (ADR 0094 decision 1).
	result, err := h.Mutate.DeleteObject(ctx, h.ActiveScope(ctx), c, "", identity)
	if err == nil || result.Outcome == "applied" {
		t.Fatalf("an empty object name was not refused: outcome %q, err %v", result.Outcome, err)
	}
	if result, err := h.Mutate.DeleteMarker(ctx, h.ActiveScope(ctx), c, "", identity); err == nil || result.Outcome == "applied" {
		t.Fatalf("an empty marker name was not refused: outcome %q, err %v", result.Outcome, err)
	}
	if result, err := h.Mutate.DeleteManifest(ctx, h.ActiveScope(ctx), c, "", identity); err == nil || result.Outcome == "applied" {
		t.Fatalf("an empty manifest name was not refused: outcome %q, err %v", result.Outcome, err)
	}
	if status := headStatus(); status/100 != 2 {
		t.Fatalf("the container no longer answers after the refused deletes: HEAD %d", status)
	}
	facts, err := mutationfacts.NewStore(h.database).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if (fact.Operation == "object.delete" || fact.Operation == "marker.delete" || fact.Operation == "manifest.delete") && fact.Resource == c+"/" {
			t.Fatalf("a refused empty-name delete left a fact: %+v", fact)
		}
	}
}
