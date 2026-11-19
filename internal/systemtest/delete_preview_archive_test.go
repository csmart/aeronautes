package systemtest

import (
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// ADR 0102 decision 3 through the public preview, on both fixtures: the bulk
// delete preview of a container another container archives into names that
// owner, the owner's own preview names nobody, and the scan reports itself
// established on an account it could cover.
func TestDeletePreviewNamesArchiveOwnersMock(t *testing.T) {
	deletePreviewArchiveCases(t, findMockHarness)
}
func TestDeletePreviewNamesArchiveOwnersSwift(t *testing.T) { deletePreviewArchiveCases(t, New) }

func deletePreviewArchiveCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Helper()
	h := makeHarness(t)
	owner := h.Container(h.Primary, "archive-owner")
	archive := h.Container(h.Primary, "archive-target")
	h.rawPut(h.Primary, "/"+owner, nil, map[string]string{"X-Versions-Location": archive})
	h.Seed(h.Primary, archive, "00bsome-object/0001", []byte("an earlier version"), textType)

	preview, err := h.Delete.PreviewDelete(t.Context(), app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: archive, Mode: app.ModeEmpty})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.ArchiveOf) != 1 || preview.ArchiveOf[0] != owner || preview.ArchiveUnestablished != "" {
		t.Fatalf("the archive's preview did not name its owner: %+v", preview)
	}
	if preview.Objects != 1 {
		t.Fatalf("the counts changed with the disclosure: %+v", preview)
	}
	ownerPreview, err := h.Delete.PreviewDelete(t.Context(), app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: owner, Mode: app.ModeEmpty})
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerPreview.ArchiveOf) != 0 || ownerPreview.ArchiveUnestablished != "" {
		t.Fatalf("the owner's own preview names an owner: %+v", ownerPreview)
	}
	selected, read := h.PreviewSelectedDelete(t.Context(), app.SelectedDeleteRequestDTO{
		Scope: h.ActiveScope(t.Context()), Container: archive,
		Members: []app.ListingRowDTO{{Kind: "prefix", Name: "00bsome-object/"}, {Kind: "prefix", Name: "empty/"}},
	})
	if read.err != nil || len(selected) != 2 {
		t.Fatalf("selected preview = %+v, %v", selected, read.err)
	}
	for i, row := range selected {
		p := row.Preview
		if row.Error != "" || p == nil || p.Objects != int64(1-i) || len(p.ArchiveOf) != 1 || p.ArchiveOf[0] != owner || p.ArchiveUnestablished != "" {
			t.Fatalf("selected folder %s lost its count or archive: %+v, %q", row.Prefix, p, row.Error)
		}
	}
	read.CreditRead()
}
