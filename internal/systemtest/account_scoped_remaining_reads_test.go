package systemtest

import (
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

func TestRemainingReadsAreAccountScoped(t *testing.T) {
	h := New(t)
	ctx := t.Context()

	shared := h.Container(h.Primary, "l11-remain")
	h.rawPut(h.Secondary, "/"+shared, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, shared) })
	h.rawPut(h.Primary, "/"+shared+"/only-in-primary", []byte("p"), nil)
	h.rawPut(h.Secondary, "/"+shared+"/only-in-secondary", []byte("s"), nil)

	connect := func(t *testing.T, id, label string) {
		t.Helper()
		if _, err := h.Browse.Connect(ctx, id); err != nil {
			t.Fatalf("connect %s: %v", label, err)
		}
	}

	t.Run("inspector detail states the account that answered", func(t *testing.T) {
		connect(t, h.Primary.BookmarkID, "primary")
		primary, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, shared, "only-in-primary")
		if err != nil {
			t.Fatalf("primary object: %v", err)
		}
		connect(t, h.Secondary.BookmarkID, "secondary")
		secondary, err := h.Inspector.Object(ctx, h.Secondary.BookmarkID, shared, "only-in-secondary")
		if err != nil {
			t.Fatalf("secondary object: %v", err)
		}
		if primary.Scope.BookmarkID != h.Primary.BookmarkID {
			t.Errorf("primary scope = %q", primary.Scope.BookmarkID)
		}
		if secondary.Scope.BookmarkID != h.Secondary.BookmarkID {
			t.Errorf("secondary scope = %q", secondary.Scope.BookmarkID)
		}
		if primary.Scope == secondary.Scope {
			t.Errorf("two accounts produced identical scopes %+v", primary.Scope)
		}
	})

	t.Run("inspector naming the other account refuses", func(t *testing.T) {
		connect(t, h.Primary.BookmarkID, "primary")
		_, err := h.Inspector.Object(ctx, h.Secondary.BookmarkID, shared, "only-in-secondary")
		if !errors.Is(err, app.ErrAccountScopeChanged) {
			t.Fatalf("inspector naming the unconnected account = %v, want ErrAccountScopeChanged", err)
		}
	})

	t.Run("a find poll under a later connection refuses", func(t *testing.T) {
		connect(t, h.Primary.BookmarkID, "primary")
		id, err := h.Find.StartFind(ctx, h.Primary.BookmarkID, shared, "", "only-in-primary", false)
		if err != nil {
			t.Fatalf("StartFind: %v", err)
		}
		t.Cleanup(func() { _ = h.Find.CancelFind(ctx, h.Primary.BookmarkID, id) })
		connect(t, h.Secondary.BookmarkID, "secondary")
		_, err = h.Find.FindStatus(ctx, h.Secondary.BookmarkID, id)
		if !errors.Is(err, app.ErrAccountScopeChanged) {
			t.Fatalf("poll after connect elsewhere = %v, want ErrAccountScopeChanged", err)
		}
		status, err := h.Find.FindStatus(ctx, h.Primary.BookmarkID, id)
		if err != nil {
			t.Fatalf("poll naming the scan's account: %v", err)
		}
		if status.Scope == nil || status.Scope.BookmarkID != h.Primary.BookmarkID {
			t.Errorf("scan scope = %+v", status.Scope)
		}
	})
}
