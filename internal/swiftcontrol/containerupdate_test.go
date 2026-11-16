package swiftcontrol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/ncw/swift/v2"
)

func TestUpdateContainerAppliesDelta(t *testing.T) {
	s, conn := newWritableSession(t)
	ctx := context.Background()

	// Seed state including families the edit surface must never touch.
	if err := conn.ContainerUpdate(ctx, "media", swift.Headers{
		"X-Container-Meta-Keep":                        "old",
		"X-Container-Meta-Drop":                        "gone-soon",
		"X-Container-Meta-Access-Control-Allow-Origin": "https://app.example",
		"X-Container-Meta-Temp-URL-Key":                "sekrit",
		"X-Container-Meta-Web-Index":                   "index.html",
		"X-Container-Read":                             ".r:*",
	}); err != nil {
		t.Fatal(err)
	}

	before, err := s.ContainerDetail(ctx, "media")
	if err != nil {
		t.Fatal(err)
	}

	// A working copy built from the detail carries the static web and CORS
	// keys along (ADR 0090, ADR 0101); this edit leaves them as they were.
	result, err := s.UpdateContainer(ctx, "media", before.EditableFingerprint, ContainerEdit{
		Meta: map[string]string{
			"Keep": "new", "Added": "yes", "Web-Index": "index.html",
			"Access-Control-Allow-Origin": "https://app.example",
		},
		WriteACL:   "acct:writer",
		Versioning: ContainerVersioning{Mode: VersioningVersionsLocation, Archive: "media-versions"},
		QuotaBytes: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateApplied {
		t.Fatalf("Outcome = %q (%s)", result.Outcome, result.Mismatch)
	}
	after := result.After

	if after.Meta["Keep"] != "new" || after.Meta["Added"] != "yes" {
		t.Errorf("Meta = %v", after.Meta)
	}
	if _, still := after.Meta["Drop"]; still {
		t.Error("removed key still present")
	}
	if after.ReadACL != "" || after.WriteACL != "acct:writer" {
		t.Errorf("ACLs = %q / %q (read should be cleared, write set)", after.ReadACL, after.WriteACL)
	}
	if after.Versioning.Mode != VersioningVersionsLocation || after.Versioning.Archive != "media-versions" {
		t.Errorf("Versioning = %+v", after.Versioning)
	}
	if after.QuotaBytes != 1000 {
		t.Errorf("QuotaBytes = %d", after.QuotaBytes)
	}
	// The carried CORS row and the untouchable slot survived the delta.
	if !after.CORS || after.Meta["Access-Control-Allow-Origin"] != "https://app.example" {
		t.Errorf("CORS after a copy that kept it: CORS=%v Meta=%v", after.CORS, after.Meta)
	}
	if len(after.TempURLKeySlots) != 1 {
		t.Errorf("TempURL slots = %v, must be untouched", after.TempURLKeySlots)
	}
	if !after.StaticWeb || after.Meta["Web-Index"] != "index.html" {
		t.Errorf("static web after a copy that kept it: StaticWeb=%v Meta=%v", after.StaticWeb, after.Meta)
	}
	if len(result.TransIDs) != 3 {
		t.Errorf("TransIDs = %v", result.TransIDs)
	}
	for _, id := range result.TransIDs {
		if id == "" {
			t.Errorf("TransIDs contains an unobserved empty entry: %v", result.TransIDs)
		}
	}

	// Second edit: swap versioning mode, clear the quota, and drop the static
	// web and CORS keys from the working copy, which removes them like any
	// other key; the hidden slot is still not the edit's to remove.
	result2, err := s.UpdateContainer(ctx, "media", after.EditableFingerprint, ContainerEdit{
		Meta:       map[string]string{"Keep": "new", "Added": "yes"},
		WriteACL:   "acct:writer",
		Versioning: ContainerVersioning{Mode: VersioningHistoryLocation, Archive: "media-history"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result2.Outcome != UpdateApplied {
		t.Fatalf("Outcome = %q (%s)", result2.Outcome, result2.Mismatch)
	}
	if result2.After.Versioning.Mode != VersioningHistoryLocation {
		t.Errorf("Versioning = %+v", result2.After.Versioning)
	}
	if result2.After.QuotaBytes != 0 {
		t.Errorf("QuotaBytes = %d, want cleared", result2.After.QuotaBytes)
	}
	if result2.After.StaticWeb {
		t.Errorf("static web still configured after the working copy dropped it: %v", result2.After.Meta)
	}
	if result2.After.CORS || result2.After.Meta["Access-Control-Allow-Origin"] != "" {
		t.Errorf("CORS still configured after the working copy dropped it: %v", result2.After.Meta)
	}
	if len(result2.After.TempURLKeySlots) != 1 {
		t.Error("the TempURL slot was removed by an edit that cannot see it")
	}
}

// A working copy that names a TempURL slot or a quota key is refused before
// any I/O. The detail keeps those keys out of Meta, so the delta would
// otherwise SET the caller's value: a slot would be overwritten behind the
// redaction and a quota changed behind its typed field, each surfacing only
// as a verify mismatch after the effect (K-SEC-006, ADR 0101).
func TestUpdateContainerRejectsReservedKeys(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	conn := &swift.Connection{AuthUrl: srv.URL + "/auth/v1.0", UserName: "test:tester", ApiKey: "testing", AuthVersion: 1}
	ctx := context.Background()
	if err := conn.ContainerCreate(ctx, "media", swift.Headers{
		"X-Container-Meta-Temp-URL-Key": "sekrit",
		"X-Container-Meta-Quota-Bytes":  "500",
	}); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"Temp-URL-Key", "temp-url-key", "Temp-URL-Key-2", "Quota-Bytes", "quota-count"} {
		before := mock.Requests()
		_, err := s.UpdateContainer(ctx, "media", "", ContainerEdit{Meta: map[string]string{key: "intruder"}})
		if err == nil {
			t.Errorf("reserved key %q was admitted", key)
		}
		if got := mock.Requests(); got != before {
			t.Errorf("reserved key %q issued %d storage request(s) before refusal", key, got-before)
		}
	}

	// A normal key still round-trips, with the slot and the quota untouched.
	base, err := s.ContainerDetail(ctx, "media")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.UpdateContainer(ctx, "media", base.EditableFingerprint, ContainerEdit{
		Meta: map[string]string{"Project": "atlas"}, QuotaBytes: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateApplied || result.After.Meta["Project"] != "atlas" {
		t.Fatalf("normal edit outcome = %q meta = %v", result.Outcome, result.After.Meta)
	}
	if len(result.After.TempURLKeySlots) != 1 || result.After.QuotaBytes != 500 {
		t.Errorf("slots = %v quota = %d; want the secret slot and the quota untouched", result.After.TempURLKeySlots, result.After.QuotaBytes)
	}
}

func TestUpdateContainerConflict(t *testing.T) {
	s, conn := newWritableSession(t)
	ctx := context.Background()

	before, err := s.ContainerDetail(ctx, "media")
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.ContainerUpdate(ctx, "media", swift.Headers{"X-Container-Meta-Interloper": "yes"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.UpdateContainer(ctx, "media", before.EditableFingerprint, ContainerEdit{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateConflict || result.After != nil {
		t.Fatalf("result = %+v, want conflict without write", result)
	}
	current, _ := s.ContainerDetail(ctx, "media")
	if current.Meta["Interloper"] != "yes" {
		t.Error("conflicted update clobbered the interloper's change")
	}
}

// containerUpdateWire is a container endpoint that records HEAD and POST
// counts so a refused edit can be shown not to have reached the store.
type containerUpdateWire struct {
	heads, posts atomic.Int32
	head         func(http.Header, int32)
}

func newContainerUpdateSession(t *testing.T, wire *containerUpdateWire) *Session {
	t.Helper()
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	mux.HandleFunc("/v1/AUTH_acct/media", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			n := wire.heads.Add(1)
			h := w.Header()
			h.Set("X-Container-Object-Count", "0")
			h.Set("X-Container-Bytes-Used", "0")
			if wire.head != nil {
				wire.head(h, n)
			}
			if h.Get("X-Fail-Head") == "1" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			wire.posts.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	auth.serverURL = srv.URL
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUpdateContainerRefusesUnestablishedMode(t *testing.T) {
	wire := &containerUpdateWire{head: func(h http.Header, n int32) {
		if n == 1 {
			h.Set("X-Fail-Head", "1")
		}
	}}
	s := newContainerUpdateSession(t, wire)
	_, err := s.UpdateContainer(context.Background(), "media", "", ContainerEdit{
		Meta: map[string]string{"Note": "edited"}, Versioning: ContainerVersioning{Mode: VersioningOff},
	})
	if !errors.Is(err, ErrVersioningUnestablished) {
		t.Fatalf("unestablished HEAD = %v", err)
	}
	if wire.posts.Load() != 0 {
		t.Fatalf("POST count = %d, want 0", wire.posts.Load())
	}
	if wire.heads.Load() != 1 {
		t.Fatalf("HEAD count = %d, want 1 (the write preflight, not a separate probe)", wire.heads.Load())
	}
}

func TestUpdateContainerRefusesModernVersioning(t *testing.T) {
	wire := &containerUpdateWire{head: func(h http.Header, _ int32) {
		h.Set("X-Versions-Enabled", "true")
	}}
	s := newContainerUpdateSession(t, wire)
	_, err := s.UpdateContainer(context.Background(), "media", "", ContainerEdit{
		Meta: map[string]string{"Note": "edited"}, Versioning: ContainerVersioning{Mode: VersioningOff},
	})
	if !errors.Is(err, ErrModernVersioning) {
		t.Fatalf("modern rewrite = %v", err)
	}
	if wire.posts.Load() != 0 {
		t.Fatalf("POST count = %d, want 0", wire.posts.Load())
	}
}

func TestUpdateContainerRefusesLegacyRewriteOfModern(t *testing.T) {
	wire := &containerUpdateWire{head: func(h http.Header, _ int32) {
		h.Set("X-Versions-Enabled", "true")
	}}
	s := newContainerUpdateSession(t, wire)
	_, err := s.UpdateContainer(context.Background(), "media", "", ContainerEdit{
		Versioning: ContainerVersioning{Mode: VersioningHistoryLocation, Archive: "media-history"},
	})
	if !errors.Is(err, ErrModernVersioning) {
		t.Fatalf("legacy rewrite = %v", err)
	}
	if wire.posts.Load() != 0 {
		t.Fatalf("POST count = %d, want 0", wire.posts.Load())
	}
}

func TestCreateContainer(t *testing.T) {
	s, _ := newWritableSession(t)
	ctx := context.Background()

	detail, err := s.CreateContainer(ctx, ContainerCreateRequest{
		Name: "fresh", Policy: "gold", Meta: map[string]string{"Owner": "ops"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if detail.StoragePolicy != "gold" || detail.Meta["Owner"] != "ops" {
		t.Errorf("created detail = %+v", detail)
	}

	if _, err := s.CreateContainer(ctx, ContainerCreateRequest{Name: "fresh"}); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("recreate = %v, want ErrAlreadyExists", err)
	}
	if _, err := s.CreateContainer(ctx, ContainerCreateRequest{Name: "bad/name"}); err == nil {
		t.Error("slash-bearing container name accepted")
	}
	// The keys the editors refuse are refused at creation too, before any
	// request: a slot is a secret and a quota has its own field.
	for _, key := range []string{"Temp-URL-Key-2", "Quota-Count"} {
		if _, err := s.CreateContainer(ctx, ContainerCreateRequest{Name: "reserved", Meta: map[string]string{key: "1"}}); err == nil {
			t.Errorf("creation accepted the reserved key %q", key)
		}
	}
	if _, err := s.ContainerDetail(ctx, "reserved"); err == nil {
		t.Error("a refused creation still created the container")
	}
}

func TestMarkers(t *testing.T) {
	s, conn := newWritableSession(t)
	ctx := context.Background()

	// Create: verified marker-backed.
	detail, err := s.CreateMarker(ctx, "media", "photos/")
	if err != nil {
		t.Fatal(err)
	}
	if !detail.MarkerBacked {
		t.Fatalf("detail = %+v", detail)
	}
	if _, err := s.CreateMarker(ctx, "media", "photos/"); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("recreate marker = %v, want ErrAlreadyExists", err)
	}
	if _, err := s.CreateMarker(ctx, "media", "noslash"); err == nil {
		t.Error("marker without trailing delimiter accepted")
	}

	// A folder-suit real object must not be overwritten by a marker.
	if _, err := conn.ObjectPut(ctx, "media", "suit/", strings.NewReader("real data"), true, "", "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMarker(ctx, "media", "suit/"); err == nil || errors.Is(err, ErrAlreadyExists) {
		t.Errorf("CreateMarker over real object = %v, want refusal", err)
	}

	// Delete: marker goes, children survive.
	if _, err := conn.ObjectPut(ctx, "media", "photos/a.jpg", strings.NewReader("x"), true, "", "image/jpeg", nil); err != nil {
		t.Fatal(err)
	}
	result, err := s.DeleteName(ctx, "media", "photos/")
	if err != nil || result.Outcome != UpdateApplied || result.Gone {
		t.Fatalf("result = %+v, %v", result, err)
	}
	after, err := s.PrefixDetail(ctx, "media", "photos/")
	if err != nil {
		t.Fatal(err)
	}
	if after.SelfObject != nil {
		t.Error("marker still present after delete")
	}
	if _, err := s.ObjectDetail(ctx, "media", "photos/a.jpg"); err != nil {
		t.Errorf("child object lost with the marker: %v", err)
	}

	// An implied prefix has no marker: the store answers not found, which
	// the deletion reports as already gone.
	if result, err := s.DeleteName(ctx, "media", "photos/"); err != nil || !result.Gone {
		t.Errorf("delete of an implied prefix's marker = %+v, %v, want already gone", result, err)
	}
}

// Ceph RGW answers X-Remove-Container-Read and -Write with 204 and keeps
// the ACL, so making a public container private left it public (F36). The
// update also sends the header with an empty value, which clears an ACL on
// that store and on Swift; the mock here ignores the removal headers as
// RGW does.
func TestUpdateContainerClearsACLsWhereRemovalIsIgnored(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.IgnoresACLRemoval = true
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mock.AddContainer("media", "standard")
	before, err := s.ContainerDetail(ctx, "media")
	if err != nil {
		t.Fatal(err)
	}
	public, err := s.UpdateContainer(ctx, "media", before.EditableFingerprint, ContainerEdit{ReadACL: ".r:*,.rlistings", WriteACL: "acct:writer"})
	if err != nil || public.Outcome != UpdateApplied {
		t.Fatalf("making it public: %v %+v", err, public)
	}
	private, err := s.UpdateContainer(ctx, "media", public.After.EditableFingerprint, ContainerEdit{})
	if err != nil || private.Outcome != UpdateApplied {
		t.Fatalf("making it private: %v outcome %q (%s)", err, private.Outcome, private.Mismatch)
	}
	if private.After.ReadACL != "" || private.After.WriteACL != "" {
		t.Fatalf("after clearing, the ACLs read %q and %q", private.After.ReadACL, private.After.WriteACL)
	}
}
