package systemtest

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
)

func TestAuditRecentRefusesUnscopedEvents(t *testing.T) {
	ctx := t.Context()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	store := audit.NewStore(database)
	for _, bookmark := range []string{"alpha", "beta"} {
		if err := store.Append(ctx, audit.Event{
			BookmarkID: bookmark, Operation: "object.metadata.update", Resource: bookmark + "/object",
			Summary: bookmark + " event", Result: audit.ResultApplied,
		}); err != nil {
			t.Fatal(err)
		}
	}
	h := &Harness{T: t, Audit: app.NewAuditService(store)}
	events, receipt := h.AuditRecent(ctx, 10)
	if !errors.Is(receipt.Err(), audit.ErrBookmarkRequired) || len(events) != 0 {
		t.Fatalf("unscoped Recent = (%+v, %v), want no events and ErrBookmarkRequired", events, receipt.Err())
	}
	for _, bookmark := range []string{"alpha", "beta"} {
		rows, err := h.Audit.RecentFor(ctx, bookmark, 10)
		if err != nil || len(rows) != 1 || rows[0].BookmarkID != bookmark || rows[0].Resource != bookmark+"/object" {
			t.Fatalf("RecentFor %s after refusal = (%+v, %v), want its unchanged event", bookmark, rows, err)
		}
	}
	receipt.CreditRefusal()
}
