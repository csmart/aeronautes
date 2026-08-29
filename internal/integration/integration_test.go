package integration

import (
	"context"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
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
