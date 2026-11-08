package swiftcontrol

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// newListingServer serves one swifttest-backed session for the listing
// suite; the mock answers raw paths (no ServeMux), which is what the
// encoding cases must prove survives.
func newListingServer(t *testing.T) (*swifttest.Server, *Session) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	return mock, mustSession(t, srv.URL)
}

// listingRequests counts the container listings the mock served, so a
// pagination case can say how many pages were fetched.
func listingRequests(mock *swifttest.Server, container string) int {
	n := 0
	for _, line := range mock.RequestLog() {
		if strings.HasPrefix(line, "GET /v1/AUTH_test/"+container+"?") {
			n++
		}
	}
	return n
}

func collectEntries(t *testing.T, s *Session, container string, opts ObjectListOptions) []ListingEntry {
	t.Helper()
	var out []ListingEntry
	for entry, err := range s.Objects(context.Background(), container, opts) {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, entry)
	}
	return out
}

func TestObjectsDelimiterListing(t *testing.T) {
	f, s := newListingServer(t)
	f.PutObject("browse", "hello.txt", swifttest.Object{Bytes: 5, ContentType: "text/plain"})
	f.PutObject("browse", "implied/child.txt", swifttest.Object{Bytes: 2, ContentType: "text/plain"})
	f.PutObject("browse", "folder-marker/", swifttest.Object{Bytes: 0, ContentType: "application/directory"})
	f.PutObject("browse", "both/", swifttest.Object{Bytes: 0, ContentType: "application/directory"})
	f.PutObject("browse", "both/child.txt", swifttest.Object{Bytes: 3, ContentType: "text/plain"})
	f.PutObject("browse", "weird/", swifttest.Object{Bytes: 7, ContentType: "text/plain"})
	f.PutObject("browse", "weird/x", swifttest.Object{Bytes: 1, ContentType: "text/plain"})
	f.PutObject("browse", "video.mp4", swifttest.Object{Bytes: 1 << 20, ContentType: "video/mp4", SLOEtag: "abc"})

	entries := collectEntries(t, s, "browse", ObjectListOptions{})

	byName := map[string]ListingEntry{}
	for _, e := range entries {
		switch v := e.(type) {
		case ObjectEntry:
			if _, dup := byName["obj:"+v.Name]; dup {
				t.Errorf("duplicate object row %q", v.Name)
			}
			byName["obj:"+v.Name] = e
		case PrefixEntry:
			if _, dup := byName["pfx:"+v.Prefix]; dup {
				t.Errorf("duplicate prefix row %q", v.Prefix)
			}
			byName["pfx:"+v.Prefix] = e
		}
	}

	// Real-server grouping (§14.1): every boundary object — a lone marker,
	// a marker with children, even a folder-ish real object — folds into
	// exactly one prefix row; PrefixDetail tells them apart on selection.
	for _, prefix := range []string{"folder-marker/", "implied/", "both/", "weird/"} {
		if _, ok := byName["pfx:"+prefix].(PrefixEntry); !ok {
			t.Errorf("%s: missing PrefixEntry", prefix)
		}
		if _, stray := byName["obj:"+prefix]; stray {
			t.Errorf("boundary object %s leaked as a separate ObjectEntry", prefix)
		}
	}
	// Plain object fields and the SLO listing hint.
	if o, ok := byName["obj:hello.txt"].(ObjectEntry); !ok || o.Bytes != 5 || o.ContentType != "text/plain" || o.SLOListed {
		t.Errorf("hello.txt = %#v", byName["obj:hello.txt"])
	}
	if o, ok := byName["obj:video.mp4"].(ObjectEntry); !ok || !o.SLOListed {
		t.Errorf("video.mp4 = %#v, want SLOListed", byName["obj:video.mp4"])
	}
}

// TestPrefixDetail covers the selection-triggered HEAD that a delimiter
// listing cannot answer (§14.1/§14.2): marker-backed vs implied vs a
// folder-ish real object hiding behind the group row.
func TestPrefixDetail(t *testing.T) {
	f, s := newListingServer(t)
	f.PutObject("browse", "folder-marker/", swifttest.Object{Bytes: 0, ContentType: "application/directory"})
	f.PutObject("browse", "implied/child.txt", swifttest.Object{Bytes: 2, ContentType: "text/plain"})
	f.PutObject("browse", "weird/", swifttest.Object{Bytes: 7, ContentType: "text/plain"})
	f.PutObject("browse", "weird/x", swifttest.Object{Bytes: 1, ContentType: "text/plain"})
	ctx := context.Background()

	marker, err := s.PrefixDetail(ctx, "browse", "folder-marker/")
	if err != nil {
		t.Fatal(err)
	}
	if !marker.MarkerBacked || marker.SelfObject == nil {
		t.Errorf("folder-marker/ = %+v, want marker-backed with self object", marker)
	}

	implied, err := s.PrefixDetail(ctx, "browse", "implied/")
	if err != nil {
		t.Fatal(err)
	}
	if implied.MarkerBacked || implied.SelfObject != nil {
		t.Errorf("implied/ = %+v, want implied (no self object)", implied)
	}

	folderish, err := s.PrefixDetail(ctx, "browse", "weird/")
	if err != nil {
		t.Fatal(err)
	}
	if folderish.MarkerBacked {
		t.Error("weird/ must not classify as marker-backed from name alone")
	}
	if folderish.SelfObject == nil || folderish.SelfObject.ContentLength != 7 {
		t.Errorf("weird/ self object = %+v, want the 7-byte folder-ish object surfaced", folderish.SelfObject)
	}
}

// TestObjectsSelfRowSkipped pins the self-object rule: inside a prefix,
// the object at the prefix's own name is not a child row (it belongs to
// PrefixDetail), while Deep mode reports it verbatim for jobs.
func TestObjectsSelfRowSkipped(t *testing.T) {
	f, s := newListingServer(t)
	f.PutObject("browse", "photos/", swifttest.Object{Bytes: 0, ContentType: "application/directory"})
	f.PutObject("browse", "photos/a.jpg", swifttest.Object{Bytes: 1, ContentType: "image/jpeg"})

	entries := collectEntries(t, s, "browse", ObjectListOptions{Prefix: "photos/"})
	if len(entries) != 1 {
		t.Fatalf("delimiter view inside photos/ = %#v, want just the child", entries)
	}
	if o, ok := entries[0].(ObjectEntry); !ok || o.Name != "photos/a.jpg" {
		t.Fatalf("entry = %#v, want photos/a.jpg", entries[0])
	}

	deep := collectEntries(t, s, "browse", ObjectListOptions{Prefix: "photos/", Deep: true})
	if len(deep) != 2 || deep[0].(ObjectEntry).Name != "photos/" {
		t.Fatalf("deep view = %#v, want the marker object kept", deep)
	}
}

func TestObjectsPrefixNavigation(t *testing.T) {
	f, s := newListingServer(t)
	f.PutObject("browse", "photos/2026/a.jpg", swifttest.Object{Bytes: 1, ContentType: "image/jpeg"})
	f.PutObject("browse", "photos/2026/b.jpg", swifttest.Object{Bytes: 2, ContentType: "image/jpeg"})
	f.PutObject("browse", "photos/2025/c.jpg", swifttest.Object{Bytes: 3, ContentType: "image/jpeg"})
	f.PutObject("browse", "photos/readme.txt", swifttest.Object{Bytes: 4, ContentType: "text/plain"})

	entries := collectEntries(t, s, "browse", ObjectListOptions{Prefix: "photos/"})
	var prefixes, objects []string
	for _, e := range entries {
		switch v := e.(type) {
		case PrefixEntry:
			prefixes = append(prefixes, v.Prefix)
		case ObjectEntry:
			objects = append(objects, v.Name)
		}
	}
	if len(prefixes) != 2 || prefixes[0] != "photos/2025/" || prefixes[1] != "photos/2026/" {
		t.Errorf("prefixes = %v, want [photos/2025/ photos/2026/]", prefixes)
	}
	if len(objects) != 1 || objects[0] != "photos/readme.txt" {
		t.Errorf("objects = %v, want [photos/readme.txt]", objects)
	}
}

func TestObjectsPaginationAndEarlyBreak(t *testing.T) {
	f, s := newListingServer(t)
	for i := range 7 {
		f.PutObject("browse", fmt.Sprintf("o%02d", i), swifttest.Object{Bytes: int64(i), ContentType: "text/plain"})
	}

	entries := collectEntries(t, s, "browse", ObjectListOptions{PageSize: 3})
	if len(entries) != 7 {
		t.Fatalf("streamed %d entries, want 7", len(entries))
	}
	if got := listingRequests(f, "browse"); got != 4 {
		t.Errorf("server saw %d listing requests, want 4 (3+3+1+0)", got)
	}

	before := listingRequests(f, "browse")
	for entry, err := range s.Objects(context.Background(), "browse", ObjectListOptions{PageSize: 3}) {
		if err != nil {
			t.Fatal(err)
		}
		if entry.(ObjectEntry).Name == "o00" {
			break
		}
	}
	if got := listingRequests(f, "browse") - before; got != 1 {
		t.Errorf("server saw %d requests after early break, want 1", got)
	}
}

func TestObjectsDeepListingKeepsMarkers(t *testing.T) {
	f, s := newListingServer(t)
	f.PutObject("browse", "folder-marker/", swifttest.Object{Bytes: 0, ContentType: "application/directory"})
	f.PutObject("browse", "implied/child.txt", swifttest.Object{Bytes: 2, ContentType: "text/plain"})

	entries := collectEntries(t, s, "browse", ObjectListOptions{Deep: true})
	if len(entries) != 2 {
		t.Fatalf("deep listing = %d entries, want 2", len(entries))
	}
	for _, e := range entries {
		if _, isPrefix := e.(PrefixEntry); isPrefix {
			t.Fatalf("deep listing produced a PrefixEntry: %#v — jobs need every real object", e)
		}
	}
	names := []string{entries[0].(ObjectEntry).Name, entries[1].(ObjectEntry).Name}
	if names[0] != "folder-marker/" || names[1] != "implied/child.txt" {
		t.Fatalf("deep names = %v", names)
	}
}

// TestObjectNameEncodingRoundTrip is the §14.3 suite on the native path:
// every hostile name must list back byte-exact and be HEADable, with
// nothing silently skipped or normalized (K-TEST-001's native half).
func TestObjectNameEncodingRoundTrip(t *testing.T) {
	names := []string{
		"a b",
		"a+b",
		"a%b",
		"a?b",
		"a#b",
		"日本語",
		"foo//bar",
		"/foo",
		"foo/",
		"foo%2Fbar",
		"line\nbreak",
		"a/./b",
		"a/../b",
		"   ",
		"trailing space ",
	}

	f, s := newListingServer(t)
	for i, name := range names {
		f.PutObject("enc", name, swifttest.Object{Bytes: int64(i + 1), ContentType: "application/octet-stream"})
	}

	// Small pages force pagination markers through the hostile names too.
	var listed []string
	for entry, err := range s.Objects(context.Background(), "enc", ObjectListOptions{Deep: true, PageSize: 3}) {
		if err != nil {
			t.Fatal(err)
		}
		listed = append(listed, entry.(ObjectEntry).Name)
	}

	want := append([]string(nil), names...)
	sort.Strings(want)
	if len(listed) != len(want) {
		t.Fatalf("listed %d names, want %d: %q", len(listed), len(want), listed)
	}
	for i := range want {
		if listed[i] != want[i] {
			t.Errorf("listed[%d] = %q, want %q (byte-exact)", i, listed[i], want[i])
		}
	}

	// Client→server direction: HEAD each exact name; the mock 404s on any
	// mangled path, so success proves the encoding round-trips.
	ctx := context.Background()
	for i, name := range names {
		info, _, err := s.conn.Object(ctx, "enc", name)
		if err != nil {
			t.Errorf("HEAD %q: %v", name, err)
			continue
		}
		if info.Bytes != int64(i+1) {
			t.Errorf("HEAD %q returned bytes %d, want %d — wrong object?", name, info.Bytes, i+1)
		}
	}
}
