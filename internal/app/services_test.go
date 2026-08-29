package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// harness stands up the full backend the shell wires: SQLite, session
// secrets, bookmark store, connections, and the mock cluster.
type harness struct {
	bookmarks *BookmarkService
	browse    *BrowseService
	inspector *InspectorService
	mock      *swifttest.Server
	authURL   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	mock := swifttest.New("test:tester", "testing")
	mock.LoadCanonicalFixtures()
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)

	store := bookmarks.NewStore(d, secrets.NewSessionStore())
	connections := NewConnections(store)
	ev := evidence.NewStore(d)
	return &harness{
		bookmarks: NewBookmarkService(store),
		browse:    NewBrowseService(connections, ev),
		inspector: NewInspectorService(connections, ev),
		mock:      mock,
		authURL:   srv.URL + "/auth/v1.0",
	}
}

func (h *harness) connect(t *testing.T) AccountDTO {
	t.Helper()
	ctx := context.Background()
	saved, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "Mock",
		AuthURL:     h.authURL,
		Username:    "test:tester",
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	account, err := h.browse.Connect(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func TestBookmarkServiceRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	saved, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "Prod AU",
		AuthURL:     "https://swift.example/auth/v1.0",
		Username:    "acct:user",
	}, "swift-key")
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || !saved.HasCredential || saved.TLSMode != "system" {
		t.Fatalf("saved = %+v", saved)
	}

	// Update without a secret keeps the credential.
	saved.DisplayName = "Prod AU (renamed)"
	updated, err := h.bookmarks.Save(ctx, saved, "")
	if err != nil {
		t.Fatal(err)
	}
	if !updated.HasCredential || updated.DisplayName != "Prod AU (renamed)" {
		t.Fatalf("updated = %+v", updated)
	}

	list, err := h.bookmarks.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}
	if err := h.bookmarks.Delete(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
}

func TestBrowseRequiresConnection(t *testing.T) {
	h := newHarness(t)
	_, err := h.browse.Containers(context.Background(), "", 100)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}

func TestConnectWrongKeyFails(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	saved, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "Mock", AuthURL: h.authURL, Username: "test:tester",
	}, "WRONG")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.browse.Connect(ctx, saved.ID); err == nil {
		t.Fatal("Connect with a wrong key must fail")
	}
}

func TestConnectAndAccountPortrait(t *testing.T) {
	h := newHarness(t)
	account := h.connect(t)
	if account.Name != "AUTH_test" {
		t.Errorf("Name = %q", account.Name)
	}
	if account.Containers != 6 {
		t.Errorf("Containers = %d, want 6", account.Containers)
	}
}

func TestContainersPaging(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()

	var names []string
	marker := ""
	pages := 0
	for {
		page, err := h.browse.Containers(ctx, marker, 2)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, row := range page.Rows {
			names = append(names, row.Name)
		}
		if page.NextMarker == "" {
			break
		}
		marker = page.NextMarker
	}
	if len(names) != 6 || pages < 3 {
		t.Fatalf("paged names = %v over %d pages", names, pages)
	}
	if names[0] != ".file-segments" || names[5] != "slo-parts" {
		t.Errorf("order wrong: %v", names)
	}
}

func TestObjectsDelimiterPage(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()

	page, err := h.browse.Objects(ctx, "fixtures", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextMarker != "" {
		t.Errorf("NextMarker = %q, want exhausted", page.NextMarker)
	}
	kinds := map[string]string{}
	for _, row := range page.Rows {
		kinds[row.Name] = row.Kind
	}
	if kinds["external-dlo"] != "object" || kinds["weird/"] != "prefix" {
		t.Errorf("rows = %v", kinds)
	}

	if kinds["folder-marker/"] != "prefix" {
		t.Error("folder-marker/ must group into a prefix row (§14.1)")
	}

	// Paging across the namespace with a small limit walks to exhaustion
	// without repeating rows (marker-at-subdir group skipping).
	seen := map[string]int{}
	marker := ""
	for i := 0; i < 20; i++ {
		p, err := h.browse.Objects(ctx, "fixtures", "", marker, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range p.Rows {
			seen[row.Kind+":"+row.Name]++
		}
		if p.NextMarker == "" {
			break
		}
		marker = p.NextMarker
	}
	if len(seen) != len(page.Rows) {
		t.Errorf("paged rows = %d, want %d: %v", len(seen), len(page.Rows), seen)
	}
	for name, count := range seen {
		if count != 1 {
			t.Errorf("row %s repeated %d times", name, count)
		}
	}
}

func TestInspectorPrefixDetail(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()

	marker, err := h.inspector.Prefix(ctx, "fixtures", "folder-marker/")
	if err != nil {
		t.Fatal(err)
	}
	if !marker.MarkerBacked || marker.SelfObject == nil {
		t.Errorf("folder-marker/ = %+v, want marker-backed with self object", marker)
	}

	implied, err := h.inspector.Prefix(ctx, "fixtures", "implied/")
	if err != nil {
		t.Fatal(err)
	}
	if implied.MarkerBacked || implied.SelfObject != nil {
		t.Errorf("implied/ = %+v, want implied", implied)
	}
}

// TestClassificationLearnsFromInspection is the §65.1 Phase 2 gate at
// service level: the §58 namespace classifies correctly, and ordinary
// inspection upgrades classification through evidence (§24).
func TestClassificationLearnsFromInspection(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()

	classOf := func(name string) string {
		t.Helper()
		page, err := h.browse.Containers(ctx, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Rows {
			if row.Name == name {
				return row.Class
			}
		}
		t.Fatalf("container %s not listed", name)
		return ""
	}

	// Before any inspection: name heuristics only. The photos_segments
	// trap stays suspected — grouped, never hidden.
	if got := classOf("external-dlo-segments"); got != "suspected" {
		t.Errorf("external-dlo-segments before inspection = %q, want suspected", got)
	}
	if got := classOf("photos_segments"); got != "suspected" {
		t.Errorf("photos_segments = %q, want suspected", got)
	}
	if got := classOf("slo-parts"); got != "normal" {
		t.Errorf("slo-parts before inspection = %q, want normal (no evidence, no name match)", got)
	}
	if got := classOf("fixtures"); got != "normal" {
		t.Errorf("fixtures = %q, want normal", got)
	}

	// Inspecting the DLO confirms its segment store.
	if _, err := h.inspector.Object(ctx, "fixtures", "external-dlo"); err != nil {
		t.Fatal(err)
	}
	if got := classOf("external-dlo-segments"); got != "confirmed" {
		t.Errorf("external-dlo-segments after DLO HEAD = %q, want confirmed", got)
	}

	// Fetching the SLO manifest confirms slo-parts.
	manifest, err := h.inspector.SLOManifest(ctx, "fixtures", "external-slo")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) == 0 {
		t.Fatal("manifest came back empty")
	}
	if got := classOf("slo-parts"); got != "confirmed" {
		t.Errorf("slo-parts after manifest fetch = %q, want confirmed", got)
	}

	// photos_segments never upgrades from inspection of other things.
	if got := classOf("photos_segments"); got != "suspected" {
		t.Errorf("photos_segments after inspections = %q, must stay suspected", got)
	}
}

func TestInspectorContainerDetail(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()

	d, err := h.inspector.Container(ctx, "photos_segments")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "photos_segments" || d.ObjectCount != 3 {
		t.Errorf("detail = %+v", d)
	}
	if d.StoragePolicy != "ec42" {
		t.Errorf("StoragePolicy = %q", d.StoragePolicy)
	}
	if d.VersioningMode != "off" {
		t.Errorf("VersioningMode = %q", d.VersioningMode)
	}
	if d.Class != "suspected" {
		t.Errorf("Class = %q, want suspected", d.Class)
	}
}

func TestInspectorObjectDetail(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()

	d, err := h.inspector.Object(ctx, "fixtures", "external-dlo")
	if err != nil {
		t.Fatal(err)
	}
	if d.Class != "dlo" || d.Manifest != "external-dlo-segments/wedding-video/" {
		t.Errorf("detail = %+v", d)
	}
	if d.Meta["Shoot"] != "wedding" {
		t.Errorf("Meta = %v", d.Meta)
	}

	if _, err := h.inspector.Object(ctx, "fixtures", "does-not-exist"); err == nil {
		t.Error("missing object must error")
	}
}
