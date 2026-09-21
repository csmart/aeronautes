package swiftcontrol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

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
		"X-Container-Read":                             ".r:*",
	}); err != nil {
		t.Fatal(err)
	}

	before, err := s.ContainerDetail(ctx, "media")
	if err != nil {
		t.Fatal(err)
	}

	result, err := s.UpdateContainer(ctx, "media", before.EditableFingerprint, ContainerEdit{
		Meta:       map[string]string{"Keep": "new", "Added": "yes"},
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
	// The untouchable families survived the delta.
	if !after.CORS {
		t.Error("CORS configuration was destroyed by an unrelated edit")
	}
	if len(after.TempURLKeySlots) != 1 {
		t.Errorf("TempURL slots = %v, must be untouched", after.TempURLKeySlots)
	}
	if len(result.TransIDs) != 3 {
		t.Errorf("TransIDs = %v", result.TransIDs)
	}
	for _, id := range result.TransIDs {
		if id == "" {
			t.Errorf("TransIDs contains an unobserved empty entry: %v", result.TransIDs)
		}
	}

	// Second edit: swap versioning mode and clear the quota.
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

	// A folder-suit real object must be neither overwritten nor deleted
	// through the marker path.
	if _, err := conn.ObjectPut(ctx, "media", "suit/", strings.NewReader("real data"), true, "", "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMarker(ctx, "media", "suit/"); err == nil || errors.Is(err, ErrAlreadyExists) {
		t.Errorf("CreateMarker over real object = %v, want refusal", err)
	}
	if err := s.DeleteMarker(ctx, "media", "suit/"); err == nil {
		t.Error("DeleteMarker deleted a folder-suit real object")
	}

	// Delete: marker goes, children survive.
	if _, err := conn.ObjectPut(ctx, "media", "photos/a.jpg", strings.NewReader("x"), true, "", "image/jpeg", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMarker(ctx, "media", "photos/"); err != nil {
		t.Fatal(err)
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

	// Deleting an implied prefix's marker is not-found, not silent success.
	var se *SwiftError
	if err := s.DeleteMarker(ctx, "media", "photos/"); !errors.As(err, &se) || se.Kind != KindNotFound {
		t.Errorf("delete implied marker = %v, want not_found", err)
	}
}
