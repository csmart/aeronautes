package systemtest

import (
	"fmt"
	"reflect"
	"testing"
)

type literalBrowseRow struct {
	kind, name string
}

func literalBrowse(t *testing.T, h *Harness) {
	t.Helper()
	cases := []struct {
		label, prefix string
		names         []string
		want          []literalBrowseRow
		self          string
	}{
		{
			label: "exact plain match", prefix: "external",
			names: []string{"external", "external-a", "external/", "external/child", "outside"},
			want:  []literalBrowseRow{{"object", "external"}, {"object", "external-a"}, {"prefix", "external/"}},
		},
		{
			label: "space-only filter", prefix: "  ",
			names: []string{"  ", "  a", "  /child", "outside"},
			want:  []literalBrowseRow{{"object", "  "}, {"prefix", "  /"}, {"object", "  a"}},
		},
		{
			label: "trailing space", prefix: "trailing ",
			names: []string{"trailing ", "trailing  more", "trailing /child", "trailing-other"},
			want:  []literalBrowseRow{{"object", "trailing "}, {"object", "trailing  more"}, {"prefix", "trailing /"}},
		},
		{
			label: "repeated separators and unicode", prefix: "parent//雪",
			names: []string{"parent//雪", "parent//雪 明", "parent//雪/child", "outside"},
			want:  []literalBrowseRow{{"object", "parent//雪"}, {"object", "parent//雪 明"}, {"prefix", "parent//雪/"}},
		},
		{
			label: "literal percent escape", prefix: " literal%2F +日本語",
			names: []string{" literal%2F +日本語", " literal%2F +日本語!", " literal%2F +日本語/child", "literal%2F +日本語"},
			want:  []literalBrowseRow{{"object", " literal%2F +日本語"}, {"object", " literal%2F +日本語!"}, {"prefix", " literal%2F +日本語/"}},
		},
		{
			label: "folder self stays excluded", prefix: "folder/", self: "folder/",
			names: []string{"folder/", "folder//empty", "folder/child", "outside"},
			want:  []literalBrowseRow{{"prefix", "folder//"}, {"object", "folder/child"}},
		},
		{
			label: "leading empty folder", prefix: "/", self: "/",
			names: []string{"/", "//nested", "/child", "outside"},
			want:  []literalBrowseRow{{"prefix", "//"}, {"object", "/child"}},
		},
		{
			label: "root", prefix: "",
			names: []string{"  name ", "dir/child", "literal%2F", "日本語"},
			want:  []literalBrowseRow{{"object", "  name "}, {"prefix", "dir/"}, {"object", "literal%2F"}, {"object", "日本語"}},
		},
	}
	containers := make([]string, len(cases))
	for i, tc := range cases {
		containers[i] = h.Container(h.Primary, fmt.Sprintf("literal-browse-%d", i))
		for _, name := range tc.names {
			h.Seed(h.Primary, containers[i], name, []byte("bytes:"+name), nil)
		}
	}
	sentinel := h.Container(h.Secondary, "literal-browse-sentinel")
	h.Seed(h.Secondary, sentinel, "untouched", []byte("secondary"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	var firstRead *Receipt
	for i, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			trace := limitListingPages(t, h, h.Primary, containers[i], "short", "")
			marker := ""
			var got []literalBrowseRow
			for pageNumber := 0; ; pageNumber++ {
				if pageNumber > len(tc.want) {
					t.Fatal("listing did not terminate within the fixture bound")
				}
				page, receipt := h.BrowseObjects(t.Context(), h.Primary.BookmarkID, containers[i], tc.prefix, marker, 2)
				if receipt.Err() != nil {
					t.Fatal(receipt.Err())
				}
				if firstRead == nil {
					firstRead = receipt
				}
				for _, row := range page.Rows {
					got = append(got, literalBrowseRow{row.Kind, row.Name})
					if row.Kind == "object" && row.Bytes != int64(len("bytes:"+row.Name)) {
						t.Fatalf("wrong byte count for literal object: %+v", row)
					}
				}
				if pageNumber == 0 {
					wantRequests := 2
					if tc.self != "" {
						wantRequests++
					}
					if len(page.Rows) != 2 || len(trace.snapshot()) != wantRequests {
						t.Fatalf("first bridge page must consume only two visible rows: page=%+v requests=%+v", page, trace.snapshot())
					}
				}
				if page.NextMarker == "" {
					break
				}
				if len(page.Rows) != 2 || page.NextMarker != page.Rows[len(page.Rows)-1].Name || page.NextMarker <= marker {
					t.Fatalf("page continuation lost the exact last row: %+v after %q", page, marker)
				}
				marker = page.NextMarker
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("literal prefix %q: got %+v, want %+v", tc.prefix, got, tc.want)
			}
			wantMarkers := []string{""}
			if tc.self != "" {
				wantMarkers = append(wantMarkers, tc.self)
			}
			for _, row := range tc.want {
				wantMarkers = append(wantMarkers, row.name)
			}
			trace.assertMarkers(t, tc.prefix, "/", wantMarkers...)
			for _, request := range trace.snapshot() {
				if request.prefix != tc.prefix || request.delimiter != "/" || request.limit != "2" {
					t.Fatalf("bound request altered the literal filter or page limit: %+v", request)
				}
			}
			trace.assertNoObjectHeads(t)
		})
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	if !t.Failed() {
		firstRead.CreditRead()
	}
}

func TestLiteralBrowseMock(t *testing.T)      { literalBrowse(t, findMockHarness(t)) }
func TestLiteralBrowseRealSwift(t *testing.T) { literalBrowse(t, New(t)) }
