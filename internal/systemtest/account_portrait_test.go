package systemtest

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

func TestAccountPortraitBindingMock(t *testing.T) {
	accountPortraitBinding(t, findMockHarness(t))
}

func TestAccountPortraitBindingRealSwift(t *testing.T) {
	accountPortraitBinding(t, New(t))
}

func accountPortraitBinding(t *testing.T, h *Harness) {
	t.Helper()
	ctx := t.Context()
	primary := h.Container(h.Primary, "portrait-primary")
	secondary := h.Container(h.Secondary, "portrait-secondary")
	h.Seed(h.Primary, primary, "keep", []byte("primary content"), nil)
	h.Seed(h.Secondary, secondary, "keep", []byte("secondary content"), nil)
	beforePrimary, beforeSecondary := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	connected, err := h.Browse.Connect(ctx, h.Primary.BookmarkID)
	if err != nil {
		t.Fatal(err)
	}
	portrait, portraitRead := h.ReadAccountPortrait(ctx, h.Primary.BookmarkID)
	caps, capabilityRead := h.ReadCapabilities(ctx, h.Primary.BookmarkID)
	expectedName := h.Primary.storageURL[strings.LastIndex(h.Primary.storageURL, "/")+1:]
	if portraitRead.Err() != nil || portrait.Name != expectedName || portrait.Scope != connected.Scope || portrait.Scope.BookmarkID != h.Primary.BookmarkID || portrait.Scope.Generation <= 0 {
		t.Fatalf("primary portrait = %+v, %v; connect scope %+v", portrait, portraitRead.Err(), connected.Scope)
	}
	if capabilityRead.Err() != nil || caps.Scope != portrait.Scope || (caps.Source != "advertised" && caps.Source != "unknown") {
		t.Fatalf("primary capabilities = %+v, %v", caps, capabilityRead.Err())
	}
	if got, err := h.Browse.Account(ctx, ""); err == nil || !reflect.DeepEqual(got, app.AccountDTO{}) {
		t.Fatalf("empty-bookmark portrait = %+v, %v", got, err)
	}
	if got, err := h.Browse.Capabilities(ctx, ""); err == nil || !reflect.DeepEqual(got, app.CapabilitiesDTO{}) {
		t.Fatalf("empty-bookmark capabilities = %+v, %v", got, err)
	}
	other, err := h.Browse.Connect(ctx, h.Secondary.BookmarkID)
	expectedOtherName := h.Secondary.storageURL[strings.LastIndex(h.Secondary.storageURL, "/")+1:]
	if err != nil || other.Name != expectedOtherName || other.Scope.BookmarkID != h.Secondary.BookmarkID || other.Scope.Generation <= 0 || other.Scope == portrait.Scope {
		t.Fatalf("secondary portrait = %+v, %v", other, err)
	}
	otherCaps, err := h.Browse.Capabilities(ctx, h.Secondary.BookmarkID)
	if err != nil || otherCaps.Scope != other.Scope {
		t.Fatalf("secondary capabilities = %+v, %v", otherCaps, err)
	}
	refusedPortrait, portraitRefusal := h.ReadAccountPortrait(ctx, h.Primary.BookmarkID)
	refusedCaps, capabilityRefusal := h.ReadCapabilities(ctx, h.Primary.BookmarkID)
	if !errors.Is(portraitRefusal.Err(), app.ErrAccountScopeChanged) || !reflect.DeepEqual(refusedPortrait, app.AccountDTO{}) {
		t.Fatalf("stale-bookmark portrait = %+v, %v", refusedPortrait, portraitRefusal.Err())
	}
	if !errors.Is(capabilityRefusal.Err(), app.ErrAccountScopeChanged) || !reflect.DeepEqual(refusedCaps, app.CapabilitiesDTO{}) {
		t.Fatalf("stale-bookmark capabilities = %+v, %v", refusedCaps, capabilityRefusal.Err())
	}
	h.AssertNoCollateral(beforePrimary, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeSecondary, h.SnapshotNamespace(h.Secondary))
	portraitRead.CreditRead()
	capabilityRead.CreditRead()
	portraitRefusal.CreditRefusal()
	capabilityRefusal.CreditRefusal()
}
