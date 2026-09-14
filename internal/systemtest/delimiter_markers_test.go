package systemtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"testing"
)

func delimiterMarkerHTTP(t *testing.T, h *Harness) {
	t.Helper()
	container := h.Container(h.Primary, "delimiter-http")
	for _, name := range []string{"scope/", "scope/a/", "scope/a/one", "scope/a/two", "scope/b", "scope/b/three", "scope/z", "outside", "repeated//", "repeated//a/child", "repeated//z", " 雪%2F/", " 雪%2F/a/child", " 雪%2F/z"} {
		h.Seed(h.Primary, container, name, []byte("bytes:"+name), nil)
	}
	sentinel := h.Container(h.Secondary, "delimiter-sentinel")
	h.Seed(h.Secondary, sentinel, "untouched", []byte("secondary"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	cases := []struct {
		label, prefix, marker, delimiter string
		want                             []string
	}{
		{"initial own object", "scope/", "", "/", []string{"object:scope/", "prefix:scope/a/", "object:scope/b", "prefix:scope/b/", "object:scope/z"}},
		{"after own marker", "scope/", "scope/", "/", []string{"prefix:scope/a/", "object:scope/b", "prefix:scope/b/", "object:scope/z"}},
		{"after child group", "scope/", "scope/a/", "/", []string{"object:scope/b", "prefix:scope/b/", "object:scope/z"}},
		{"inside child group", "scope/", "scope/a/o", "/", []string{"prefix:scope/a/", "object:scope/b", "prefix:scope/b/", "object:scope/z"}},
		{"inside nested group", "scope/", "scope/a/one/", "/", []string{"prefix:scope/a/", "object:scope/b", "prefix:scope/b/", "object:scope/z"}},
		{"marker before partial filter", "scope/b", "scope/", "/", []string{"object:scope/b", "prefix:scope/b/"}},
		{"root after partial group", "", "scope", "/", []string{"prefix:scope/"}},
		{"root after group interior", "", "scope/a", "/", []string{"prefix:scope/"}},
		{"deep ignores groups", "scope/", "scope/a/", "", []string{"object:scope/a/one", "object:scope/a/two", "object:scope/b", "object:scope/b/three", "object:scope/z"}},
		{"repeated separator own marker", "repeated//", "repeated//", "/", []string{"prefix:repeated//a/", "object:repeated//z"}},
		{"literal unicode own marker", " 雪%2F/", " 雪%2F/", "/", []string{"prefix: 雪%2F/a/", "object: 雪%2F/z"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			query := url.Values{"format": {"json"}, "prefix": {tc.prefix}, "marker": {tc.marker}, "delimiter": {tc.delimiter}, "limit": {"100"}}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.Primary.storageURL+"/"+url.PathEscape(container)+"?"+query.Encode(), nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("X-Auth-Token", h.Primary.token)
			response, err := h.Primary.rawClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			closeErr := response.Body.Close()
			if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || len(body) == 1<<20 {
				t.Fatalf("raw listing: HTTP %d read=%v close=%v size=%d", response.StatusCode, readErr, closeErr, len(body))
			}
			var rows []struct{ Name, Subdir string }
			if err := json.Unmarshal(body, &rows); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, row := range rows {
				if (row.Name == "") == (row.Subdir == "") {
					t.Fatalf("ambiguous listing row: %+v", row)
				}
				if row.Name != "" {
					got = append(got, "object:"+row.Name)
				} else {
					got = append(got, "prefix:"+row.Subdir)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("prefix %q marker %q: got %q, want %q", tc.prefix, tc.marker, got, tc.want)
			}
			if len(rows) > 0 && rows[0].Subdir != "" && rows[0].Subdir <= tc.marker {
				// Provider grouping can move an interior marker backwards;
				// the application must refuse rather than publish that page.
				page, read := h.BrowseObjects(t.Context(), h.Primary.BookmarkID, container, tc.prefix, tc.marker, 100)
				if read.Err() == nil || len(page.Rows) != 0 || page.NextMarker != "" {
					t.Fatalf("non-advancing provider page was accepted: %+v error=%v", page, read.Err())
				}
			}
		})
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}

func TestDelimiterMarkerHTTPMock(t *testing.T)      { delimiterMarkerHTTP(t, findMockHarness(t)) }
func TestDelimiterMarkerHTTPRealSwift(t *testing.T) { delimiterMarkerHTTP(t, New(t)) }

func ownMarkerBrowse(t *testing.T, h *Harness) {
	t.Helper()
	container := h.Container(h.Primary, "own-marker-browse")
	prefixes := []string{"folder/", "/", " 雪%2F//"}
	for _, prefix := range prefixes {
		h.Seed(h.Primary, container, prefix, []byte("self"), nil)
		h.Seed(h.Primary, container, prefix+"a/child", []byte("child"), nil)
		h.Seed(h.Primary, container, prefix+"z", []byte("tail"), nil)
	}
	sentinel := h.Container(h.Secondary, "own-marker-sentinel")
	h.Seed(h.Secondary, sentinel, "untouched", []byte("secondary"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	var firstRead *Receipt
	for _, prefix := range prefixes {
		t.Run(prefix, func(t *testing.T) {
			trace := limitListingPages(t, h, h.Primary, container, "short", "")
			page, receipt := h.BrowseObjects(t.Context(), h.Primary.BookmarkID, container, prefix, "", 2)
			if receipt.Err() != nil {
				t.Fatal(receipt.Err())
			}
			if len(page.Rows) != 2 || page.Rows[0].Kind != "prefix" || page.Rows[0].Name != prefix+"a/" ||
				page.Rows[1].Kind != "object" || page.Rows[1].Name != prefix+"z" || page.Rows[1].Bytes != 4 || page.NextMarker != prefix+"z" {
				t.Fatalf("own marker hid or changed its children: %+v", page)
			}
			trace.assertMarkers(t, prefix, "/", "", prefix, prefix+"a/")
			empty, end := h.BrowseObjects(t.Context(), h.Primary.BookmarkID, container, prefix, page.NextMarker, 2)
			if end.Err() != nil || len(empty.Rows) != 0 || empty.NextMarker != "" {
				t.Fatalf("listing did not terminate: %+v error=%v", empty, end.Err())
			}
			trace.assertMarkers(t, prefix, "/", "", prefix, prefix+"a/", prefix+"z")
			if firstRead == nil {
				firstRead = receipt
			}
		})
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	if !t.Failed() && firstRead != nil {
		// The reconciler accepts one action/scenario claim per test.
		firstRead.CreditRead()
	}
}

func TestOwnMarkerBrowseMock(t *testing.T)      { ownMarkerBrowse(t, findMockHarness(t)) }
func TestOwnMarkerBrowseRealSwift(t *testing.T) { ownMarkerBrowse(t, New(t)) }
