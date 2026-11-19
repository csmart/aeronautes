package systemtest

import (
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// Maintained regressions for ADR 0049. A listing read is bound to the
// account the caller named, and its reply states the account that answered.
// These replace the probe archived with the plan: the two cases that
// reproduced P28 there are the refusal and the attribution asserted here.
func TestListingReadsAreAccountScoped(t *testing.T) {
	h := New(t)
	ctx := t.Context()

	// One container name in BOTH accounts holding different objects, which
	// is ordinary: container names are per-account and collide in general.
	const shared = "l11-shared"
	h.rawPut(h.Primary, "/"+shared, nil, nil)
	t.Cleanup(func() { h.purge(h.Primary, shared) })
	h.rawPut(h.Secondary, "/"+shared, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, shared) })
	h.rawPut(h.Primary, "/"+shared+"/only-in-primary", []byte("p"), nil)
	h.rawPut(h.Secondary, "/"+shared+"/only-in-secondary", []byte("s"), nil)
	// A second container on the primary, so a one-row page has a row after
	// it and so a next marker, whatever else the account holds: a page that
	// ends the listing has none (ADR 0074 decision 6).
	const second = "l11-second"
	h.rawPut(h.Primary, "/"+second, nil, nil)
	t.Cleanup(func() { h.purge(h.Primary, second) })

	connect := func(t *testing.T, id, label string) {
		t.Helper()
		if _, err := h.Browse.Connect(ctx, id); err != nil {
			t.Fatalf("connect %s: %v", label, err)
		}
	}

	t.Run("a page continuation refuses after the account changes", func(t *testing.T) {
		connect(t, h.Primary.BookmarkID, "primary")
		first, err := h.Browse.Containers(ctx, h.Primary.BookmarkID, "", 1)
		if err != nil {
			t.Fatalf("first page: %v", err)
		}
		if first.NextMarker == "" {
			t.Fatalf("primary has too few containers to page")
		}
		// Anything reachable from the bridge can connect elsewhere mid-listing.
		connect(t, h.Secondary.BookmarkID, "secondary")
		// The caller continues the page it began, naming the account it still
		// believes it is reading. It must be refused, not answered from the
		// account that happens to be connected now.
		rest, err := h.Browse.Containers(ctx, h.Primary.BookmarkID, first.NextMarker, 50)
		if !errors.Is(err, app.ErrAccountScopeChanged) {
			t.Fatalf("continuation after an account change = (%d rows, %v), want ErrAccountScopeChanged",
				len(rest.Rows), err)
		}
	})

	t.Run("a listing states the account that answered", func(t *testing.T) {
		connect(t, h.Primary.BookmarkID, "primary")
		primary, err := h.Browse.Objects(ctx, h.Primary.BookmarkID, shared, "", "", 10)
		if err != nil {
			t.Fatalf("objects on primary: %v", err)
		}
		connect(t, h.Secondary.BookmarkID, "secondary")
		secondary, err := h.Browse.Objects(ctx, h.Secondary.BookmarkID, shared, "", "", 10)
		if err != nil {
			t.Fatalf("objects on secondary: %v", err)
		}
		// Identical arguments but for the account; the replies must be
		// attributable without inspecting the rows, which collide in general.
		if primary.Scope.BookmarkID != h.Primary.BookmarkID {
			t.Errorf("primary page scope = %q, want the primary bookmark", primary.Scope.BookmarkID)
		}
		if secondary.Scope.BookmarkID != h.Secondary.BookmarkID {
			t.Errorf("secondary page scope = %q, want the secondary bookmark", secondary.Scope.BookmarkID)
		}
		if primary.Scope == secondary.Scope {
			t.Errorf("two accounts produced identical scopes %+v", primary.Scope)
		}
	})

	t.Run("a container listing states the account that answered", func(t *testing.T) {
		connect(t, h.Primary.BookmarkID, "primary")
		page, err := h.Browse.Containers(ctx, h.Primary.BookmarkID, "", 5)
		if err != nil {
			t.Fatalf("containers: %v", err)
		}
		if page.Scope.BookmarkID != h.Primary.BookmarkID {
			t.Errorf("container page scope = %q, want the primary bookmark", page.Scope.BookmarkID)
		}
		if page.Scope.Generation == 0 {
			t.Errorf("container page scope states no generation: %+v", page.Scope)
		}
	})

	t.Run("naming the other account refuses rather than answering", func(t *testing.T) {
		connect(t, h.Primary.BookmarkID, "primary")
		page, err := h.Browse.Objects(ctx, h.Secondary.BookmarkID, shared, "", "", 10)
		if !errors.Is(err, app.ErrAccountScopeChanged) {
			t.Fatalf("read naming the unconnected account = (%d rows, %v), want ErrAccountScopeChanged",
				len(page.Rows), err)
		}
	})

	t.Run("control: a read naming the connected account still answers", func(t *testing.T) {
		connect(t, h.Secondary.BookmarkID, "secondary")
		page, err := h.Browse.Objects(ctx, h.Secondary.BookmarkID, shared, "", "", 10)
		if err != nil {
			t.Fatalf("objects: %v", err)
		}
		if len(page.Rows) != 1 || page.Rows[0].Name != "only-in-secondary" {
			t.Fatalf("secondary listing = %d rows, want the secondary object", len(page.Rows))
		}
	})
}
