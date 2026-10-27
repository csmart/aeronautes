package systemtest

import (
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// BookmarkService.Verify answers for the details as typed and saves nothing:
// the right key reports the account the endpoint answered for, a wrong key
// is refused, and the bookmark list is as empty afterwards as before.

func TestBookmarkVerifyMock(t *testing.T) {
	bookmarkVerify(t, findMockHarness(t))
}

func TestBookmarkVerifyRealSwift(t *testing.T) {
	bookmarkVerify(t, New(t))
}

func bookmarkVerify(t *testing.T, h *Harness) {
	t.Helper()
	ctx := t.Context()
	before, err := h.Bookmarks.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	typed := app.BookmarkDTO{DisplayName: "typed, not saved", AuthURL: h.Primary.authURL, Username: h.Primary.user, TLSMode: "system"}

	result, verified := h.Verify(ctx, typed, h.Primary.key)
	if verified.Err() != nil {
		t.Fatalf("verify with the fixture's key: %v", verified.Err())
	}
	wantAccount := h.Primary.storageURL[strings.LastIndex(h.Primary.storageURL, "/")+1:]
	if result.Account != wantAccount || result.Containers < 0 || result.Objects < 0 || result.BytesUsed < 0 {
		t.Fatalf("verify result = %+v, want account %q with non-negative counts", result, wantAccount)
	}

	_, refused := h.Verify(ctx, typed, h.Primary.key+"-wrong")
	if refused.Err() == nil {
		t.Fatal("a wrong key was not refused")
	}

	after, err := h.Bookmarks.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("verify changed the bookmark list: %d before, %d after", len(before), len(after))
	}
	verified.CreditRead()
	refused.CreditRefusal()
}
