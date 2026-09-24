package systemtest

import (
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

func TestMutationScopeBindingMock(t *testing.T) {
	mutationScopeBinding(t, findMockHarness(t))
}

func TestMutationScopeBindingRealSwift(t *testing.T) {
	mutationScopeBinding(t, New(t))
}

// ADR 0079 through the public services on two accounts: with the
// primary account active, each of the four kept mutations called under
// the secondary bookmark's scope is refused with the named error, neither
// account changes, and the same call under the active portrait applies.
func mutationScopeBinding(t *testing.T, h *Harness) {
	t.Helper()
	ctx := t.Context()
	container := h.Container(h.Primary, "mutation-scope")
	h.Seed(h.Primary, container, "object.bin", []byte("scoped bytes"), nil)
	if _, err := h.Browse.Connect(ctx, h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	current := h.ActiveScope(ctx)
	if current.BookmarkID != h.Primary.BookmarkID || current.Generation <= 0 {
		t.Fatalf("active scope = %+v", current)
	}
	foreign := app.ListingScopeDTO{BookmarkID: h.Secondary.BookmarkID, Generation: current.Generation}
	objectDetail, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, container, "object.bin")
	if err != nil {
		t.Fatal(err)
	}
	containerDetail, err := h.Inspector.Container(ctx, h.Primary.BookmarkID, container)
	if err != nil {
		t.Fatal(err)
	}
	actions := []struct {
		name string
		run  func(scope app.ListingScopeDTO, suffix string) error
	}{
		{"UpdateObjectMetadata", func(scope app.ListingScopeDTO, suffix string) error {
			_, err := h.Mutate.UpdateObjectMetadata(ctx, scope, container, "object.bin", objectDetail.EditableFingerprint, app.ObjectMetadataEditDTO{Meta: map[string]string{"scope-case": suffix}})
			return err
		}},
		{"UpdateContainer", func(scope app.ListingScopeDTO, suffix string) error {
			_, err := h.Mutate.UpdateContainer(ctx, scope, container, containerDetail.EditableFingerprint, app.ContainerEditDTO{Meta: map[string]string{"scope-case": suffix}})
			return err
		}},
		{"CreateContainer", func(scope app.ListingScopeDTO, suffix string) error {
			name := h.ns + "-scoped-" + suffix
			t.Cleanup(func() { h.purge(h.Primary, name) })
			_, err := h.Mutate.CreateContainer(ctx, scope, app.ContainerCreateDTO{Name: name})
			return err
		}},
		{"CreateMarker", func(scope app.ListingScopeDTO, suffix string) error {
			_, err := h.Mutate.CreateMarker(ctx, scope, container, "scoped-"+suffix+"/")
			return err
		}},
	}
	for _, a := range actions {
		err := a.run(foreign, "foreign")
		if err == nil || !strings.Contains(err.Error(), "the connected account changed; nothing was written") {
			t.Fatalf("%s under the secondary scope was not refused: %v", a.name, err)
		}
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	for _, a := range actions {
		if err := a.run(h.ActiveScope(ctx), "current"); err != nil {
			t.Fatalf("%s under the active portrait: %v", a.name, err)
		}
	}
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}
