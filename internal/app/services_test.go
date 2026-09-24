package app

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// harness stands up the full backend the shell wires: SQLite, session
// secrets, bookmark store, connections, and the mock cluster.
type harness struct {
	bookmarks *BookmarkService
	browse    *BrowseService
	inspector *InspectorService
	mutate    *MutateService
	auditSvc  *AuditService
	transfer  *TransferService
	jobsSvc   *JobService
	evidence  *evidence.Store
	db        *db.DB
	dbPath    string
	mock      *swifttest.Server
	authURL   string
	connected string
}

// portrait is the active connection's bookmark and generation, the scope
// a caller names on a write (ADR 0079).
func (h *harness) portrait() ListingScopeDTO {
	id, generation, ok := h.transfer.connections.activeEndpoint()
	if !ok {
		return ListingScopeDTO{}
	}
	return ListingScopeDTO{BookmarkID: id, Generation: generation}
}

// transferService builds the transfer service with evidence wired the
// way main.go does.
func transferService(connections *Connections, jobStore *jobs.Store, auditStore *audit.Store, ev *evidence.Store) *TransferService {
	service := NewTransferService(connections, jobStore, auditStore)
	service.setEvidenceStore(ev)
	return service
}

func newHarness(t *testing.T) *harness {
	return newHarnessWithInfo(t, "")
}

// newHarnessWithInfo lets a test serve a custom /info document — the
// capability-gating tests need clusters that advertise less.
func newHarnessWithInfo(t *testing.T, infoJSON string) *harness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	mock := swifttest.New("test:tester", "testing")
	mock.InfoJSON = infoJSON
	mock.LoadCanonicalFixtures()
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)

	store := bookmarks.NewStore(d, secrets.NewSessionStore())
	connections := NewConnections(store)
	ev := evidence.NewStore(d)
	auditStore := audit.NewStore(d)
	jobStore := jobs.NewStore(d)
	return &harness{
		bookmarks: NewBookmarkService(store, connections, jobStore),
		browse:    NewBrowseService(connections, ev),
		inspector: NewInspectorService(connections, ev),
		mutate:    confirmedMutateService(connections, auditStore, ev),
		auditSvc:  NewAuditService(auditStore),
		transfer:  transferService(connections, jobStore, auditStore, ev),
		jobsSvc:   NewJobService(jobStore),
		evidence:  ev,
		db:        d,
		dbPath:    path,
		mock:      mock,
		authURL:   srv.URL + "/auth/v1.0",
	}
}

func (h *harness) bookmarkID() string {
	if id := h.browse.connections.ActiveBookmarkID(); id != "" {
		return id
	}
	return h.connected
}

func (h *harness) ensureBookmark(t *testing.T) string {
	t.Helper()
	if id := h.bookmarkID(); id != "" {
		return id
	}
	saved, err := h.bookmarks.Save(t.Context(), BookmarkDTO{
		DisplayName: "Fixture", AuthURL: h.authURL, Username: "test:tester",
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	h.connected = saved.ID
	return saved.ID
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
	h.connected = saved.ID
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
	_, err := h.browse.Containers(context.Background(), "any-bookmark", "", 100)
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
		page, err := h.browse.Containers(ctx, h.browse.connections.ActiveBookmarkID(), marker, 2)
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

	page, err := h.browse.Objects(ctx, h.browse.connections.ActiveBookmarkID(), "fixtures", "", "", 100)
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
		p, err := h.browse.Objects(ctx, h.browse.connections.ActiveBookmarkID(), "fixtures", "", marker, 2)
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

	marker, err := h.inspector.Prefix(ctx, h.bookmarkID(), "fixtures", "folder-marker/")
	if err != nil {
		t.Fatal(err)
	}
	if !marker.MarkerBacked || marker.SelfObject == nil {
		t.Errorf("folder-marker/ = %+v, want marker-backed with self object", marker)
	}

	implied, err := h.inspector.Prefix(ctx, h.bookmarkID(), "fixtures", "implied/")
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
		page, err := h.browse.Containers(ctx, h.browse.connections.ActiveBookmarkID(), "", 100)
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
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", "external-dlo"); err != nil {
		t.Fatal(err)
	}
	if got := classOf("external-dlo-segments"); got != "confirmed" {
		t.Errorf("external-dlo-segments after DLO HEAD = %q, want confirmed", got)
	}

	// Fetching the SLO manifest confirms slo-parts.
	manifest, err := h.inspector.SLOManifest(ctx, h.bookmarkID(), "fixtures", "external-slo")
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

	d, err := h.inspector.Container(ctx, h.bookmarkID(), "photos_segments")
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

	d, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", "external-dlo")
	if err != nil {
		t.Fatal(err)
	}
	if d.Class != "dlo" || d.Manifest != "external-dlo-segments/wedding-video/" {
		t.Errorf("detail = %+v", d)
	}
	if d.Meta["Shoot"] != "wedding" {
		t.Errorf("Meta = %v", d.Meta)
	}

	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", "does-not-exist"); err == nil {
		t.Error("missing object must error")
	}
}

func TestCapabilitiesAtConnect(t *testing.T) {
	h := newHarness(t)
	h.connect(t)

	caps, err := h.browse.Capabilities(context.Background(), h.bookmarkID())
	if err != nil {
		t.Fatal(err)
	}
	if caps.Source != "advertised" {
		t.Errorf("Source = %q", caps.Source)
	}
	names := map[string]bool{}
	var hasDefault bool
	for _, p := range caps.StoragePolicies {
		names[p.Name] = true
		hasDefault = hasDefault || p.Default
	}
	if !names["standard"] || !names["gold"] || !hasDefault {
		t.Errorf("StoragePolicies = %+v", caps.StoragePolicies)
	}
	if len(caps.VersioningFlags) != 2 {
		t.Errorf("VersioningFlags = %v", caps.VersioningFlags)
	}
	if !caps.SLOSupported || caps.MaxFileSize <= 0 {
		t.Errorf("caps = %+v", caps)
	}
}

func TestMutateObjectMetadataWithAudit(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()

	before, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", "normal/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	edit := ObjectMetadataEditDTO{Meta: map[string]string{}}
	for k, v := range before.Meta {
		edit.Meta[k] = v
	}
	edit.Meta["Camera"] = "sony-fx6"

	result, err := h.mutate.UpdateObjectMetadata(ctx, h.portrait(), "fixtures", "normal/hello.txt", "", edit)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "applied" || result.ObjectAfter == nil {
		t.Fatalf("result = %+v", result)
	}
	if result.ObjectAfter.Meta["Origin"] != "fixture" || result.ObjectAfter.Meta["Camera"] != "sony-fx6" {
		t.Errorf("after = %v", result.ObjectAfter.Meta)
	}
	if len(result.TransIDs) != 3 {
		t.Errorf("TransIDs = %v", result.TransIDs)
	}

	// A stale-fingerprint attempt records a conflict.
	stale, err := h.mutate.UpdateObjectMetadata(ctx, h.portrait(), "fixtures", "normal/hello.txt", "not-the-fingerprint", edit)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Outcome != "conflict" {
		t.Errorf("stale outcome = %q", stale.Outcome)
	}

	events, err := h.auditSvc.RecentFor(ctx, h.bookmarkID(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2", len(events))
	}
	if events[0].Result != "conflict" || events[1].Result != "applied" {
		t.Errorf("audit results = %q, %q", events[0].Result, events[1].Result)
	}
	if events[1].Operation != "object.metadata.update" || events[1].Resource != "fixtures/normal/hello.txt" {
		t.Errorf("audit event = %+v", events[1])
	}
	if len(events[1].TransIDs) != 3 {
		t.Errorf("audit TransIDs = %v", events[1].TransIDs)
	}
}

func TestCreateContainerPolicyGate(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()

	if _, err := h.mutate.CreateContainer(ctx, h.portrait(), ContainerCreateDTO{Name: "gated", Policy: "nope"}); err == nil {
		t.Fatal("unadvertised policy must be refused before touching the cluster")
	}

	detail, err := h.mutate.CreateContainer(ctx, h.portrait(), ContainerCreateDTO{Name: "gated", Policy: "gold"})
	if err != nil {
		t.Fatal(err)
	}
	if detail.StoragePolicy != "gold" {
		t.Errorf("policy = %q", detail.StoragePolicy)
	}

	events, _ := h.auditSvc.RecentFor(ctx, h.bookmarkID(), 5)
	if len(events) == 0 || events[0].Operation != "container.create" || events[0].Result != "applied" {
		t.Errorf("audit = %+v", events)
	}
}

func TestUpdateContainerHasNoSeparateVersioningProbe(t *testing.T) {
	src, err := os.ReadFile("mutateservice.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(src, []byte("currentVersioningMode")) {
		t.Fatal("a separate versioning probe treats a failed HEAD as editable; the write preflight must establish the mode")
	}
}

func TestVersioningGate(t *testing.T) {
	// A cluster that advertises /info WITHOUT versioned_writes: the mode
	// is KNOWN-unsupported and must refuse before the wire.
	restricted := newHarnessWithInfo(t, `{"swift": {"max_file_size": 100}}`)
	restricted.connect(t)
	ctx := context.Background()

	_, err := restricted.mutate.UpdateContainer(ctx, restricted.portrait(), "fixtures", "", ContainerEditDTO{
		VersioningMode: "history_location", VersioningArchive: "arch",
	})
	if err == nil {
		t.Fatal("versioning on a known-unsupporting cluster must refuse")
	}

	// The default mock advertises both flags: allowed.
	h := newHarness(t)
	h.connect(t)
	if _, err := h.mutate.CreateContainer(ctx, h.portrait(), ContainerCreateDTO{Name: "arch"}); err != nil {
		t.Fatal(err)
	}
	result, err := h.mutate.UpdateContainer(ctx, h.portrait(), "fixtures", "", ContainerEditDTO{
		VersioningMode: "history_location", VersioningArchive: "arch",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "applied" || result.ContainerAfter.VersioningMode != "history_location" {
		t.Errorf("result = %+v", result)
	}
}

func TestMarkerServiceWithAudit(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()

	created, err := h.mutate.CreateMarker(ctx, h.portrait(), "fixtures", "newdir/")
	if err != nil {
		t.Fatal(err)
	}
	if !created.MarkerBacked {
		t.Errorf("created = %+v", created)
	}
	if err := h.mutate.DeleteMarker(ctx, "fixtures", "newdir/"); err != nil {
		t.Fatal(err)
	}
	// Refusals are audited as failures too.
	if err := h.mutate.DeleteMarker(ctx, "fixtures", "fake/"); err == nil {
		t.Fatal("folder-suit delete must refuse")
	}

	events, _ := h.auditSvc.RecentFor(ctx, h.bookmarkID(), 5)
	if len(events) != 3 {
		t.Fatalf("audit events = %d, want 3", len(events))
	}
	if events[0].Operation != "marker.delete" || events[0].Result != "failed" {
		t.Errorf("refusal audit = %+v", events[0])
	}
	if events[1].Operation != "marker.delete" || events[1].Result != "applied" {
		t.Errorf("delete audit = %+v", events[1])
	}
	if events[2].Operation != "marker.create" || events[2].Result != "applied" {
		t.Errorf("create audit = %+v", events[2])
	}
}

func TestDLOSegmentsService(t *testing.T) {
	h := newHarness(t)
	h.connect(t)

	preview, err := h.inspector.DLOSegments(context.Background(), h.bookmarkID(), "external-dlo-segments/wedding-video/")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Segments) != 3 || preview.HasMore {
		t.Fatalf("preview = %d segments, hasMore=%v", len(preview.Segments), preview.HasMore)
	}
	if preview.Container != "external-dlo-segments" || preview.Segments[0].Kind != "object" {
		t.Errorf("preview = %+v", preview)
	}
}

func TestDeleteManifestService(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()

	// A plain object refuses and audits as failed.
	if _, err := h.mutate.DeleteManifest(ctx, "fixtures", "empty-object"); err == nil {
		t.Fatal("plain object accepted")
	}

	deleted, err := h.mutate.DeleteManifest(ctx, "fixtures", "external-dlo")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Class != "dlo" {
		t.Errorf("deleted = %+v", deleted)
	}

	// Segments survive: the store still lists all three under the prefix.
	inner, err := h.browse.Objects(ctx, h.browse.connections.ActiveBookmarkID(), "external-dlo-segments", "wedding-video/", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(inner.Rows) != 3 {
		t.Errorf("segments after manifest delete = %d, want 3", len(inner.Rows))
	}

	events, err := h.auditSvc.RecentFor(ctx, h.bookmarkID(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Operation != "manifest.delete" || events[0].Result != "applied" {
		t.Errorf("audit[0] = %+v", events[0])
	}
	if events[1].Operation != "manifest.delete" || events[1].Result != "failed" {
		t.Errorf("audit[1] = %+v", events[1])
	}
	if !strings.Contains(events[0].Summary, "segments remain at external-dlo-segments/wedding-video/") {
		t.Errorf("summary = %q", events[0].Summary)
	}
}

func TestBridgeSurfaceCarriesNoWiringHooks(t *testing.T) {
	// G19 mechanized: the Wails binding generator exposes a bound
	// service's exported METHODS — none of the composition hooks may
	// ever be one again.
	// Internal-maintenance/composition verbs must never be exported on a
	// bound service — the Wails generator would surface them to the
	// page. A prefix guard (not a hand-maintained denylist) so a NEW
	// exported hook is caught the moment it appears (review R-13 — the
	// old denylist missed RecoverySweepStep in the very commit that
	// added it). allowed lists the genuine bridge actions that happen to
	// start with a guarded verb.
	guardedPrefixes := []string{"Set", "Wire", "Sweep", "Recovery", "Run", "Force", "Recommendation"}
	allowed := map[string]bool{"RunBenchmark": true, "Set": true} // Set = SettingsService.Set, the settings-save bridge action
	for _, subject := range []any{&TransferService{}, &BenchmarkService{}, &SettingsService{}, &DeleteService{}, &MutateService{}, &BookmarkService{}} {
		typ := reflect.TypeOf(subject)
		for i := 0; i < typ.NumMethod(); i++ {
			name := typ.Method(i).Name
			if allowed[name] {
				continue
			}
			for _, prefix := range guardedPrefixes {
				if strings.HasPrefix(name, prefix) {
					t.Errorf("%s exports maintenance-shaped method %s: it would be bound to the webview (unexport it or add to allowed)", typ, name)
				}
			}
		}
	}
}

func TestJobCeilingSurvivesResize(t *testing.T) {
	// G24/H-16: lowering the ceiling under running jobs must not admit
	// new work until enough of them finish — the old channel swap let
	// running jobs release into an orphaned channel while new jobs used
	// an empty fresh one.
	runner := newJobRunner(nil, nil)
	// The production ceiling is one (ADR 0047). This raises it in-package
	// so the case can still express its own setup. The exemption exists to
	// preserve the only detector of the count-based admission correction
	// below, not to make a test pass: with a clamp against the constant
	// this case could not admit two, and the proof that a lowered ceiling
	// holds running jobs accountable would go with it.
	runner.ceiling = 2
	runner.setMaxActiveJobs(2)
	ctx := context.Background()
	if !runner.acquireSlot(ctx) || !runner.acquireSlot(ctx) {
		t.Fatal("two slots under a ceiling of two must admit")
	}
	// Lower the ceiling with both running: nothing further admits.
	runner.setMaxActiveJobs(1)
	admitted := make(chan struct{})
	go func() {
		if runner.acquireSlot(ctx) {
			close(admitted)
		}
	}()
	select {
	case <-admitted:
		t.Fatal("third job admitted over a lowered ceiling with two running")
	case <-time.After(100 * time.Millisecond):
	}
	// One finishes: still over the new ceiling of one → no admit.
	runner.releaseSlot()
	select {
	case <-admitted:
		t.Fatal("job admitted while the running count still met the lowered ceiling")
	case <-time.After(100 * time.Millisecond):
	}
	// The second finishes: now under the ceiling → the queued job runs.
	runner.releaseSlot()
	select {
	case <-admitted:
	case <-time.After(2 * time.Second):
		t.Fatal("queued job never admitted after capacity freed")
	}
	// A queued job's cancellation releases it without admission.
	cancelCtx, cancel := context.WithCancel(ctx)
	refused := make(chan bool, 1)
	go func() { refused <- !runner.acquireSlot(cancelCtx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case ok := <-refused:
		if !ok {
			t.Fatal("cancelled queued acquire reported admission")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled queued acquire never returned")
	}
}

func TestBookmarkSaveInvalidatesCachedSession(t *testing.T) {
	// H-12, second attempt with a test this time: after editing a
	// bookmark's endpoint, SessionFor must open against the NEW cluster,
	// and the active browse pointer must not survive the edit.
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	mockA := swifttest.New("test:tester", "testing")
	srvA := httptest.NewServer(mockA)
	t.Cleanup(srvA.Close)
	mockB := swifttest.New("test:tester", "testing")
	srvB := httptest.NewServer(mockB)
	t.Cleanup(srvB.Close)

	store := bookmarks.NewStore(d, secrets.NewSessionStore())
	connections := NewConnections(store)
	service := NewBookmarkService(store, connections, jobs.NewStore(d))
	ctx := context.Background()
	saved, err := service.Save(ctx, BookmarkDTO{
		DisplayName: "primary", AuthURL: srvA.URL + "/auth/v1.0", Username: "test:tester",
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	if err := connections.Connect(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	first, err := connections.SessionFor(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldURL := first.StorageURL()

	saved.AuthURL = srvB.URL + "/auth/v1.0"
	if _, err := service.Save(ctx, saved, "testing"); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Session(); err == nil {
		t.Fatal("active session survived the endpoint edit")
	}
	second, err := connections.SessionFor(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.StorageURL() == oldURL {
		t.Fatalf("SessionFor still answers the OLD cluster %s after the edit", oldURL)
	}
}

func TestSymlinkMetadataEditRefused(t *testing.T) {
	// F-05: the editor must refuse a followed symlink outright — on
	// real Swift the POST would mutate the LINK with target-derived
	// metadata and answer 307 (mimicked by the mock).
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	h.mock.PutObject("media", "real.txt", swifttest.Object{Bytes: 12, ETag: "aa", ContentType: "text/plain", Data: []byte("target bytes")})
	h.mock.PutObject("media", "link.txt", swifttest.Object{ContentType: "text/plain", SymlinkTarget: "media/real.txt"})

	_, err := h.mutate.UpdateObjectMetadata(ctx, h.portrait(), "media", "link.txt", "", ObjectMetadataEditDTO{
		Meta: map[string]string{"Team": "ops"},
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink edit = %v, want refusal", err)
	}
	// Nothing was written to the link.
	session, err := h.transfer.connections.Session()
	if err != nil {
		t.Fatal(err)
	}
	link, err := session.SymlinkGet(ctx, "media", "link.txt")
	if err != nil {
		t.Fatal(err)
	}
	if link.Meta["Team"] != "" {
		t.Fatalf("refusal still wrote to the link: %+v", link.Meta)
	}
}

func TestPublicAclRequiresAcknowledgement(t *testing.T) {
	// grok follow-up 3: the Go side refuses a world-readable ACL without
	// the explicit ack — a bypassed/compromised webview cannot quietly
	// publish a container.
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	h.mock.AddContainer("private", "standard")

	_, err := h.mutate.UpdateContainer(ctx, h.portrait(), "private", "", ContainerEditDTO{ReadACL: ".r:*"})
	if err == nil || !strings.Contains(err.Error(), "publicly readable") {
		t.Fatalf("public ACL without ack = %v, want refusal", err)
	}
	// With the acknowledgement it proceeds (a private ACL never needs it).
	if _, err := h.mutate.UpdateContainer(ctx, h.portrait(), "private", "", ContainerEditDTO{ReadACL: ".r:*", AckPublic: true}); err != nil {
		t.Fatalf("acknowledged public ACL refused: %v", err)
	}
	if _, err := h.mutate.UpdateContainer(ctx, h.portrait(), "private", "", ContainerEditDTO{ReadACL: ".r:example.com"}); err != nil {
		t.Fatalf("non-public ACL refused: %v", err)
	}
}

// confirmerFunc answers the native public-read confirmation in tests.
type confirmerFunc func(ctx context.Context, container, account string) (bool, error)

func (f confirmerFunc) ConfirmPublicRead(ctx context.Context, container, account string) (bool, error) {
	return f(ctx, container, account)
}

// confirmedMutateService wires a confirmer that always says yes, as the
// shell's native dialog would once the operator answers; the refusal
// paths have their own tests.
func confirmedMutateService(connections *Connections, auditStore *audit.Store, ev *evidence.Store) *MutateService {
	s := NewMutateService(connections, auditStore, ev)
	s.setConfirmer(confirmerFunc(func(context.Context, string, string) (bool, error) { return true, nil }))
	return s
}
