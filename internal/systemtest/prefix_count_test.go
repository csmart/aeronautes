package systemtest

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// ADR 0072 decision 2: counting the objects under a prefix is a count-only
// Find scan: it lists page by page, keeps no match, reports the running
// count and stops when cancelled; a plain scan still needs a query.
func prefixCountCases(t *testing.T, h *Harness) {
	ctx := context.Background()
	c := h.Container(h.Primary, "count")
	const under = 37
	for i := range under {
		h.Seed(h.Primary, c, fmt.Sprintf("scope/%03d", i), []byte("x"), nil)
	}
	h.Seed(h.Primary, c, "outside", []byte("y"), nil)
	h.Seed(h.Primary, c, "scope-adjacent", []byte("z"), nil)
	defer h.CloseFind()

	if _, err := h.Find.StartFind(ctx, h.Primary.BookmarkID, c, "scope/", "", false); err == nil {
		t.Fatal("a plain scan without a query was started")
	}
	id, err := h.Find.StartFind(ctx, h.Primary.BookmarkID, c, "scope/", "", true)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, err := h.Find.FindStatus(ctx, h.Primary.BookmarkID, id)
		if err != nil {
			t.Fatal(err)
		}
		if status.Done {
			if status.Error != "" || status.Scanned != under || len(status.Matches) != 0 || status.Truncated {
				t.Fatalf("count reported %+v, want %d scanned and no matches", status, under)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("count did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPrefixCountMock(t *testing.T)      { prefixCountCases(t, findMockHarness(t)) }
func TestPrefixCountRealSwift(t *testing.T) { prefixCountCases(t, New(t)) }
