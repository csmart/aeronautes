package integration

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/deletion"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	localtransfer "github.com/csmart/aeronautes/internal/transfer/local"
	"github.com/csmart/aeronautes/internal/transfer/plan"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
	"github.com/ncw/swift/v2"
)

var shared struct {
	once   sync.Once
	h      *Harness
	skip   string
	err    error
	loaded bool
}

// harness lazily builds one fixture namespace shared by every test in
// the run; TestMain tears it down.
func harness(t *testing.T) *Harness {
	t.Helper()
	shared.once.Do(func() {
		shared.h, shared.skip, shared.err = newHarness(context.Background())
		shared.loaded = shared.h != nil
	})
	if shared.skip != "" {
		t.Skipf("integration suite skipped: %s", shared.skip)
	}
	if shared.err != nil {
		t.Fatalf("integration harness: %v", shared.err)
	}
	return shared.h
}

func TestMain(m *testing.M) {
	code := m.Run()
	if shared.loaded {
		shared.h.teardown(context.Background())
	}
	os.Exit(code)
}

func TestPortraitAndToken(t *testing.T) {
	h := harness(t)
	ctx := context.Background()

	p, err := h.Session.AccountPortrait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Containers < 5 {
		t.Errorf("Containers = %d, want at least this run's 5", p.Containers)
	}
	if p.TransID == "" {
		t.Error("TransID must be captured from a real response")
	}

	// SAIO's TempAuth advertises X-Auth-Token-Expires (§26.2 rung 1).
	expires := h.Session.TokenExpires()
	if expires.IsZero() {
		t.Error("TokenExpires must be captured against SAIO")
	} else if until := time.Until(expires); until < time.Minute || until > 48*time.Hour {
		t.Errorf("token expiry %v from now is implausible", until)
	}
}

func TestCapabilities(t *testing.T) {
	h := harness(t)
	caps, err := h.Session.FetchCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if caps.Source != swiftcontrol.SourceAdvertised {
		t.Fatalf("Source = %v, want advertised from a real /info", caps.Source)
	}
	if caps.MaxFileSize() <= 0 {
		t.Error("MaxFileSize must be advertised")
	}
	if caps.State("slo") != swiftcontrol.CapabilitySupported {
		t.Errorf("slo state = %v, SAIO advertises it", caps.State("slo"))
	}
}

func TestContainersPaging(t *testing.T) {
	h := harness(t)
	var names []string
	for c, err := range h.Session.Containers(context.Background(), swiftcontrol.ListOptions{
		Prefix: h.Prefix, PageSize: 2,
	}) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, c.Name)
	}
	want := []string{
		h.Container("encoding"),
		h.Container("external-dlo-segments"),
		h.Container("fixtures"),
		h.Container("photos_segments"),
		h.Container("slo-parts"),
	}
	if len(names) != len(want) {
		t.Fatalf("containers = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("container %d = %q, want %q", i, names[i], want[i])
		}
	}
}

func collect(t *testing.T, h *Harness, opts swiftcontrol.ObjectListOptions) []string {
	t.Helper()
	var rows []string
	for e, err := range h.Session.Objects(context.Background(), h.Container("fixtures"), opts) {
		if err != nil {
			t.Fatal(err)
		}
		switch v := e.(type) {
		case swiftcontrol.ObjectEntry:
			rows = append(rows, "obj:"+v.Name)
		case swiftcontrol.PrefixEntry:
			rows = append(rows, "pfx:"+v.Prefix)
		}
	}
	return rows
}

func TestDelimiterSemanticsRealSwift(t *testing.T) {
	h := harness(t)
	// Real Swift folds every boundary object — the fake/ folder-ish real
	// object and the folder-marker/ marker itself — into the group row;
	// PrefixDetail (below) is how they are told apart.
	want := []string{
		"obj:empty-object",
		"obj:expiring",
		"obj:external-dlo",
		"obj:external-slo",
		"pfx:fake/",
		"pfx:folder-marker/",
		"pfx:implied/",
		"pfx:normal/",
		"obj:tagged.txt",
		"pfx:unicode/",
		"pfx:weird/",
	}
	got := collect(t, h, swiftcontrol.ObjectListOptions{})
	if len(got) != len(want) {
		t.Fatalf("rows = %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The same view paged hard: tiny pages, no repeats, same rows.
	paged := collect(t, h, swiftcontrol.ObjectListOptions{PageSize: 3})
	if len(paged) != len(want) {
		t.Fatalf("paged rows = %v, want %d rows", paged, len(want))
	}
	seen := map[string]bool{}
	for _, row := range paged {
		if seen[row] {
			t.Errorf("row %q repeated across pages", row)
		}
		seen[row] = true
	}
}

// TestPrefixDetailRealSwift proves the selection-triggered HEAD tells
// apart what the delimiter listing folds together (§14.1).
func TestPrefixDetailRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	fixtures := h.Container("fixtures")

	marker, err := h.Session.PrefixDetail(ctx, fixtures, "folder-marker/")
	if err != nil {
		t.Fatal(err)
	}
	if !marker.MarkerBacked || marker.SelfObject == nil {
		t.Errorf("folder-marker/ = %+v, want marker-backed", marker)
	}

	implied, err := h.Session.PrefixDetail(ctx, fixtures, "implied/")
	if err != nil {
		t.Fatal(err)
	}
	if implied.MarkerBacked || implied.SelfObject != nil {
		t.Errorf("implied/ = %+v, want implied", implied)
	}

	folderish, err := h.Session.PrefixDetail(ctx, fixtures, "fake/")
	if err != nil {
		t.Fatal(err)
	}
	if folderish.MarkerBacked {
		t.Error("fake/ must not classify as marker-backed: it has real bytes")
	}
	if folderish.SelfObject == nil || folderish.SelfObject.ContentLength == 0 {
		t.Errorf("fake/ self object = %+v, want the folder-ish real object surfaced", folderish.SelfObject)
	}

	// Inside fake/, the self object is not a child row; the real child is.
	var inside []string
	for e, err := range h.Session.Objects(ctx, fixtures, swiftcontrol.ObjectListOptions{Prefix: "fake/"}) {
		if err != nil {
			t.Fatal(err)
		}
		if o, ok := e.(swiftcontrol.ObjectEntry); ok {
			inside = append(inside, o.Name)
		}
	}
	if len(inside) != 1 || inside[0] != "fake/child.txt" {
		t.Errorf("inside fake/ = %v, want just fake/child.txt", inside)
	}
}

func TestEncodingSuiteRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()

	var listed []string
	for e, err := range h.Session.Objects(ctx, h.Container("encoding"), swiftcontrol.ObjectListOptions{
		Deep: true, PageSize: 3,
	}) {
		if err != nil {
			t.Fatal(err)
		}
		if o, ok := e.(swiftcontrol.ObjectEntry); ok {
			listed = append(listed, o.Name)
		}
	}

	want := append([]string(nil), EncodingNames...)
	sort.Strings(want)
	if len(listed) != len(want) {
		t.Fatalf("listed %d names, want %d: %q", len(listed), len(want), listed)
	}
	for i := range want {
		if listed[i] != want[i] {
			t.Errorf("name %d = %q, want %q (byte-exact)", i, listed[i], want[i])
		}
	}

	// And back the other way: every hostile name HEADs.
	for _, name := range EncodingNames {
		d, err := h.Session.ObjectDetail(ctx, h.Container("encoding"), name)
		if err != nil {
			t.Errorf("HEAD %q: %v", name, err)
			continue
		}
		if d.ContentLength != int64(len("payload:"+name)) {
			t.Errorf("HEAD %q: ContentLength %d, want %d", name, d.ContentLength, len("payload:"+name))
		}
	}
}

func TestObjectHeadRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	fixtures := h.Container("fixtures")

	tagged, err := h.Session.ObjectDetail(ctx, fixtures, "tagged.txt")
	if err != nil {
		t.Fatal(err)
	}
	if tagged.Class != swiftcontrol.ClassPlain {
		t.Errorf("tagged.txt Class = %q", tagged.Class)
	}
	if tagged.Meta["Shoot"] != "wedding" || tagged.Meta["Location"] != "sydney" {
		t.Errorf("Meta = %v; server canonicalises case, categorisation must too", tagged.Meta)
	}
	if tagged.EditableFingerprint == "" || tagged.TransID == "" {
		t.Errorf("fingerprint/transid missing: %+v", tagged)
	}

	expiring, err := h.Session.ObjectDetail(ctx, fixtures, "expiring")
	if err != nil {
		t.Fatal(err)
	}
	if until := time.Until(expiring.DeleteAt); until < 50*time.Minute || until > 70*time.Minute {
		t.Errorf("DeleteAt %v from now, want ~1h", until)
	}

	dlo, err := h.Session.ObjectDetail(ctx, fixtures, "external-dlo")
	if err != nil {
		t.Fatal(err)
	}
	if dlo.Class != swiftcontrol.ClassDLO {
		t.Errorf("external-dlo Class = %q", dlo.Class)
	}
	if dlo.Manifest != h.Container("external-dlo-segments")+"/wedding-video/" {
		t.Errorf("Manifest = %q", dlo.Manifest)
	}
	if dlo.ContentLength != 3000 {
		t.Errorf("DLO logical size = %d, want 3000 (assembled)", dlo.ContentLength)
	}

	slo, err := h.Session.ObjectDetail(ctx, fixtures, "external-slo")
	if err != nil {
		t.Fatal(err)
	}
	if slo.Class != swiftcontrol.ClassSLO {
		t.Errorf("external-slo Class = %q", slo.Class)
	}
	if slo.ContentLength != 1000 {
		t.Errorf("SLO logical size = %d, want 1000", slo.ContentLength)
	}

	// The exact manifest round-trips through ?multipart-manifest=get.
	manifest, err := h.Session.SLOManifest(ctx, fixtures, "external-slo")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 2 {
		t.Fatalf("manifest entries = %d, want 2", len(manifest.Entries))
	}
	first, ok := manifest.Entries[0].(swiftcontrol.ObjectSegment)
	if !ok || first.Path.Container != h.Container("slo-parts") ||
		first.Path.Object != "backup/part-000001" || first.Size != 500 {
		t.Errorf("manifest entry 0 = %#v", manifest.Entries[0])
	}

	// The listing hints SLO for the manifest row (§14.2: hint, not truth).
	for e, err := range h.Session.Objects(ctx, fixtures, swiftcontrol.ObjectListOptions{}) {
		if err != nil {
			t.Fatal(err)
		}
		if o, ok := e.(swiftcontrol.ObjectEntry); ok && o.Name == "external-slo" && !o.SLOListed {
			t.Error("listing row for external-slo should carry the slo_etag hint")
		}
	}
}

// TestMetadataUpdateGateRealSwift is the Phase 3 gate (§60) against real
// Swift: editing one metadata key erases nothing else, a DLO stays a
// DLO — and the underlying trap is demonstrated to be real, so the
// preservation is provably doing work.
func TestMetadataUpdateGateRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	fixtures := h.Container("fixtures")

	// A discovered nuance, pinned: real Swift 404s a HEAD of a DLO
	// manifest whose segment container does not exist — a dangling DLO
	// looks *absent*, not broken.
	if err := h.put(ctx, fixtures, "dangling-dlo", nil, "video/mp4", swift.Headers{
		"X-Object-Manifest": "no-such-container/prefix/",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Session.ObjectDetail(ctx, fixtures, "dangling-dlo"); err == nil {
		t.Error("expected real Swift to 404 a DLO HEAD with a missing segment container")
	}

	// First: prove the trap exists on real Swift. A naive POST that only
	// sends the new key destroys the other keys and the DLO manifest.
	if err := h.put(ctx, fixtures, "trap-dlo", nil, "video/mp4", trapHeaders(h)); err != nil {
		t.Fatal(err)
	}
	if err := h.conn.ObjectUpdate(ctx, fixtures, "trap-dlo", map[string]string{
		"X-Object-Meta-Naive": "post",
	}); err != nil {
		t.Fatal(err)
	}
	trapped, err := h.Session.ObjectDetail(ctx, fixtures, "trap-dlo")
	if err != nil {
		t.Fatal(err)
	}
	if trapped.Class == swiftcontrol.ClassDLO {
		t.Error("expected real Swift to drop the DLO manifest on a naive POST; it did not — mock semantics need revisiting")
	}
	if _, kept := trapped.Meta["Shoot"]; kept {
		t.Error("expected real Swift to drop unsent metadata on POST; it did not")
	}

	// Now the adapter's update on a fresh DLO: one key added, everything
	// else intact.
	if err := h.put(ctx, fixtures, "safe-dlo", nil, "video/mp4", trapHeaders(h)); err != nil {
		t.Fatal(err)
	}
	before, err := h.Session.ObjectDetail(ctx, fixtures, "safe-dlo")
	if err != nil {
		t.Fatal(err)
	}
	edit := swiftcontrol.ObjectMetadataEdit{Meta: map[string]string{}}
	for k, v := range before.Meta {
		edit.Meta[k] = v
	}
	edit.Meta["Camera"] = "sony-fx6"

	result, err := h.Session.UpdateObjectMetadata(ctx, fixtures, "safe-dlo", before.EditableFingerprint, edit)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != swiftcontrol.UpdateApplied {
		t.Fatalf("Outcome = %q (%s)", result.Outcome, result.Mismatch)
	}
	after := result.After
	if after.Class != swiftcontrol.ClassDLO {
		t.Error("GATE: DLO did not remain a DLO after metadata edit")
	}
	if after.Meta["Shoot"] != "wedding" || after.Meta["Location"] != "sydney" || after.Meta["Camera"] != "sony-fx6" {
		t.Errorf("GATE: metadata edit erased keys: %v", after.Meta)
	}
	if after.ContentType != "video/mp4" {
		t.Errorf("content type not preserved: %q", after.ContentType)
	}

	// Stale-fingerprint write refuses against real Swift too.
	conflict, err := h.Session.UpdateObjectMetadata(ctx, fixtures, "safe-dlo", before.EditableFingerprint, edit)
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Outcome != swiftcontrol.UpdateConflict {
		t.Errorf("stale write = %q, want conflict", conflict.Outcome)
	}
}

func trapHeaders(h *Harness) swift.Headers {
	return swift.Headers{
		"X-Object-Meta-Shoot":    "wedding",
		"X-Object-Meta-Location": "sydney",
		"X-Object-Manifest":      h.Container("external-dlo-segments") + "/wedding-video/",
	}
}

// TestContainerMutationRealSwift drives create, the working-copy update
// delta, and markers against real Swift.
func TestContainerMutationRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	name := h.Container("mutable")

	created, err := h.Session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{
		Name: name, Meta: map[string]string{"Owner": "ops"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Meta["Owner"] != "ops" || created.StoragePolicy == "" {
		t.Errorf("created = %+v", created)
	}
	if _, err := h.Session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: name}); err == nil {
		t.Error("recreate must fail, not silently accept")
	}

	// Update: set ACLs, quota, versioning; then verify a second edit
	// removes them — all against real middleware.
	versions := h.Container("mutable-versions")
	if _, err := h.Session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: versions}); err != nil {
		t.Fatal(err)
	}
	result, err := h.Session.UpdateContainer(ctx, name, created.EditableFingerprint, swiftcontrol.ContainerEdit{
		Meta:       map[string]string{"Owner": "ops", "Tier": "gold"},
		ReadACL:    ".r:*,.rlistings",
		Versioning: swiftcontrol.ContainerVersioning{Mode: swiftcontrol.VersioningVersionsLocation, Archive: versions},
		QuotaBytes: 5000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != swiftcontrol.UpdateApplied {
		t.Fatalf("Outcome = %q (%s)", result.Outcome, result.Mismatch)
	}
	after := result.After
	if after.ReadACL != ".r:*,.rlistings" || after.QuotaBytes != 5000000 {
		t.Errorf("after = %+v", after)
	}
	if after.Versioning.Mode != swiftcontrol.VersioningVersionsLocation || after.Versioning.Archive != versions {
		t.Errorf("Versioning = %+v", after.Versioning)
	}

	result2, err := h.Session.UpdateContainer(ctx, name, after.EditableFingerprint, swiftcontrol.ContainerEdit{
		Meta: map[string]string{"Owner": "ops"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result2.Outcome != swiftcontrol.UpdateApplied {
		t.Fatalf("Outcome = %q (%s)", result2.Outcome, result2.Mismatch)
	}
	cleared := result2.After
	if cleared.ReadACL != "" || cleared.QuotaBytes != 0 || cleared.Versioning.Mode != swiftcontrol.VersioningOff {
		t.Errorf("clear edit left state behind: %+v", cleared)
	}
	if _, still := cleared.Meta["Tier"]; still {
		t.Error("removed metadata key survived")
	}

	// Stale fingerprint refuses against real Swift: `after` (the fully
	// configured state) is stale now that the clearing edit landed.
	conflict, err := h.Session.UpdateContainer(ctx, name, after.EditableFingerprint, swiftcontrol.ContainerEdit{})
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Outcome != swiftcontrol.UpdateConflict {
		t.Errorf("stale update = %q, want conflict", conflict.Outcome)
	}

	// Markers on real Swift: create, folder-suit refusal, delete keeps
	// children.
	if _, err := h.Session.CreateMarker(ctx, name, "photos/"); err != nil {
		t.Fatal(err)
	}
	if err := h.put(ctx, name, "photos/a.jpg", []byte("x"), "image/jpeg", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.put(ctx, name, "suit/", []byte("real data"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Session.CreateMarker(ctx, name, "suit/"); err == nil {
		t.Error("CreateMarker over a real object must refuse")
	}
	suit, err := h.Session.ObjectDetail(ctx, name, "suit/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Session.DeleteMarkerObject(ctx, name, "suit/", suit.ContentFingerprint()); err == nil {
		t.Error("DeleteMarkerObject over a real object must refuse")
	}
	shown, err := h.Session.PrefixDetail(ctx, name, "photos/")
	if err != nil || shown.SelfObject == nil {
		t.Fatalf("marker detail = %+v, %v", shown, err)
	}
	if _, err := h.Session.DeleteMarkerObject(ctx, name, "photos/", shown.SelfObject.ContentFingerprint()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Session.ObjectDetail(ctx, name, "photos/a.jpg"); err != nil {
		t.Errorf("child lost with marker: %v", err)
	}
}

// TestDLOSegmentPreviewRealSwift: the bounded preview against real
// listings, in assembly (name) order.
func TestDLOSegmentPreviewRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	manifest := h.Container("external-dlo-segments") + "/wedding-video/"

	full, err := h.Session.DLOSegmentPreview(ctx, manifest, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Segments) != 3 || full.HasMore || full.PreviewBytes != 3000 {
		t.Fatalf("preview = %d segments, hasMore=%v, bytes=%d", len(full.Segments), full.HasMore, full.PreviewBytes)
	}
	bounded, err := h.Session.DLOSegmentPreview(ctx, manifest, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded.Segments) != 2 || !bounded.HasMore {
		t.Errorf("bounded = %d, hasMore=%v", len(bounded.Segments), bounded.HasMore)
	}
}

// TestManifestOnlyDeleteRealSwift proves the §60 low-risk property on
// real Swift: deleting a DLO or SLO manifest with a plain DELETE leaves
// every segment in place.
func TestManifestOnlyDeleteRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	fixtures := h.Container("fixtures")
	segStore := h.Container("external-dlo-segments")

	countSegments := func(container string) int {
		t.Helper()
		n := 0
		for e, err := range h.Session.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true}) {
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := e.(swiftcontrol.ObjectEntry); ok {
				n++
			}
		}
		return n
	}

	// Disposable DLO manifest over the existing segments.
	if err := h.put(ctx, fixtures, "del-dlo", nil, "video/mp4", swift.Headers{
		"X-Object-Manifest": segStore + "/wedding-video/",
	}); err != nil {
		t.Fatal(err)
	}
	dloSegments := countSegments(segStore)
	shownDLO, err := h.Session.ObjectDetail(ctx, fixtures, "del-dlo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Session.DeleteManifestObject(ctx, fixtures, "del-dlo", shownDLO.ContentFingerprint()); err != nil {
		t.Fatal(err)
	}
	if got := countSegments(segStore); got != dloSegments {
		t.Fatalf("DLO segments = %d after manifest delete, want %d", got, dloSegments)
	}

	// Disposable SLO manifest over the existing parts.
	sloParts := h.Container("slo-parts")
	manifest := `[{"path": "/` + sloParts + `/backup/part-000001"}]`
	if err := h.rawPut(ctx, fixtures+"/del-slo?multipart-manifest=put", []byte(manifest), "application/x-tar"); err != nil {
		t.Fatal(err)
	}
	sloSegments := countSegments(sloParts)
	shownSLO, err := h.Session.ObjectDetail(ctx, fixtures, "del-slo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Session.DeleteManifestObject(ctx, fixtures, "del-slo", shownSLO.ContentFingerprint()); err != nil {
		t.Fatal(err)
	}
	if got := countSegments(sloParts); got != sloSegments {
		t.Fatalf("SLO segments = %d after manifest delete, want %d", got, sloSegments)
	}

	// A plain object refuses and survives.
	shownPlain, err := h.Session.ObjectDetail(ctx, fixtures, "tagged.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Session.DeleteManifestObject(ctx, fixtures, "tagged.txt", shownPlain.ContentFingerprint()); err == nil {
		t.Fatal("plain object accepted by manifest-only delete")
	}
	if _, err := h.Session.ObjectDetail(ctx, fixtures, "tagged.txt"); err != nil {
		t.Errorf("refused object was deleted: %v", err)
	}
}

// TestRcloneRunnerRealSwift drives the pinned rclone through the runner
// against real Swift: a cross-account batch with a large object, the
// captured segment prefix matching reality, and env-only credentials.
func TestRcloneRunnerRealSwift(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("pinned rclone not installed")
	}
	h := harness(t)
	if h.Conn2() == nil {
		skipOrFailSecondAccount(t)
	}
	ctx := context.Background()

	runner, err := rclone.NewRunner(rclone.Options{ChunkSize: "1Mi", Retries: 1, LowLevelRetries: 2})
	if err != nil {
		t.Fatal(err)
	}

	// A dedicated source container: rclone traverses the WHOLE source
	// namespace even under --files-from, and other tests seed deliberate
	// traps (dangling manifests) into the shared fixtures container.
	srcName := h.Container("runner-src")
	if err := h.conn.ContainerCreate(ctx, srcName, nil); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte{0xBB}, 3*1024*1024)
	if err := h.put(ctx, srcName, "runner/big.bin", big, "application/octet-stream", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.put(ctx, srcName, "runner/small.txt", []byte("small"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}

	// Destination container on the SECOND account.
	dstName := h.Prefix + "runner-out"
	if err := h.rawPut2(ctx, dstName, nil, ""); err != nil {
		t.Fatal(err)
	}

	remotes := []rclone.Remote{
		{Name: "aeronautes_src", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken},
		{Name: "aeronautes_dst", StorageURL: h.Conn2().StorageUrl, Token: h.Conn2().AuthToken},
	}
	// The runner's own policy: writes land in a staging namespace.
	result, err := runner.Copy(ctx, remotes,
		"aeronautes_src:"+srcName, "aeronautes_dst:"+dstName+"/.aeronautes/stage/it-runner/batch",
		[]string{"runner/big.bin", "runner/small.txt"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || len(result.Errors) != 0 {
		t.Fatalf("result = %+v", result)
	}

	// The captured prefix matches the real segment namespace.
	if len(result.SegmentPrefixes) != 1 {
		t.Fatalf("prefixes = %+v, want the one large object", result.SegmentPrefixes)
	}
	captured := result.SegmentPrefixes[0]
	if captured.Container != dstName+"_segments" {
		t.Errorf("segment container = %q", captured.Container)
	}
	segments, err := h.Conn2().ObjectsAll(ctx, captured.Container, &swift.ObjectsOpts{Prefix: captured.Prefix})
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 3 {
		t.Errorf("captured prefix lists %d segments, want 3", len(segments))
	}

	// Both objects landed with the right sizes.
	obj, _, err := h.Conn2().Object(ctx, dstName, ".aeronautes/stage/it-runner/batch/runner/big.bin")
	if err != nil || obj.Bytes != int64(len(big)) {
		t.Errorf("big.bin at staged dest: %v bytes=%d", err, obj.Bytes)
	}
	if _, _, err := h.Conn2().Object(ctx, dstName, ".aeronautes/stage/it-runner/batch/runner/small.txt"); err != nil {
		t.Errorf("small.txt at staged dest: %v", err)
	}
}

// TestCopyPlannerRealSwift merge-joins real deep listings across the two
// SAIO accounts: the full fixtures namespace (hostile names, DLO, SLO)
// against an empty destination, then a seeded pair for each verdict.
func TestCopyPlannerRealSwift(t *testing.T) {
	h := harness(t)
	if h.Session2() == nil {
		skipOrFailSecondAccount(t)
	}
	ctx := context.Background()

	emptyDst := h.Prefix + "plan-empty"
	if err := h.rawPut2(ctx, emptyDst, nil, ""); err != nil {
		t.Fatal(err)
	}
	items := map[string]plan.Item{}
	summary, err := plan.Copy(ctx,
		h.Session.Objects(ctx, h.Container("fixtures"), swiftcontrol.ObjectListOptions{Deep: true}),
		h.Session2().Objects(ctx, emptyDst, swiftcontrol.ObjectListOptions{Deep: true}),
		"", "", func(i plan.Item) error { items[i.Name] = i; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if summary.New != int64(len(items)) || summary.New == 0 || summary.Same+summary.Changed+summary.Unknown+summary.DestOnly != 0 {
		t.Fatalf("fixtures vs empty: %+v", summary)
	}
	// The real SLO listing must carry slo_etag → conservative flag.
	if item := items["external-slo"]; item.Reason != plan.ReasonLargeObject {
		t.Errorf("external-slo = %+v, want large_object", item)
	}
	// Zero-byte rows (the DLO manifest among them) flag for HEAD resolution.
	for _, name := range []string{"external-dlo", "empty-object", "folder-marker/"} {
		if item := items[name]; item.Reason != plan.ReasonPossibleManifest {
			t.Errorf("%s = %+v, want possible_manifest", name, item)
		}
	}
	// §14.3 unsafe identities route native; safe hostile names ride rclone.
	for name, want := range map[string]plan.Route{
		"weird/repeated//slash": plan.RouteNative,
		"fake/":                 plan.RouteNative,
		"weird/percent%name":    plan.RouteRclone,
		"unicode/日本語.txt":       plan.RouteRclone,
	} {
		if item := items[name]; item.Route != want {
			t.Errorf("%s routed %s, want %s", name, item.Route, want)
		}
	}

	// Pair verdicts against a seeded destination.
	srcName, dstName := h.Container("plan-src"), h.Prefix+"plan-dst"
	if err := h.conn.ContainerCreate(ctx, srcName, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.rawPut2(ctx, dstName, nil, ""); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"deep/changed.txt": []byte("source-version"),
		"deep/same.txt":    []byte("identical bytes"),
		"deep/newone.txt":  []byte("fresh"),
	} {
		if err := h.put(ctx, srcName, name, data, "text/plain", nil); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string][]byte{
		"mirror/changed.txt":  []byte("older-version!"), // same length, different bytes
		"mirror/same.txt":     []byte("identical bytes"),
		"mirror/obsolete.txt": []byte("dest only"),
	} {
		if err := h.rawPut2(ctx, dstName+"/"+name, data, "text/plain"); err != nil {
			t.Fatal(err)
		}
	}

	verdicts := map[string]plan.Verdict{}
	summary, err = plan.Copy(ctx,
		h.Session.Objects(ctx, srcName, swiftcontrol.ObjectListOptions{Deep: true, Prefix: "deep/"}),
		h.Session2().Objects(ctx, dstName, swiftcontrol.ObjectListOptions{Deep: true, Prefix: "mirror/"}),
		"deep/", "mirror/", func(i plan.Item) error { verdicts[i.Name] = i.Verdict; return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]plan.Verdict{
		"changed.txt":  plan.VerdictChanged,
		"same.txt":     plan.VerdictSame,
		"newone.txt":   plan.VerdictNew,
		"obsolete.txt": plan.VerdictDestOnly,
	}
	for name, v := range want {
		if verdicts[name] != v {
			t.Errorf("%s = %s, want %s", name, verdicts[name], v)
		}
	}
	if summary.Overwrites != 1 || summary.TransferObjects != 2 {
		t.Errorf("summary = %+v", summary)
	}
}

// executorFixture builds a real-Swift executor over the two SAIO
// accounts with the pinned rclone bound as the engine.
func executorFixture(t *testing.T, h *Harness, srcContainer, dstContainer, jobID string) *transfercopy.Executor {
	t.Helper()
	runner, err := rclone.NewRunner(rclone.Options{ChunkSize: "1Mi", Retries: 1, LowLevelRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	remotes := []rclone.Remote{
		{Name: "aeronautes_src", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken},
		{Name: "aeronautes_dst", StorageURL: h.Conn2().StorageUrl, Token: h.Conn2().AuthToken},
	}
	executor, err := transfercopy.New(transfercopy.Config{
		Source: h.Session, Dest: h.Session2(),
		Engine:  transfercopy.BindRunner(runner, remotes),
		SrcSpec: "aeronautes_src:" + srcContainer, DstSpec: "aeronautes_dst:" + dstContainer,
		SrcContainer: srcContainer, DstContainer: dstContainer,
		JobID: jobID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := executor.Close(); err != nil {
			t.Error(err)
		}
	})
	return executor
}

// planInto streams a fresh plan straight into the executor (the
// production wiring) and returns the reports.
func planInto(ctx context.Context, t *testing.T, h *Harness, executor *transfercopy.Executor, srcContainer, dstContainer string) *transfercopy.Report {
	t.Helper()
	_, err := plan.Copy(ctx,
		h.Session.Objects(ctx, srcContainer, swiftcontrol.ObjectListOptions{Deep: true}),
		h.Session2().Objects(ctx, dstContainer, swiftcontrol.ObjectListOptions{Deep: true}),
		"", "", func(i plan.Item) error { return executor.Handle(ctx, i) })
	if err != nil {
		t.Fatal(err)
	}
	report, err := executor.Finish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// TestCopyExecutorRealSwift is the K-TEST-001 copy gate on real Swift:
// the full §58 hostile-name suite crosses accounts byte-exact through
// the real pinned rclone with native fallback, large objects survive as
// logical copies, changed destinations are refused, and reconciliation
// retains uncertain residue after cancellation.
func TestCopyExecutorRealSwift(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("pinned rclone not installed")
	}
	h := harness(t)
	if h.Session2() == nil {
		skipOrFailSecondAccount(t)
	}
	ctx := context.Background()

	t.Run("hostile-suite", func(t *testing.T) {
		src := h.Container("encoding")
		dst := h.Prefix + "exec-encoding"
		if err := h.rawPut2(ctx, dst, nil, ""); err != nil {
			t.Fatal(err)
		}
		executor := executorFixture(t, h, src, dst, "job-enc")
		report := planInto(ctx, t, h, executor, src, dst)
		if len(report.Failures) != 0 {
			t.Fatalf("failures: %+v", report.Failures)
		}
		if report.Copied != int64(len(EncodingNames)) {
			t.Errorf("copied %d of %d", report.Copied, len(EncodingNames))
		}
		if report.NativeRouted == 0 {
			t.Error("no names went native — the unsafe identities must have")
		}

		// Independent verification: fresh listings prove Same for every
		// name (§28.4), then raw byte compares on the worst identities.
		verify, err := executor.Verify(ctx,
			h.Session.Objects(ctx, src, swiftcontrol.ObjectListOptions{Deep: true}),
			h.Session2().Objects(ctx, dst, swiftcontrol.ObjectListOptions{Deep: true}))
		if err != nil {
			t.Fatal(err)
		}
		if len(verify.Failures) != 0 || verify.Verified != int64(len(EncodingNames)) {
			t.Fatalf("verify = %+v", verify)
		}
		for _, name := range []string{"/foo", "foo/", "foo//bar", "line\nbreak", "   ", "a/./b"} {
			body, _, err := h.Session2().OpenObject(ctx, dst, name)
			if err != nil {
				t.Fatalf("GET dest %q: %v", name, err)
			}
			data, _ := io.ReadAll(body)
			body.Close()
			if string(data) != "payload:"+name {
				t.Errorf("%q arrived as %q", name, data)
			}
		}
	})

	t.Run("large-and-metadata", func(t *testing.T) {
		src := h.Container("exec-src")
		dst := h.Prefix + "exec-dst"
		if err := h.conn.ContainerCreate(ctx, src, nil); err != nil {
			t.Fatal(err)
		}
		if err := h.rawPut2(ctx, dst, nil, ""); err != nil {
			t.Fatal(err)
		}
		big := bytes.Repeat([]byte{0xCD}, 3*1024*1024)
		if err := h.put(ctx, src, "big.bin", big, "application/octet-stream",
			swift.Headers{"X-Object-Meta-Shoot": "launch"}); err != nil {
			t.Fatal(err)
		}
		if err := h.put(ctx, src, "small.txt", []byte("v1 bytes"), "text/plain", nil); err != nil {
			t.Fatal(err)
		}

		executor := executorFixture(t, h, src, dst, "job-big")
		report := planInto(ctx, t, h, executor, src, dst)
		if len(report.Failures) != 0 || report.Copied != 2 {
			t.Fatalf("report: copied=%d failures=%+v", report.Copied, report.Failures)
		}
		// The 3MiB object crossed at 1Mi chunks: the destination is the
		// engine's own DLO, logical size preserved, metadata re-applied
		// (rclone drops it), fingerprint stamped.
		d, err := h.Session2().ObjectDetail(ctx, dst, "big.bin")
		if err != nil {
			t.Fatal(err)
		}
		if d.Class != swiftcontrol.ClassDLO || d.ContentLength != int64(len(big)) {
			t.Fatalf("big.bin = class %s length %d", d.Class, d.ContentLength)
		}
		if d.Meta["Shoot"] != "launch" || d.Meta["Aeronautes-Fingerprint"] == "" {
			t.Errorf("metadata = %v", d.Meta)
		}
		if len(report.CapturedPrefixes) == 0 || len(report.CommittedPrefixes) == 0 {
			t.Errorf("prefixes: %+v / %+v", report.CapturedPrefixes, report.CommittedPrefixes)
		}

		// Updating the source cannot authorise replacing either destination.
		if err := h.put(ctx, src, "small.txt", []byte("v2 BYTES"), "text/plain", nil); err != nil {
			t.Fatal(err)
		}
		big2 := bytes.Repeat([]byte{0xEE}, 2*1024*1024+512)
		if err := h.put(ctx, src, "big.bin", big2, "application/octet-stream", nil); err != nil {
			t.Fatal(err)
		}
		executor2 := executorFixture(t, h, src, dst, "job-big2")
		report2 := planInto(ctx, t, h, executor2, src, dst)
		if len(report2.Failures) != 0 || report2.Overwrites != 0 || report2.OverwritesRefused != 2 || report2.Copied != 0 {
			t.Fatalf("refusal report: %+v (failures %+v)", report2, report2.Failures)
		}
		d2, err := h.Session2().ObjectDetail(ctx, dst, "big.bin")
		if err != nil {
			t.Fatal(err)
		}
		if d2.Class != swiftcontrol.ClassDLO || d2.ContentLength != int64(len(big)) {
			t.Fatalf("big.bin after refusal = class %s length %d", d2.Class, d2.ContentLength)
		}
		if d2.Manifest != d.Manifest {
			t.Error("manifest changed despite refusal")
		}
		small, _, err := h.Session2().OpenObject(ctx, dst, "small.txt")
		if err != nil {
			t.Fatal(err)
		}
		smallBytes, _ := io.ReadAll(small)
		small.Close()
		if string(smallBytes) != "v1 bytes" {
			t.Errorf("small.txt = %q", smallBytes)
		}
		// No staging litter under either job's prefix.
		for _, ex := range []*transfercopy.Executor{executor, executor2} {
			for entry, err := range h.Session2().Objects(ctx, dst, swiftcontrol.ObjectListOptions{Deep: true, Prefix: ex.StagingPrefix()}) {
				if err != nil {
					t.Fatal(err)
				}
				t.Errorf("staging litter: %+v", entry)
			}
		}

		// Restoring the original source makes both pairs equal again;
		// refusal has preserved the prior destination fingerprint and bytes.
		// A full-fidelity skip (ADR 0096) treats a missing X-Object-Meta-Shoot as a
		// difference, so the restore returns big.bin's metadata too, not only its
		// bytes: idempotency now requires the source's carried set to match what
		// was copied, which is what "the original source" means.
		if err := h.put(ctx, src, "small.txt", []byte("v1 bytes"), "text/plain", nil); err != nil {
			t.Fatal(err)
		}
		if err := h.put(ctx, src, "big.bin", big, "application/octet-stream",
			swift.Headers{"X-Object-Meta-Shoot": "launch"}); err != nil {
			t.Fatal(err)
		}
		executor3 := executorFixture(t, h, src, dst, "job-noop")
		report3 := planInto(ctx, t, h, executor3, src, dst)
		if report3.Copied != 0 || report3.OverwritesRefused != 0 || len(report3.Failures) != 0 || report3.SkippedEqual == 0 {
			t.Errorf("idempotent re-plan: %+v (failures %+v)", report3, report3.Failures)
		}
	})

	t.Run("cancel-reconcile", func(t *testing.T) {
		src := h.Container("exec-cancel-src")
		dst := h.Prefix + "exec-cancel-dst"
		if err := h.conn.ContainerCreate(ctx, src, nil); err != nil {
			t.Fatal(err)
		}
		if err := h.rawPut2(ctx, dst, nil, ""); err != nil {
			t.Fatal(err)
		}
		huge := bytes.Repeat([]byte{0x5A}, 24*1024*1024)
		if err := h.put(ctx, src, "huge.bin", huge, "application/octet-stream", nil); err != nil {
			t.Fatal(err)
		}

		runner, err := rclone.NewRunner(rclone.Options{ChunkSize: "1Mi", BWLimit: "2M", Retries: 1, LowLevelRetries: 2})
		if err != nil {
			t.Fatal(err)
		}
		remotes := []rclone.Remote{
			{Name: "aeronautes_src", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken},
			{Name: "aeronautes_dst", StorageURL: h.Conn2().StorageUrl, Token: h.Conn2().AuthToken},
		}
		executor, err := transfercopy.New(transfercopy.Config{
			Source: h.Session, Dest: h.Session2(),
			Engine:  transfercopy.BindRunner(runner, remotes),
			SrcSpec: "aeronautes_src:" + src, DstSpec: "aeronautes_dst:" + dst,
			SrcContainer: src, DstContainer: dst,
			JobID: "job-cancel",
		})
		if err != nil {
			t.Fatal(err)
		}

		// Cancel a few seconds into the throttled upload.
		defer func() {
			if err := executor.Close(); err != nil {
				t.Error(err)
			}
		}()
		cancelCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		_ = executor.Handle(cancelCtx, plan.Item{Name: "huge.bin", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: int64(len(huge))})
		report, _ := executor.Finish(cancelCtx)

		witness, err := oracle.Connect(os.Getenv("AERONAUTES_SAIO_AUTH_URL"), envOr("AERONAUTES_SAIO_USER2", "test2:tester2"), envOr("AERONAUTES_SAIO_KEY2", "testing2"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(witness.Close)
		// The child and Finish may already have cleaned some objects.
		// Reconciliation must retain everything that remains at its boundary.
		before, err := witness.SnapshotContainers(dst, dst+"_segments")
		if err != nil {
			t.Fatal(err)
		}
		reconcile, err := executor.Reconcile(ctx, report)
		if err != nil || reconcile.SweptStaged != 0 || reconcile.SweptSegments != 0 || len(reconcile.Notes) == 0 {
			t.Fatalf("cancel reconciliation = %+v, %v", reconcile, err)
		}
		after, err := witness.SnapshotContainers(dst, dst+"_segments")
		if err != nil {
			t.Fatal(err)
		}
		if delta := oracle.Diff(before, after); !delta.Empty() {
			t.Fatalf("reconciliation changed retained data after child exit: %s", delta)
		}
		t.Logf("cancel: captured=%d swept=%d kept=%v notes=%v",
			len(report.CapturedPrefixes), reconcile.SweptSegments, reconcile.KeptPrefixes, reconcile.Notes)
	})
}

// TestLocalTransferRealSwift drives upload and download through the
// real pinned rclone with native fallback: a chunked upload lands as a
// DLO, hostile names ride the native path both ways, and the §27.5
// preflight rejects unmaterializable namespaces completely.
func TestLocalTransferRealSwift(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("pinned rclone not installed")
	}
	h := harness(t)
	ctx := context.Background()

	runner, err := rclone.NewRunner(rclone.Options{ChunkSize: "1Mi", Retries: 1, LowLevelRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	engine := transfercopy.BindRunner(runner, []rclone.Remote{
		{Name: "aeronautes_dst", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken},
	})

	container := h.Container("local-up")
	if err := h.conn.ContainerCreate(ctx, container, nil); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	big := bytes.Repeat([]byte{0x7E}, 3*1024*1024)
	for name, content := range map[string][]byte{
		"small.txt": []byte("small upload"), "big.bin": big, "line\nname": []byte("hostile upload"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	preflight, err := localtransfer.PreflightUpload(ctx, h.Session, container, "up/",
		[]string{filepath.Join(dir, "small.txt"), filepath.Join(dir, "big.bin"), filepath.Join(dir, "line\nname")})
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %v %+v", err, preflight)
	}
	report, err := localtransfer.Upload(ctx, h.Session, engine, "aeronautes_dst:"+container, container, "up/", "it-up-job", preflight, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Done != 3 || len(report.Failures) != 0 || report.NativeRouted != 1 {
		t.Fatalf("upload report = %+v (failures %+v)", report, report.Failures)
	}
	d, err := h.Session.ObjectDetail(ctx, container, "up/big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if d.Class != swiftcontrol.ClassDLO || d.ContentLength != int64(len(big)) {
		t.Errorf("big.bin = class %s length %d, want chunked DLO", d.Class, d.ContentLength)
	}
	if len(report.CapturedPrefixes) == 0 {
		t.Error("no segment prefixes captured on the chunked upload")
	}

	// Download it back byte-exact (the DLO materializes logically).
	destDir := t.TempDir()
	srcEngine := transfercopy.BindRunner(runner, []rclone.Remote{
		{Name: "aeronautes_src", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken},
	})
	// The uploaded control-character name is legal IN Swift but outside
	// the cross-platform materialization policy: the download preflight
	// must reject the scope while it is present (§65.9 item 13, pinned
	// on real Swift), and proceed once it is gone.
	rejected, err := localtransfer.PreflightDownload(ctx, h.Session, container, "up/", destDir)
	if err != nil {
		t.Fatal(err)
	}
	if !rejected.Rejected() {
		t.Fatalf("control-character name did not reject the download: %+v", rejected)
	}
	if err := h.Session.DeleteObject(ctx, container, "up/line\nname"); err != nil {
		t.Fatal(err)
	}
	downPre, err := localtransfer.PreflightDownload(ctx, h.Session, container, "up/", destDir)
	if err != nil || downPre.Rejected() {
		t.Fatalf("download preflight: %v %+v", err, downPre)
	}
	downReport, err := localtransfer.Download(ctx, h.Session, srcEngine, "aeronautes_src:"+container, container, "up/", destDir, "it-down-job", downPre, nil)
	if err != nil {
		t.Fatal(err)
	}
	if downReport.Done != 2 || len(downReport.Failures) != 0 {
		t.Fatalf("download report = %+v (failures %+v)", downReport, downReport.Failures)
	}
	for name, want := range map[string][]byte{"small.txt": []byte("small upload"), "big.bin": big} {
		got, err := os.ReadFile(filepath.Join(destDir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%q: err %v, %d bytes (want %d)", name, err, len(got), len(want))
		}
	}

	// The encoding container carries dot-segment names: materializing it
	// must be rejected COMPLETELY before any write (§27.5).
	rejectDir := t.TempDir()
	rejected, err = localtransfer.PreflightDownload(ctx, h.Session, h.Container("encoding"), "", rejectDir)
	if err != nil {
		t.Fatal(err)
	}
	if !rejected.Rejected() {
		t.Fatal("dot-segment namespace not rejected")
	}
	entries, _ := os.ReadDir(rejectDir)
	if len(entries) != 0 {
		t.Errorf("preflight wrote files: %v", entries)
	}
}

// TestDeletionEngineRealSwift drives both §31.2 executors against the
// real bulk middleware: a synthetic namespace with hostile names dies
// completely, shared-segment manifests die manifest-only, and Empty
// Container preserves every piece of container configuration (§33).
func TestDeletionEngineRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	capabilities, err := h.Session.FetchCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.BulkDeleteMaxDeletes() == 0 {
		t.Fatal("SAIO should advertise bulk_delete")
	}

	container := h.Container("del-scope")
	if err := h.conn.ContainerCreate(ctx, container, swift.Headers{
		"X-Container-Meta-Team": "ops",
		"X-Container-Read":      ".r:*",
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 120; i++ {
		if err := h.put(ctx, container, fmt.Sprintf("doomed/obj-%04d", i), []byte("x"), "text/plain", nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"doomed/a b", "doomed/line\nbreak", "doomed/日本語", "doomed//lead", "doomed/trail/"} {
		if err := h.put(ctx, container, name, []byte("hostile"), "text/plain", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.put(ctx, container, "kept.txt", []byte("survivor"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	// Shared segments: the doomed manifest and a kept sibling reference
	// the same prefix in the fixtures segment store.
	segStore := h.Container("external-dlo-segments")
	for _, name := range []string{"doomed/shared-video", "kept-video"} {
		if err := h.put(ctx, container, name, nil, "video/mp4", swift.Headers{
			"X-Object-Manifest": segStore + "/wedding-video/",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Bulk path.
	result, err := deletion.DeletePrefix(ctx, h.Session, capabilities, container, "doomed/", deletion.Options{BulkSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != deletion.StrategyBulk || result.Remaining != 0 || len(result.Failures) != 0 {
		t.Fatalf("bulk result = %+v (failures %+v)", result, result.Failures)
	}
	if result.Deleted != 126 {
		t.Errorf("deleted = %d, want 126", result.Deleted)
	}
	// The sibling manifest still assembles: 3000 logical bytes.
	d, err := h.Session.ObjectDetail(ctx, container, "kept-video")
	if err != nil {
		t.Fatal(err)
	}
	if d.ContentLength != 3000 {
		t.Fatalf("shared segments harmed: sibling = %d bytes", d.ContentLength)
	}

	// Parallel path empties what remains ("" prefix = Empty Container's
	// engine); container config must be untouched (§33).
	before, err := h.Session.ContainerDetail(ctx, container)
	if err != nil {
		t.Fatal(err)
	}
	result, err = deletion.DeletePrefix(ctx, h.Session, capabilities, container, "", deletion.Options{ForceParallel: true, Workers: 8})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != deletion.StrategyParallel || result.Remaining != 0 {
		t.Fatalf("parallel result = %+v", result)
	}
	after, err := h.Session.ContainerDetail(ctx, container)
	if err != nil {
		t.Fatal(err)
	}
	if diff := deletion.SnapshotConfig(before).Diff(deletion.SnapshotConfig(after)); diff != "" {
		t.Fatalf("Empty Container changed config: %s", diff)
	}
	if after.Meta["Team"] != "ops" || after.ReadACL != ".r:*" {
		t.Fatalf("config survived wrong: %+v", after)
	}

	// Delete Container: now-empty container DELETEs natively.
	if err := h.Session.DeleteContainer(ctx, container); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Session.ContainerDetail(ctx, container); !swiftcontrol.IsNotFound(err) {
		t.Fatalf("container survived: %v", err)
	}
	// The shared segment store is fully intact.
	segments := 0
	for entry, err := range h.Session.Objects(ctx, segStore, swiftcontrol.ObjectListOptions{Deep: true, Prefix: "wedding-video/"}) {
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := entry.(swiftcontrol.ObjectEntry); ok {
			segments++
		}
	}
	if segments != 3 {
		t.Fatalf("segment store = %d segments, want 3", segments)
	}
}

// TestSLOSegmentsDeleteRealSwift validates the ?multipart-manifest=delete
// wire op against the real SLO middleware: the manifest AND exactly its
// referenced segments die; unrelated neighbours survive.
func TestSLOSegmentsDeleteRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()

	parts := h.Container("slo-del-parts")
	manifests := h.Container("slo-del")
	if err := h.conn.ContainerCreate(ctx, parts, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.conn.ContainerCreate(ctx, manifests, nil); err != nil {
		t.Fatal(err)
	}
	type sloSeg struct {
		Path      string `json:"path"`
		ETag      string `json:"etag"`
		SizeBytes int    `json:"size_bytes"`
	}
	var entries []sloSeg
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("doomed/part-%02d", i)
		data := bytes.Repeat([]byte{byte(i)}, 400)
		if err := h.put(ctx, parts, name, data, "application/octet-stream", nil); err != nil {
			t.Fatal(err)
		}
		sum := md5.Sum(data)
		entries = append(entries, sloSeg{Path: "/" + parts + "/" + name, ETag: hex.EncodeToString(sum[:]), SizeBytes: 400})
	}
	if err := h.put(ctx, parts, "neighbour", []byte("uninvolved"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(entries)
	if err := h.rawPut(ctx, manifests+"/doomed.slo?multipart-manifest=put", body, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}

	// The product path (§65.9 item 7): fetch the graph, delete the
	// manifest with a plain DELETE (manifest-only on real Swift), then
	// delete exactly the fetched identities natively — never the
	// recursive ?multipart-manifest=delete, whose target is whatever
	// occupies the live name at execution time.
	graph, err := h.Session.SLOManifest(ctx, manifests, "doomed.slo")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Session.DeleteObject(ctx, manifests, "doomed.slo"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Session.ObjectDetail(ctx, manifests, "doomed.slo"); !swiftcontrol.IsNotFound(err) {
		t.Fatalf("manifest survived: %v", err)
	}
	// Plain DELETE was manifest-only: the segments are still there.
	if _, err := h.Session.ObjectDetail(ctx, parts, "doomed/part-01"); err != nil {
		t.Fatalf("plain DELETE followed segments on real Swift: %v", err)
	}
	deleted := 0
	for _, entry := range graph.Entries {
		segment, ok := entry.(swiftcontrol.ObjectSegment)
		if !ok {
			t.Fatalf("unexpected entry shape %T", entry)
		}
		if err := h.Session.DeleteObject(ctx, segment.Path.Container, segment.Path.Object); err != nil {
			t.Fatal(err)
		}
		deleted++
	}
	if deleted != 3 {
		t.Fatalf("deleted %d segments, want 3", deleted)
	}
	for i := 1; i <= 3; i++ {
		if _, err := h.Session.ObjectDetail(ctx, parts, fmt.Sprintf("doomed/part-%02d", i)); !swiftcontrol.IsNotFound(err) {
			t.Fatalf("segment %d survived: %v", i, err)
		}
	}
	if _, err := h.Session.ObjectDetail(ctx, parts, "neighbour"); err != nil {
		t.Fatal("uninvolved neighbour deleted")
	}
}

// Copy and manifest-only deletion remain independently supported domain
// operations. These component checks cannot certify public Mirror,
// whose start is refused under ADR 0020.
func TestCopyAndManifestDeleteComponentsRealSwift(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("pinned rclone not installed")
	}
	h := harness(t)
	if h.Session2() == nil {
		skipOrFailSecondAccount(t)
	}
	ctx := context.Background()
	capabilities, err := h.Session2().FetchCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}

	src := h.Container("mirror-src")
	dst := h.Prefix + "mirror-dst"
	if err := h.conn.ContainerCreate(ctx, src, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.rawPut2(ctx, dst, nil, ""); err != nil {
		t.Fatal(err)
	}
	seed2 := func(name string, data []byte, headers map[string]string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPut,
			h.Conn2().StorageUrl+"/"+dst+"/"+name, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Auth-Token", h.Conn2().AuthToken)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			t.Fatalf("seed %s: %s", name, resp.Status)
		}
	}

	// Source scope: two objects. Dest scope: one converged, two
	// obsolete, and a dest-only DLO sharing segments with an
	// out-of-scope manifest.
	if err := h.put(ctx, src, "scope/keep.txt", []byte("kept bytes"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.put(ctx, src, "scope/new.txt", []byte("fresh"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	seed2("scope/keep.txt", []byte("kept bytes"), nil)
	seed2("scope/obsolete.txt", []byte("stale"), nil)
	segStore := dst + "_segments"
	if err := h.rawPut2(ctx, segStore, nil, ""); err != nil {
		t.Fatal(err)
	}
	req2, _ := http.NewRequestWithContext(ctx, http.MethodPut,
		h.Conn2().StorageUrl+"/"+segStore+"/shared/000", bytes.NewReader(bytes.Repeat([]byte{0xAB}, 500)))
	req2.Header.Set("X-Auth-Token", h.Conn2().AuthToken)
	if resp, err := http.DefaultClient.Do(req2); err != nil || resp.StatusCode >= 300 {
		t.Fatalf("segment seed: %v %v", err, resp)
	}
	seed2("scope/obsolete-dlo.bin", nil, map[string]string{"X-Object-Manifest": segStore + "/shared/"})
	seed2("outside-kept-dlo.bin", nil, map[string]string{"X-Object-Manifest": segStore + "/shared/"})

	// Copy half through the real engine, scoped to the prefixes.
	runner, err := rclone.NewRunner(rclone.Options{Retries: 1, LowLevelRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	remotes := []rclone.Remote{
		{Name: "aeronautes_src", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken},
		{Name: "aeronautes_dst", StorageURL: h.Conn2().StorageUrl, Token: h.Conn2().AuthToken},
	}
	executor, err := transfercopy.New(transfercopy.Config{
		Source: h.Session, Dest: h.Session2(),
		Engine:  transfercopy.BindRunner(runner, remotes),
		SrcSpec: "aeronautes_src:" + src, DstSpec: "aeronautes_dst:" + dst,
		SrcContainer: src, DstContainer: dst,
		SrcPrefix: "scope/", DstPrefix: "scope/",
		JobID: "job-mirror",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := executor.Close(); err != nil {
			t.Error(err)
		}
	}()
	var destOnly []string
	_, err = plan.Copy(ctx,
		h.Session.Objects(ctx, src, swiftcontrol.ObjectListOptions{Deep: true, Prefix: "scope/"}),
		h.Session2().Objects(ctx, dst, swiftcontrol.ObjectListOptions{Deep: true, Prefix: "scope/"}),
		"scope/", "scope/", func(item plan.Item) error {
			if item.Verdict == plan.VerdictDestOnly {
				destOnly = append(destOnly, "scope/"+item.Name)
				return nil
			}
			return executor.Handle(ctx, item)
		})
	if err != nil {
		t.Fatal(err)
	}
	report, err := executor.Finish(ctx)
	if err != nil || len(report.Failures) != 0 {
		t.Fatalf("copy half: %v %+v", err, report.Failures)
	}
	verify, err := executor.Verify(ctx,
		h.Session.Objects(ctx, src, swiftcontrol.ObjectListOptions{Deep: true, Prefix: "scope/"}),
		h.Session2().Objects(ctx, dst, swiftcontrol.ObjectListOptions{Deep: true, Prefix: "scope/"}))
	if err != nil || len(verify.Failures) != 0 {
		t.Fatalf("copy verify: %v %+v", err, verify)
	}

	// THE RACE (§28.6): one planned dest-only plain object becomes a
	// DLO over the SHARED prefix before the delete half runs.
	seed2("scope/obsolete.txt", nil, map[string]string{"X-Object-Manifest": segStore + "/shared/"})

	result, err := deletion.DeleteNames(ctx, h.Session2(), capabilities, dst, destOnly, deletion.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != int64(len(destOnly)) || result.Remaining != 0 {
		t.Fatalf("delete half = %+v (destOnly %v)", result, destOnly)
	}
	// Both fixtures hold: the out-of-scope manifest still assembles at
	// full size — neither the shared-segment deletion nor the race
	// followed segments.
	d, err := h.Session2().ObjectDetail(ctx, dst, "outside-kept-dlo.bin")
	if err != nil {
		t.Fatal(err)
	}
	if d.ContentLength != 500 {
		t.Fatalf("out-of-scope manifest = %d bytes: segments were followed", d.ContentLength)
	}
	// The mirror converged: dest scope now matches source exactly.
	summary, err := plan.Copy(ctx,
		h.Session.Objects(ctx, src, swiftcontrol.ObjectListOptions{Deep: true, Prefix: "scope/"}),
		h.Session2().Objects(ctx, dst, swiftcontrol.ObjectListOptions{Deep: true, Prefix: "scope/"}),
		"scope/", "scope/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.DestOnly != 0 || summary.New != 0 || summary.Changed != 0 {
		t.Fatalf("post-mirror join = %+v, want converged", summary)
	}
}

// TestBenchmarkRealSwift runs the §42 benchmark job against the real
// cluster through the app stack: consent-gated, measured phases, the
// recommendation persisted, and the temporary container removed.
func TestBenchmarkRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()

	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	bookmarkStore := bookmarks.NewStore(d, secrets.NewSessionStore())
	saved, err := bookmarkStore.Save(ctx, bookmarks.Bookmark{
		DisplayName: "SAIO", AuthURL: envOr("AERONAUTES_SAIO_AUTH_URL", ""), Username: envOr("AERONAUTES_SAIO_USER", "test:tester"),
	}, []byte(envOr("AERONAUTES_SAIO_KEY", "testing")))
	if err != nil {
		t.Fatal(err)
	}
	connections := app.NewConnections(bookmarkStore)
	jobStore := jobs.NewStore(d)
	runner := app.NewJobRunner(jobStore, audit.NewStore(d))
	service := app.NewBenchmarkService(connections, jobStore, runner, d)
	app.BenchmarkScale(service, 40, []int{2, 8})

	account := "AUTH_" + strings.SplitN(envOr("AERONAUTES_SAIO_USER", "test:tester"), ":", 2)[0]
	jobID, err := service.StartBenchmark(ctx, app.BenchmarkRequestDTO{BookmarkID: saved.ID, Confirm: account})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	var job jobs.Job
	for time.Now().Before(deadline) {
		job, err = jobStore.Get(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if jobs.Terminal(job.State) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if job.State != jobs.StateComplete {
		t.Fatalf("job = %+v", job)
	}
	latest, err := service.Latest(ctx, saved.ID)
	if err != nil || latest == nil || len(latest.Phases) < 4 {
		t.Fatalf("latest = %+v %v", latest, err)
	}
	t.Logf("benchmark phases: %+v recommendation: %+v", latest.Phases, latest.Recommendation)
	// The temporary container is gone from the real account.
	for entry, err := range h.Session.Containers(ctx, swiftcontrol.ListOptions{Prefix: "aeronautes-benchmark-"}) {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("benchmark container survived: %+v", entry)
	}
}

func saioProxyTarget(t *testing.T) *url.URL {
	t.Helper()
	auth, err := url.Parse(os.Getenv("AERONAUTES_SAIO_AUTH_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if auth.Scheme == "" || auth.Host == "" {
		t.Fatal("SAIO proxy needs the configured authentication endpoint")
	}
	// The proxy serves both auth and storage paths, so its target is
	// the configured origin rather than the auth endpoint's path.
	return &url.URL{Scheme: auth.Scheme, Host: auth.Host}
}

// tlsFront starts an HTTPS reverse proxy in front of the SAIO, its
// auth response rewritten so the storage URL stays behind TLS.
func tlsFront(t *testing.T) (baseURL string, caPEM []byte, caFile string) {
	t.Helper()
	backend := saioProxyTarget(t)
	proxy := httputil.NewSingleHostReverseProxy(backend)
	server := httptest.NewUnstartedServer(proxy)
	server.StartTLS()
	t.Cleanup(server.Close)
	proxy.ModifyResponse = func(resp *http.Response) error {
		// The SAIO builds the storage URL from the Host header, so it
		// already names the proxy — but with the backend's scheme; and a
		// deployment that ignores Host would name the backend outright.
		// Rewrite both shapes onto the TLS front.
		if storage := resp.Header.Get("X-Storage-Url"); storage != "" {
			storage = strings.Replace(storage, backend.String(), server.URL, 1)
			storage = strings.Replace(storage, "http://"+server.Listener.Addr().String(), server.URL, 1)
			resp.Header.Set("X-Storage-Url", storage)
		}
		return nil
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	caFile = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return server.URL, caPEM, caFile
}

// TestTLSRealSwift is the §65.6 item-1 gate: real Swift behind a
// self-signed HTTPS terminator, exercised under system trust (refused),
// custom CA, and per-bookmark insecure — native client AND the real
// rclone child (override.ca_cert via the job config file, both remotes
// same trust).
func TestTLSRealSwift(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("pinned rclone not installed")
	}
	h := harness(t)
	ctx := context.Background()
	baseURL, caPEM, caFile := tlsFront(t)

	// System trust refuses the self-signed front.
	systemSession, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: baseURL + "/auth/v1.0", Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := systemSession.Authenticate(ctx); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("system trust accepted the self-signed front: %v", err)
	}

	// Custom CA verifies; the session browses the real cluster over TLS.
	caSession, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: baseURL + "/auth/v1.0", Username: "test:tester", APIKey: "testing",
		TLS: swiftcontrol.TLSConfig{Mode: swiftcontrol.TLSModeCustomCA, CACertPEM: caPEM},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := caSession.Authenticate(ctx); err != nil {
		t.Fatalf("custom-CA auth through TLS: %v", err)
	}
	if !strings.HasPrefix(caSession.StorageURL(), "https://") {
		t.Fatalf("storage URL not rewritten behind TLS: %s", caSession.StorageURL())
	}
	src := h.Container("tls-src")
	if _, err := caSession.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: src}); err != nil {
		t.Fatal(err)
	}
	if _, err := caSession.PutObject(ctx, src, "hello.txt", strings.NewReader("tls bytes"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}

	// The real rclone through the same TLS front: custom CA rides the
	// job config file (override.ca_cert), tokens stay in env, and the
	// copy round-trips.
	dst := h.Container("tls-dst")
	if _, err := caSession.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: dst}); err != nil {
		t.Fatal(err)
	}
	runner, err := rclone.NewRunner(rclone.Options{Retries: 1, LowLevelRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	remotes := []rclone.Remote{
		{Name: "aeronautes_src", StorageURL: caSession.StorageURL(), Token: caSession.AuthToken(), CACertFile: caFile},
		{Name: "aeronautes_dst", StorageURL: caSession.StorageURL(), Token: caSession.AuthToken(), CACertFile: caFile},
	}
	result, err := runner.Copy(ctx, remotes, "aeronautes_src:"+src, "aeronautes_dst:"+dst+"/.aeronautes/stage/it-tls/batch", []string{"hello.txt"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || len(result.Errors) != 0 {
		t.Fatalf("rclone over TLS: exit %d errors %v", result.ExitCode, result.Errors)
	}
	d, err := caSession.ObjectDetail(ctx, dst, ".aeronautes/stage/it-tls/batch/hello.txt")
	if err != nil || d.ContentLength != 9 {
		t.Fatalf("copied object: %+v %v", d, err)
	}

	// Without the CA the child fails TLS — never a silent fallback.
	badRemotes := []rclone.Remote{
		{Name: "aeronautes_src", StorageURL: caSession.StorageURL(), Token: caSession.AuthToken(), CACertFile: filepath.Join(t.TempDir(), "missing-but-forces-config.pem")},
		{Name: "aeronautes_dst", StorageURL: caSession.StorageURL(), Token: caSession.AuthToken(), CACertFile: filepath.Join(t.TempDir(), "missing-but-forces-config.pem")},
	}
	_ = badRemotes // an unreadable CA path: rclone fails the transfer
	untrusted, err := runner.Copy(ctx, []rclone.Remote{
		{Name: "aeronautes_src", StorageURL: caSession.StorageURL(), Token: caSession.AuthToken(), CACertFile: caFile},
		{Name: "aeronautes_dst", StorageURL: caSession.StorageURL(), Token: caSession.AuthToken()},
	}, "aeronautes_src:"+src, "aeronautes_dst:"+dst+"/.aeronautes/stage/it-tls2/batch", []string{"hello.txt"}, nil)
	if err == nil {
		t.Fatalf("mixed trust in one child accepted: %+v", untrusted)
	}

	// Insecure mode also works per-remote (both ends, loudly).
	insecureSession, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: baseURL + "/auth/v1.0", Username: "test:tester", APIKey: "testing",
		TLS: swiftcontrol.TLSConfig{Mode: swiftcontrol.TLSModeInsecure},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := insecureSession.Authenticate(ctx); err != nil {
		t.Fatalf("insecure auth: %v", err)
	}
	insecureRemotes := []rclone.Remote{
		{Name: "aeronautes_src", StorageURL: insecureSession.StorageURL(), Token: insecureSession.AuthToken(), InsecureTLS: true},
		{Name: "aeronautes_dst", StorageURL: insecureSession.StorageURL(), Token: insecureSession.AuthToken(), InsecureTLS: true},
	}
	result, err = runner.Copy(ctx, insecureRemotes, "aeronautes_src:"+src, "aeronautes_dst:"+dst+"/.aeronautes/stage/it-tls2/batch", []string{"hello.txt"}, nil)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("insecure rclone over TLS: %v %+v", err, result)
	}
}

// TestFeatureSurfacesRealSwift closes the README-claim loop for the
// display-grade features (§53): TempURL key slots, CORS, and staticweb
// configured on REAL Swift must parse into the container detail the
// inspector shows — mock parsing alone does not earn the claim. Values
// of TempURL keys are withheld by design; only slot names surface.
func TestFeatureSurfacesRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	capabilities, err := h.Session.FetchCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, feature := range []string{"tempurl", "staticweb"} {
		if capabilities.Feature(feature) == nil {
			t.Fatalf("SAIO should advertise %s", feature)
		}
	}

	container := h.Container("features")
	if err := h.conn.ContainerCreate(ctx, container, swift.Headers{
		"X-Container-Meta-Temp-URL-Key":                "smoke-secret-one",
		"X-Container-Meta-Temp-URL-Key-2":              "smoke-secret-two",
		"X-Container-Meta-Access-Control-Allow-Origin": "https://ops.example",
		"X-Container-Meta-Web-Index":                   "index.html",
	}); err != nil {
		t.Fatal(err)
	}
	d, err := h.Session.ContainerDetail(ctx, container)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.TempURLKeySlots) != 2 {
		t.Fatalf("TempURL key slots = %v, want both slots", d.TempURLKeySlots)
	}
	if !d.CORS || !d.StaticWeb {
		t.Fatalf("CORS=%v StaticWeb=%v, want both configured", d.CORS, d.StaticWeb)
	}
	// The special families never leak into plain user metadata, and no
	// secret value ever appears anywhere in the detail (K-SEC-002). Static
	// web keys are plain editable metadata since ADR 0090, so the one seeded
	// here must be in the set the editor shows.
	for k := range d.Meta {
		if strings.Contains(strings.ToLower(k), "temp-url") ||
			strings.Contains(strings.ToLower(k), "access-control") {
			t.Fatalf("special-family key %q leaked into plain metadata", k)
		}
	}
	if d.Meta["Web-Index"] != "index.html" {
		t.Fatalf("Meta[Web-Index] = %q, want the static web key in the editable set", d.Meta["Web-Index"])
	}
	for _, raw := range d.RawHeaders {
		if strings.Contains(raw, "smoke-secret") {
			t.Fatal("TempURL key value leaked into the raw header view")
		}
	}

	// Storage policy on real Swift: the detail reports the cluster's
	// actual policy for the created container (the SAIO carries one
	// policy; multi-policy selection is covered by the mock's picker
	// tests and gated by /info capabilities either way, §10/§20).
	if d.StoragePolicy == "" {
		t.Fatal("storage policy missing from container detail")
	}
	policies := capabilities.StoragePolicies()
	found := false
	for _, p := range policies {
		if p.Name == d.StoragePolicy {
			found = true
		}
	}
	if !found {
		t.Fatalf("detail policy %q not among advertised %v", d.StoragePolicy, policies)
	}
}

// Versioning cannot exclude a concurrent writer, so it must never turn
// a refused overwrite into a permitted write or create an archive entry.
func TestVersionedOverwriteRefusedRealSwift(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("pinned rclone not installed")
	}
	h := harness(t)
	if h.Session2() == nil {
		skipOrFailSecondAccount(t)
	}
	ctx := context.Background()

	src := h.Container("ver-src")
	if err := h.conn.ContainerCreate(ctx, src, nil); err != nil {
		t.Fatal(err)
	}
	// Versioned destination on account 2: archive container + live
	// container pointing at it.
	archive := h.Prefix + "ver-archive"
	dst := h.Prefix + "ver-dst"
	if err := h.rawPut2(ctx, archive, nil, ""); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, h.Conn2().StorageUrl+"/"+dst, nil)
	req.Header.Set("X-Auth-Token", h.Conn2().AuthToken)
	req.Header.Set("X-Versions-Location", archive)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode >= 300 {
		t.Fatalf("versioned container: %v %v", err, resp.Status)
	}
	resp.Body.Close()

	if err := h.put(ctx, src, "doc.txt", []byte("version TWO"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.rawPut2(ctx, dst+"/doc.txt", []byte("version one"), "text/plain"); err != nil {
		t.Fatal(err)
	}

	executor := executorFixture(t, h, src, dst, "job-versioned")
	report := planInto(ctx, t, h, executor, src, dst)
	if len(report.Failures) != 0 || report.Overwrites != 0 || report.OverwritesRefused != 1 || report.Copied != 0 {
		t.Fatalf("report = %+v (failures %+v)", report, report.Failures)
	}
	// Refusal preserves the live bytes and leaves the empty archive alone.
	live, _, err := h.Session2().OpenObject(ctx, dst, "doc.txt")
	if err != nil {
		t.Fatal(err)
	}
	liveBytes, _ := io.ReadAll(live)
	live.Close()
	if string(liveBytes) != "version one" {
		t.Fatalf("live = %q", liveBytes)
	}
	archived := 0
	for entry, err := range h.Session2().Objects(ctx, archive, swiftcontrol.ObjectListOptions{Deep: true}) {
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := entry.(swiftcontrol.ObjectEntry); ok {
			archived++
		}
	}
	if archived != 0 {
		t.Fatal("refused overwrite created an archive entry")
	}
}

// TestTokenRefreshAcrossSpawnsRealSwift is the §65.8 item-1 gate: a
// copy spanning multiple engine spawns must survive token expiry. An
// auth proxy in front of the SAIO advertises a 65-second token
// lifetime (X-Auth-Token-Expires rewritten — 65 and not less because
// the wire library itself refuses a token inside 60s of expiry), the
// per-spawn RemoteSource refreshes sessions inside the 2-minute
// margin, and the auth counter proves each child got a freshly-
// authenticated token — while the copy itself completes.
func TestTokenRefreshAcrossSpawnsRealSwift(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("pinned rclone not installed")
	}
	h := harness(t)
	ctx := context.Background()

	var authCalls int64
	backend := saioProxyTarget(t)
	proxy := httputil.NewSingleHostReverseProxy(backend)
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.Header.Get("X-Auth-Token") != "" {
			resp.Header.Set("X-Auth-Token-Expires", "65")
		}
		return nil
	}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			atomic.AddInt64(&authCalls, 1)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)

	session, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: front.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	if until := time.Until(session.TokenExpires()); until < 30*time.Second || until > 2*time.Minute {
		t.Fatalf("proxy expiry rewrite not in effect: %v", until)
	}

	src := h.Container("tok-src")
	dst := h.Container("tok-dst")
	for _, c := range []string{src, dst} {
		if _, err := session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: c}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		if _, err := session.PutObject(ctx, src, name, strings.NewReader("payload-"+name), "application/octet-stream", nil); err != nil {
			t.Fatal(err)
		}
	}

	runner, err := rclone.NewRunner(rclone.Options{Retries: 1, LowLevelRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	source := func(spawnCtx context.Context) ([]rclone.Remote, error) {
		credentials, err := session.Credentials(spawnCtx, 2*time.Minute)
		if err != nil {
			return nil, err
		}
		return []rclone.Remote{
			{Name: "aeronautes_src", StorageURL: credentials.StorageURL, Token: credentials.Token},
			{Name: "aeronautes_dst", StorageURL: credentials.StorageURL, Token: credentials.Token},
		}, nil
	}
	executor, err := transfercopy.New(transfercopy.Config{
		Source: session, Dest: session,
		Engine:  transfercopy.BindRunnerSource(runner, source),
		SrcSpec: "aeronautes_src:" + src, DstSpec: "aeronautes_dst:" + dst,
		SrcContainer: src, DstContainer: dst,
		JobID: "job-token", BatchSize: 1, // one child per object: three spawns
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := executor.Close(); err != nil {
			t.Error(err)
		}
	}()
	before := atomic.LoadInt64(&authCalls)
	_, err = plan.Copy(ctx,
		session.Objects(ctx, src, swiftcontrol.ObjectListOptions{Deep: true}),
		session.Objects(ctx, dst, swiftcontrol.ObjectListOptions{Deep: true}),
		"", "", func(item plan.Item) error { return executor.Handle(ctx, item) })
	if err != nil {
		t.Fatal(err)
	}
	report, err := executor.Finish(ctx)
	if err != nil || len(report.Failures) != 0 || report.Copied != 3 {
		t.Fatalf("copy: %v %+v (failures %+v)", err, report, report.Failures)
	}
	refreshes := atomic.LoadInt64(&authCalls) - before
	if refreshes < 3 {
		t.Fatalf("expected a re-authentication per spawn, saw %d", refreshes)
	}
	t.Logf("3 spawns → %d re-authentications through the 65s-expiry proxy; copy complete", refreshes)
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		d, err := session.ObjectDetail(ctx, dst, name)
		if err != nil || d.ContentLength != int64(len("payload-"+name)) {
			t.Fatalf("%s: %+v %v", name, d, err)
		}
	}
}

func TestSymlinkSemanticsRealSwift(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	c := h.Container("symlink-lab")
	if err := h.conn.ContainerCreate(ctx, c, nil); err != nil {
		t.Fatal(err)
	}

	if err := h.put(ctx, c, "target.txt", []byte("real bytes"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Session.PutObject(ctx, c, "link.txt", strings.NewReader(""), "text/plain",
		map[string]string{"X-Symlink-Target": c + "/target.txt", "X-Object-Meta-Kind": "pointer"}); err != nil {
		t.Fatal(err)
	}
	// A plain HEAD follows: target size, FollowedSymlink set.
	followed, err := h.Session.ObjectDetail(ctx, c, "link.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !followed.FollowedSymlink || followed.ContentLength != int64(len("real bytes")) {
		t.Fatalf("followed HEAD = %+v (middleware contract changed?)", followed)
	}
	// ?symlink=get addresses the link itself.
	link, err := h.Session.SymlinkGet(ctx, c, "link.txt")
	if err != nil {
		t.Fatal(err)
	}
	if link.SymlinkTarget != c+"/target.txt" || link.Meta["Kind"] != "pointer" {
		t.Fatalf("symlink=get = %+v", link)
	}
}
