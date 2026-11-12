package systemtest

import (
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// ADR 0115 against the two-account mock: the secondary account, which is
// not the active one, is listed as a copy destination; a preview to a name
// it does not hold returns the not-found scope; an empty scope is refused;
// the create bound to the preview's scope makes the container there.
func TestCopyDestinationCreateMock(t *testing.T) {
	h := findMockHarness(t)
	if h.Secondary == nil {
		t.Skip("the mock harness has no second account")
	}
	source := h.Container(h.Primary, "destination-source")
	listed := h.Container(h.Secondary, "destination-listed")
	page, read := h.DestinationContainers(h.ctx, h.Secondary.BookmarkID)
	if read.Err() != nil || !slices.Contains(page.Names, listed) {
		t.Fatalf("destination containers = %+v, %v", page, read.Err())
	}
	read.CreditRead()

	name := fmt.Sprintf("%s-destination-new-%d", h.ns, atomic.AddInt64(&h.counter, 1))
	t.Cleanup(func() { h.purge(h.Secondary, name) })
	preview, err := h.Transfer.PlanCopy(h.ctx, app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: source,
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: name,
	})
	if err != nil || preview.DestinationMissing == nil || preview.DestinationMissing.BookmarkID != h.Secondary.BookmarkID {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if _, refused := h.CreateDestinationContainer(h.ctx, app.DestinationMissingDTO{}); refused.Err() == nil {
		t.Fatal("an empty scope was not refused")
	} else {
		refused.CreditRefusal()
	}
	created, receipt := h.CreateDestinationContainer(h.ctx, *preview.DestinationMissing)
	if receipt.Err() != nil || created.Name != name {
		t.Fatalf("create = %+v, %v", created, receipt.Err())
	}
	receipt.CreditApplied()
}
