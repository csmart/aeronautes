package swiftcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeObject is one stored object in the mock cluster.
type fakeObject struct {
	bytes       int64
	contentType string
	etag        string
	sloEtag     string
}

// fakeSwift is a raw-handler mock cluster (no ServeMux: mux would
// clean/redirect paths like foo//bar, which is exactly what the encoding
// suite must prove survives). It implements v1 auth, JSON object listings
// with limit/marker/prefix/delimiter semantics, and object HEAD with
// byte-exact names recovered from the escaped path.
type fakeSwift struct {
	auth      *fakeAuth
	container string

	mu       sync.Mutex
	objects  map[string]fakeObject
	requests int
}

func newFakeSwift(t *testing.T, container string) (*fakeSwift, *Session) {
	t.Helper()
	f := &fakeSwift{
		auth:      &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"},
		container: container,
		objects:   map[string]fakeObject{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	f.auth.serverURL = srv.URL

	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	return f, s
}

func (f *fakeSwift) put(name string, o fakeObject) {
	if o.contentType == "" {
		o.contentType = "application/octet-stream"
	}
	if o.etag == "" {
		o.etag = "d41d8cd98f00b204e9800998ecf8427e"
	}
	f.mu.Lock()
	f.objects[name] = o
	f.mu.Unlock()
}

func (f *fakeSwift) handle(w http.ResponseWriter, r *http.Request) {
	ep := r.URL.EscapedPath()
	containerPath := "/v1/AUTH_acct/" + f.container
	switch {
	case ep == "/auth/v1.0":
		f.auth.handle(w, r)
	case ep == containerPath:
		f.mu.Lock()
		f.requests++
		f.mu.Unlock()
		f.handleListing(w, r)
	case strings.HasPrefix(ep, containerPath+"/"):
		name, err := url.PathUnescape(strings.TrimPrefix(ep, containerPath+"/"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.handleObject(w, r, name)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeSwift) handleObject(w http.ResponseWriter, r *http.Request, name string) {
	f.mu.Lock()
	o, ok := f.objects[name]
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", o.contentType)
	w.Header().Set("Etag", o.etag)
	w.Header().Set("Content-Length", strconv.FormatInt(o.bytes, 10))
	w.Header().Set("Last-Modified", "Fri, 28 Aug 2026 10:00:00 GMT")
	w.WriteHeader(http.StatusOK)
}

type listingRow struct {
	Name         string `json:"name,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	Hash         string `json:"hash,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	SLOEtag      string `json:"slo_etag,omitempty"`
	SubDir       string `json:"subdir,omitempty"`
}

// handleListing emulates real Swift's delimiter grouping (verified via
// §57.3): every name below a child prefix — an object named exactly at
// the boundary included — folds into one subdir row, and a marker naming
// a subdir group skips the whole group.
func (f *fakeSwift) handleListing(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 10000
	}
	marker := q.Get("marker")
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")

	f.mu.Lock()
	names := make([]string, 0, len(f.objects))
	for name := range f.objects {
		names = append(names, name)
	}
	f.mu.Unlock()
	sort.Strings(names)

	var rows []listingRow
	emittedSubdir := map[string]bool{}
	for _, name := range names {
		if len(rows) == limit {
			break
		}
		if name <= marker || !strings.HasPrefix(name, prefix) {
			continue
		}
		if delimiter != "" {
			if marker != "" && strings.HasSuffix(marker, delimiter) && strings.HasPrefix(name, marker) {
				continue
			}
			rest := name[len(prefix):]
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				subdir := prefix + rest[:idx+len(delimiter)]
				if !emittedSubdir[subdir] {
					emittedSubdir[subdir] = true
					rows = append(rows, listingRow{SubDir: subdir})
				}
				continue
			}
		}
		f.mu.Lock()
		o := f.objects[name]
		f.mu.Unlock()
		rows = append(rows, objectRow(name, o))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rows)
}

func objectRow(name string, o fakeObject) listingRow {
	return listingRow{
		Name:         name,
		Bytes:        o.bytes,
		Hash:         o.etag,
		ContentType:  o.contentType,
		LastModified: "2026-08-28T10:00:00.000000",
		SLOEtag:      o.sloEtag,
	}
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
	f, s := newFakeSwift(t, "browse")
	f.put("hello.txt", fakeObject{bytes: 5, contentType: "text/plain"})
	f.put("implied/child.txt", fakeObject{bytes: 2, contentType: "text/plain"})
	f.put("folder-marker/", fakeObject{bytes: 0, contentType: "application/directory"})
	f.put("both/", fakeObject{bytes: 0, contentType: "application/directory"})
	f.put("both/child.txt", fakeObject{bytes: 3, contentType: "text/plain"})
	f.put("weird/", fakeObject{bytes: 7, contentType: "text/plain"})
	f.put("weird/x", fakeObject{bytes: 1, contentType: "text/plain"})
	f.put("video.mp4", fakeObject{bytes: 1 << 20, contentType: "video/mp4", sloEtag: "abc"})

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
	f, s := newFakeSwift(t, "browse")
	f.put("folder-marker/", fakeObject{bytes: 0, contentType: "application/directory"})
	f.put("implied/child.txt", fakeObject{bytes: 2, contentType: "text/plain"})
	f.put("weird/", fakeObject{bytes: 7, contentType: "text/plain"})
	f.put("weird/x", fakeObject{bytes: 1, contentType: "text/plain"})
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
	f, s := newFakeSwift(t, "browse")
	f.put("photos/", fakeObject{bytes: 0, contentType: "application/directory"})
	f.put("photos/a.jpg", fakeObject{bytes: 1, contentType: "image/jpeg"})

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
	f, s := newFakeSwift(t, "browse")
	f.put("photos/2026/a.jpg", fakeObject{bytes: 1, contentType: "image/jpeg"})
	f.put("photos/2026/b.jpg", fakeObject{bytes: 2, contentType: "image/jpeg"})
	f.put("photos/2025/c.jpg", fakeObject{bytes: 3, contentType: "image/jpeg"})
	f.put("photos/readme.txt", fakeObject{bytes: 4, contentType: "text/plain"})

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
	f, s := newFakeSwift(t, "browse")
	for i := range 7 {
		f.put(fmt.Sprintf("o%02d", i), fakeObject{bytes: int64(i), contentType: "text/plain"})
	}

	entries := collectEntries(t, s, "browse", ObjectListOptions{PageSize: 3})
	if len(entries) != 7 {
		t.Fatalf("streamed %d entries, want 7", len(entries))
	}
	if f.requests != 3 {
		t.Errorf("server saw %d listing requests, want 3 (3+3+1)", f.requests)
	}

	f.requests = 0
	for entry, err := range s.Objects(context.Background(), "browse", ObjectListOptions{PageSize: 3}) {
		if err != nil {
			t.Fatal(err)
		}
		if entry.(ObjectEntry).Name == "o00" {
			break
		}
	}
	if f.requests != 1 {
		t.Errorf("server saw %d requests after early break, want 1", f.requests)
	}
}

func TestObjectsDeepListingKeepsMarkers(t *testing.T) {
	f, s := newFakeSwift(t, "browse")
	f.put("folder-marker/", fakeObject{bytes: 0, contentType: "application/directory"})
	f.put("implied/child.txt", fakeObject{bytes: 2, contentType: "text/plain"})

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

	f, s := newFakeSwift(t, "enc")
	for i, name := range names {
		f.put(name, fakeObject{bytes: int64(i + 1), contentType: "application/octet-stream"})
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
