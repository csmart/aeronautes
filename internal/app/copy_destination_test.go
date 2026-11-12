package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// otherDestination saves a second bookmark on the mock that is never made
// the active connection, as a copy's destination usually is not.
func otherDestination(t *testing.T, h *harness) BookmarkDTO {
	t.Helper()
	other, err := h.bookmarks.Save(context.Background(), BookmarkDTO{DisplayName: "Destination", AuthURL: h.authURL, Username: "test:tester"}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	return other
}

func copyTo(h *harness, destination BookmarkDTO, container string) CopyRequestDTO {
	return CopyRequestDTO{SourceBookmarkID: h.connected, SourceContainer: "fixtures", DestBookmarkID: destination.ID, DestContainer: container}
}

// The destination's containers are listed for a bookmark that is not the
// active one (ADR 0115 decision 1).
func TestDestinationContainersListsANonActiveAccount(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	destination := otherDestination(t, h)
	page, err := h.transfer.DestinationContainers(context.Background(), destination.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(page.Names, "fixtures") || page.More {
		t.Fatalf("destination containers = %+v, want the mock's containers and no more", page)
	}
}

// A preview to a missing destination container returns the scope of the
// destination session that found it missing instead of an error; a start
// still fails and creates nothing; the create bound to that scope makes the
// container on the destination bookmark, after which the preview plans
// (ADR 0115 decisions 2 to 4).
func TestMissingDestinationIsOfferedAndCreatedOnItsBookmark(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	destination := otherDestination(t, h)
	const name = "made-for-the-copy"

	preview, err := h.transfer.PlanCopy(ctx, copyTo(h, destination, name))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	missing := preview.DestinationMissing
	if missing == nil || missing.BookmarkID != destination.ID || missing.Container != name || missing.Generation <= 0 {
		t.Fatalf("preview = %+v, want the destination's not-found scope", preview)
	}
	if _, err := h.transfer.StartCopy(ctx, copyTo(h, destination, name)); err == nil {
		t.Fatal("a copy started into a missing destination")
	}
	if _, err := h.mutate.CreateDestinationContainer(ctx, DestinationMissingDTO{}); !errors.Is(err, ErrMutationScopeChanged) {
		t.Fatalf("an empty scope = %v, want it refused", err)
	}
	if _, err := h.mutate.CreateDestinationContainer(ctx, DestinationMissingDTO{BookmarkID: destination.ID, Generation: missing.Generation, Container: "fixtures"}); !errors.Is(err, swiftcontrol.ErrAlreadyExists) {
		t.Fatalf("creating an existing container = %v, want it refused", err)
	}

	created, err := h.mutate.CreateDestinationContainer(ctx, *missing)
	if err != nil || created.Name != name {
		t.Fatalf("create = %+v, %v", created, err)
	}
	events, err := h.auditSvc.RecentFor(ctx, destination.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(events, func(e AuditEventDTO) bool { return e.Operation == "container.create" && e.Resource == name }) {
		t.Fatalf("no create recorded against the destination bookmark: %+v", events)
	}
	again, err := h.transfer.PlanCopy(ctx, copyTo(h, destination, name))
	if err != nil || again.DestinationMissing != nil {
		t.Fatalf("preview after the create = %+v, %v, want a plan", again, err)
	}
}

// A create from a preview whose destination has since changed is refused
// before any request: the operator was shown an older account binding.
func TestDestinationCreateRefusesAMovedGeneration(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	destination := otherDestination(t, h)
	preview, err := h.transfer.PlanCopy(ctx, copyTo(h, destination, "later"))
	if err != nil || preview.DestinationMissing == nil {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	// Saving a new key moves the bookmark's generation.
	if _, err := h.bookmarks.Save(ctx, destination, "testing"); err != nil {
		t.Fatal(err)
	}
	before := h.mock.Requests()
	if _, err := h.mutate.CreateDestinationContainer(ctx, *preview.DestinationMissing); !errors.Is(err, ErrMutationScopeChanged) {
		t.Fatalf("create after the generation moved = %v, want it refused", err)
	}
	if h.mock.Requests() != before {
		t.Fatal("the refused create sent a request")
	}
}
