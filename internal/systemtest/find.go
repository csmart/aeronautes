package systemtest

import (
	"context"
	"fmt"

	"github.com/csmart/aeronautes/internal/app"
)

// CloseFind also runs in the harness cleanup. Find scenarios defer it in
// the test body so workers exit before their container purges begin.
func (h *Harness) CloseFind() {
	h.T.Helper()
	if err := h.Find.ServiceShutdown(); err != nil {
		h.T.Errorf("Find shutdown: %v", err)
	}
}

func (h *Harness) StartFind(ctx context.Context, container, prefix, query string) (string, *Receipt) {
	id, err := h.Find.StartFind(ctx, h.Primary.BookmarkID, container, prefix, query)
	return id, &Receipt{h: h, action: "FindService.StartFind", scanID: id, err: err}
}

func (h *Harness) FindStatus(ctx context.Context, id string) (*app.FindStatusDTO, *Receipt) {
	status, err := h.Find.FindStatus(ctx, h.Primary.BookmarkID, id)
	r := &Receipt{h: h, action: "FindService.FindStatus", scanID: id, err: err}
	if status != nil {
		captured := *status
		captured.Matches = append([]app.FindMatchDTO(nil), status.Matches...)
		r.findStatus = &captured
	}
	return status, r
}

func (h *Harness) CancelFind(ctx context.Context, id string) *Receipt {
	err := h.Find.CancelFind(ctx, h.Primary.BookmarkID, id)
	return &Receipt{h: h, action: "FindService.CancelFind", scanID: id, err: err}
}

func (r *Receipt) CreditFindComplete(status *Receipt) {
	r.h.T.Helper()
	if r.action != "FindService.StartFind" || r.err != nil || r.scanID == "" ||
		status == nil || status.h != r.h || status.action != "FindService.FindStatus" ||
		status.scanID != r.scanID || status.err != nil || status.findStatus == nil ||
		!status.findStatus.Done || status.findStatus.Error != "" {
		r.h.T.Fatal("Find completion credit needs this start's successful terminal status")
	}
	r.emit("happy", "scan "+r.scanID+" completed without error")
}

func (r *Receipt) CreditFindCancelled(cancel *Receipt, requestEnded <-chan struct{}) {
	r.h.T.Helper()
	if r.action != "FindService.StartFind" || r.err != nil || r.scanID == "" ||
		cancel == nil || cancel.h != r.h || cancel.action != "FindService.CancelFind" ||
		cancel.scanID != r.scanID || cancel.err != nil {
		r.h.T.Fatal("Find cancellation credit needs this start's matching successful cancellation")
	}
	select {
	case <-requestEnded:
	default:
		r.h.T.Fatal("Find cancellation credit needs observed request termination")
	}
	if _, err := r.h.Find.FindStatus(context.Background(), r.h.Primary.BookmarkID, r.scanID); err == nil || err.Error() != fmt.Sprintf("no such scan %s", r.scanID) {
		r.h.T.Fatalf("Find cancellation credit needs the cancelled id to be absent: %v", err)
	}
	r.emit("cancel", "scan "+r.scanID+" cancelled; selected request terminated")
}

func (r *Receipt) CreditFindCancellation() {
	r.h.T.Helper()
	if r.action != "FindService.CancelFind" || r.err != nil || r.scanID == "" {
		r.h.T.Fatal("Find cancellation-call credit needs a successful cancellation receipt")
	}
	r.emit("happy", "cancellation requested for scan "+r.scanID)
}
