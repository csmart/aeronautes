package systemtest

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// Through the real service on both providers: directory-only aliases,
// spelled as markers or implied by files, are admitted and complete with
// every spelling as its own directory on this case-sensitive host and
// exact bytes, with the files beneath aliased directories routed
// natively so the pinned engine never drops one spelling as a duplicate;
// file twins and file-versus-directory pairs stay refused before any
// write.
type dirAliasServiceCase struct {
	name    string
	objects []string
	native  int
	// refusedKind names the preflight problem kind when the selection is
	// refused; empty means admitted.
	refusedKind string
}

func dirAliasServiceCases() []dirAliasServiceCase {
	nfc, nfd := "Bär", "Bär"
	return []dirAliasServiceCase{
		{name: "marker_twins", objects: []string{"Foo/", "foo/"}},
		{name: "marker_form_twins", objects: []string{nfc + "/", nfd + "/"}},
		{name: "marker_twins_with_children", objects: []string{"Foo/", "foo/", "Foo/a.txt", "foo/b.txt"}, native: 2},
		{name: "marker_beside_implicit", objects: []string{"Foo/", "foo/child.txt"}, native: 1},
		{name: "implicit_twins", objects: []string{"Foo/a.txt", "foo/b.txt"}, native: 2},
		{name: "implicit_form_twins", objects: []string{nfc + "/a.txt", nfd + "/b.txt"}, native: 2},
		{name: "deep_implicit_twins", objects: []string{"top/Foo/a.txt", "TOP/foo/b.txt"}, native: 2},
		{name: "three_spellings", objects: []string{"Foo/", "foo/a.txt", "FOO/b.txt"}, native: 2},
		{name: "alias_beside_plain", objects: []string{"Foo/a.txt", "foo/b.txt", "bar/c.txt", "d.txt"}, native: 2},
		{name: "file_twins_inside_aliases", objects: []string{"Foo/x.txt", "foo/x.txt"}, refusedKind: "case-fold"},
		{name: "file_form_twins_inside_aliases", objects: []string{nfc + "/x.txt", nfd + "/x.txt"}, refusedKind: "name-form"},
		{name: "file_vs_marker", objects: []string{"foo", "Foo/"}, refusedKind: "file-vs-prefix"},
	}
}

func TestDownloadDirAliasesMock(t *testing.T) {
	t.Parallel()
	for _, c := range dirAliasServiceCases() {
		t.Run(c.name, func(t *testing.T) { downloadDirAliases(t, findMockHarness(t), c) })
	}
}

func TestDownloadDirAliasesRealSwift(t *testing.T) {
	t.Parallel()
	for _, c := range dirAliasServiceCases() {
		t.Run(c.name, func(t *testing.T) { downloadDirAliases(t, New(t), c) })
	}
}

func dirAliasServiceBody(name string) []byte {
	if strings.HasSuffix(name, "/") {
		return nil
	}
	return []byte("bytes for " + name)
}

func downloadDirAliases(t *testing.T, h *Harness, c dirAliasServiceCase) {
	t.Helper()
	source := h.Container(h.Primary, "dir-aliases")
	owned := t.TempDir()
	selected := filepath.Join(owned, "selected")
	seedListingLocal(t, owned, "selected/keep", []byte("selected sentinel"))
	for _, name := range c.objects {
		h.Seed(h.Primary, source, "scope/"+name, dirAliasServiceBody(name), nil)
	}
	h.Seed(h.Primary, source, "outside-selection", []byte("unselected remote sentinel"), nil)
	beforeA := h.SnapshotNamespace(h.Primary)
	trace := copyCompletionProxy(t, h, h.Primary, "")
	app.WireDownloadStageBoundary(h.Transfer, func(context.Context, string) error { return nil })
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: selected})
	picked, err := h.Transfer.PickDownloadDir(t.Context())
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	request := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: "scope/", DestDirToken: picked.Token}
	before := snapshotListingLocal(t, owned)
	preflight, err := h.Transfer.PreflightDownload(t.Context(), request)
	if err != nil || preflight == nil {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	encoded, _ := json.Marshal(map[string]any{"case": c.name, "items": len(preflight.Items), "problems": preflight.Problems})
	t.Logf("directory alias observation: %s", encoded)
	if c.refusedKind != "" {
		if !preflight.Rejected() || len(preflight.Problems) != 1 || preflight.Problems[0].Kind != c.refusedKind {
			t.Fatalf("refusal differs: %+v", preflight.Problems)
		}
		if _, err := h.Transfer.StartDownload(t.Context(), request); err == nil {
			t.Fatal("start accepted a refused selection")
		}
		if after := snapshotListingLocal(t, owned); len(after) != len(before) {
			t.Fatalf("refused selection changed the local tree: %v", after)
		}
	} else {
		if preflight.Rejected() || len(preflight.Items) != len(c.objects) {
			t.Fatalf("selection was not admitted: %+v", preflight)
		}
		id, err := h.Transfer.StartDownload(t.Context(), request)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		job := h.AwaitJob(id)
		copyCompletionAwaitPins(t, h, h.Primary)
		saved := uploadRecoveryRead(t, h.database, id)
		encoded, _ := json.Marshal(map[string]any{"case": c.name, "state": job.State, "error": job.Error, "counters": job.Counters, "totals": saved.ItemTotals})
		t.Logf("directory alias outcome: %s", encoded)
		if job.State != "complete" || job.Error != "" || job.Counters.ObjectsCopied != int64(len(c.objects)) || job.Counters.NativeRouted != int64(c.native) || saved.ItemTotals.Failed != 0 {
			t.Fatalf("job outcome differs: state=%s error=%q counters=%+v totals=%+v", job.State, job.Error, job.Counters, saved.ItemTotals)
		}
		files, dirs := map[string][]byte{}, map[string]bool{}
		for _, name := range c.objects {
			rel := strings.TrimSuffix(name, "/")
			if strings.HasSuffix(name, "/") {
				dirs["selected/"+rel] = true
			} else {
				files["selected/"+name] = dirAliasServiceBody(name)
			}
			for dir := filepath.ToSlash(filepath.Dir(rel)); dir != "."; dir = filepath.ToSlash(filepath.Dir(dir)) {
				dirs["selected/"+dir] = true
			}
		}
		var newDirs []string
		for dir := range dirs {
			newDirs = append(newDirs, dir)
		}
		assertListingLocalDelta(t, before, snapshotListingLocal(t, owned), files, newDirs...)
	}
	if len(trace.writeSnapshot()) != 0 {
		t.Errorf("download changed remote data: %v", trace.writeSnapshot())
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
}
