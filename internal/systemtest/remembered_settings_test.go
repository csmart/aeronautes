package systemtest

import (
	"context"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestRememberedSettingsSurviveANewSignIn: against the owned Swift, the
// settings one HEAD read are shown as last seen after the bookmark is saved
// again, which drops the facts of the old generation and signs in afresh.
// The remembered row is keyed by the storage URL, so this holds only while
// a real cluster answers a new sign-in with the same storage URL
// (ADR 0110 decision 3).
func TestRememberedSettingsSurviveANewSignIn(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	name := h.Container(h.Primary, "seen")
	detail, err := h.Inspector.Container(ctx, h.Primary.BookmarkID, name)
	if err != nil {
		t.Fatal(err)
	}

	bookmarks, err := h.Bookmarks.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range bookmarks {
		if bookmark.ID == h.Primary.BookmarkID {
			if _, err := h.Bookmarks.Save(ctx, bookmark, h.Primary.key); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := h.Browse.Connect(ctx, h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}

	row := listedContainer(t, h, name)
	if row.Settings != nil || row.LastSeen == nil || row.LastSeen.ObservedAt != detail.Settings.ObservedAt {
		t.Fatalf("after a new sign-in the row carries %+v and last seen %+v; want the remembered settings of %s", row.Settings, row.LastSeen, detail.Settings.ObservedAt)
	}
}

func listedContainer(t *testing.T, h *Harness, name string) app.ContainerRowDTO {
	t.Helper()
	marker := ""
	for {
		page, err := h.Browse.Containers(context.Background(), h.Primary.BookmarkID, marker, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Rows {
			if row.Name == name {
				return row
			}
		}
		if page.NextMarker == "" {
			t.Fatalf("container %q not listed", name)
		}
		marker = page.NextMarker
	}
}
