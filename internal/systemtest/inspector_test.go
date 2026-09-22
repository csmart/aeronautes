package systemtest

import (
	"maps"
	"testing"
)

func TestInspectorObjectReceiptMock(t *testing.T) {
	t.Parallel()
	h := findMockHarness(t)
	objects := h.Container(h.Primary, "inspect-objects")
	segments := h.Container(h.Primary, "inspect-segments")
	other := h.Container(h.Secondary, "inspect-other")
	const name = "movie.dlo"
	manifest := segments + "/parts/"
	h.Seed(h.Primary, segments, "parts/0001", []byte("first "), nil)
	h.Seed(h.Primary, segments, "parts/0002", []byte("last"), nil)
	h.Seed(h.Primary, segments, "outside", []byte("not a segment"), nil)
	h.Seed(h.Primary, objects, name, nil, map[string]string{
		"Content-Type":          "video/mp4",
		"X-Object-Manifest":     manifest,
		"X-Object-Meta-Project": "inspection",
		"X-Object-Meta-Take":    "2",
	})
	h.Seed(h.Primary, objects, "sentinel", []byte("keep primary"), map[string]string{"X-Object-Meta-Keep": "yes"})
	h.Seed(h.Secondary, other, name, []byte("keep secondary"), map[string]string{"X-Object-Meta-Keep": "other"})
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)

	detail, receipt := h.InspectObject(t.Context(), h.Primary.BookmarkID, objects, name)
	if receipt.Err() != nil {
		t.Fatal(receipt.Err())
	}
	if detail.Name != name || detail.Scope.BookmarkID != h.Primary.BookmarkID {
		t.Fatalf("object identity = %+v", detail)
	}
	if detail.Class != "dlo" || detail.Manifest != manifest {
		t.Fatalf("DLO classification and manifest = %q, %q", detail.Class, detail.Manifest)
	}
	// The stored manifest is empty; the selected object's HEAD describes
	// its assembled bytes, excluding the object outside the segment prefix.
	if detail.ContentLength != 10 || detail.ContentType != "video/mp4" {
		t.Fatalf("DLO logical fields = %d bytes, %q", detail.ContentLength, detail.ContentType)
	}
	if want := map[string]string{"Project": "inspection", "Take": "2"}; !maps.Equal(detail.Meta, want) {
		t.Fatalf("DLO metadata = %v, want %v", detail.Meta, want)
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	receipt.CreditRead()
}

func TestInspectorPrefixReceiptMock(t *testing.T) {
	t.Parallel()
	h := findMockHarness(t)
	container := h.Container(h.Primary, "inspect-prefixes")
	other := h.Container(h.Secondary, "inspect-other")
	h.Seed(h.Primary, container, "marker/", nil, map[string]string{
		"Content-Type": "application/directory", "X-Object-Meta-Kind": "marker",
	})
	h.Seed(h.Primary, container, "implied/child", []byte("child"), nil)
	h.Seed(h.Primary, container, "real/", []byte("real payload"), map[string]string{
		"Content-Type": "text/plain", "X-Object-Meta-Kind": "real",
	})
	h.Seed(h.Primary, container, "real/child", []byte("separate child"), nil)
	h.Seed(h.Primary, container, "sentinel", []byte("keep primary"), map[string]string{"X-Object-Meta-Keep": "yes"})
	h.Seed(h.Secondary, other, "real/", []byte("keep secondary"), map[string]string{"X-Object-Meta-Keep": "other"})
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)

	cases := []struct {
		prefix      string
		marker      bool
		self        bool
		bytes       int64
		contentType string
		metaKind    string
	}{
		{"marker/", true, true, 0, "application/directory", "marker"},
		{"implied/", false, false, 0, "", ""},
		{"real/", false, true, 12, "text/plain", "real"},
	}
	var receipt *Receipt
	for _, tc := range cases {
		detail, read := h.InspectPrefix(t.Context(), h.Primary.BookmarkID, container, tc.prefix)
		if read.Err() != nil {
			t.Fatalf("prefix %q: %v", tc.prefix, read.Err())
		}
		if detail.Prefix != tc.prefix || detail.Scope.BookmarkID != h.Primary.BookmarkID {
			t.Fatalf("prefix %q identity = %+v", tc.prefix, detail)
		}
		if detail.MarkerBacked != tc.marker || (detail.SelfObject != nil) != tc.self {
			t.Fatalf("prefix %q classification = %+v", tc.prefix, detail)
		}
		if self := detail.SelfObject; self != nil {
			if self.Name != tc.prefix || self.Class != "plain" || self.Manifest != "" ||
				self.ContentLength != tc.bytes || self.ContentType != tc.contentType {
				t.Fatalf("prefix %q self object = %+v", tc.prefix, self)
			}
			if want := map[string]string{"Kind": tc.metaKind}; !maps.Equal(self.Meta, want) {
				t.Fatalf("prefix %q self metadata = %v, want %v", tc.prefix, self.Meta, want)
			}
		}
		receipt = read
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	// The reporter accepts one claim per action/scenario/test. Credit the
	// final read only after all three states and both namespaces are sound.
	receipt.CreditRead()
}
