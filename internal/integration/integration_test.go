package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
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
	if err := h.Session.DeleteMarker(ctx, name, "suit/"); err == nil {
		t.Error("DeleteMarker over a real object must refuse")
	}
	if err := h.Session.DeleteMarker(ctx, name, "photos/"); err != nil {
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
	if _, err := h.Session.DeleteManifestObject(ctx, fixtures, "del-dlo"); err != nil {
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
	if _, err := h.Session.DeleteManifestObject(ctx, fixtures, "del-slo"); err != nil {
		t.Fatal(err)
	}
	if got := countSegments(sloParts); got != sloSegments {
		t.Fatalf("SLO segments = %d after manifest delete, want %d", got, sloSegments)
	}

	// A plain object refuses and survives.
	if _, err := h.Session.DeleteManifestObject(ctx, fixtures, "tagged.txt"); err == nil {
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
		t.Skip("no second account on this cluster")
	}
	ctx := context.Background()

	runner, err := rclone.NewRunner(rclone.Options{ChunkSize: "1Mi", Retries: 1, LowLevelRetries: 2})
	if err != nil {
		t.Fatal(err)
	}

	// A disposable big object in the fixtures container.
	big := bytes.Repeat([]byte{0xBB}, 3*1024*1024)
	if err := h.put(ctx, h.Container("fixtures"), "runner/big.bin", big, "application/octet-stream", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.put(ctx, h.Container("fixtures"), "runner/small.txt", []byte("small"), "text/plain", nil); err != nil {
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
	result, err := runner.Copy(ctx, remotes,
		"aeronautes_src:"+h.Container("fixtures"), "aeronautes_dst:"+dstName,
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
	obj, _, err := h.Conn2().Object(ctx, dstName, "runner/big.bin")
	if err != nil || obj.Bytes != int64(len(big)) {
		t.Errorf("big.bin at dest: %v bytes=%d", err, obj.Bytes)
	}
	if _, _, err := h.Conn2().Object(ctx, dstName, "runner/small.txt"); err != nil {
		t.Errorf("small.txt at dest: %v", err)
	}
}

// TestCopyPlannerRealSwift merge-joins real deep listings across the two
// SAIO accounts: the full fixtures namespace (hostile names, DLO, SLO)
// against an empty destination, then a seeded pair for each verdict.
func TestCopyPlannerRealSwift(t *testing.T) {
	h := harness(t)
	if h.Session2() == nil {
		t.Skip("no second account on this cluster")
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
