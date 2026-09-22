package local

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Directory-only aliases, planned directory names that differ only by
// case or Unicode normalization form, are admitted at preflight and at
// the executor's admission whether they are spelled as markers or
// implied by files, and every file beneath an aliased directory is
// routed natively so the pinned engine never receives the pair; file
// twins and file-versus-directory pairs stay refused with their
// existing kinds. The archived before probe under
// docs/experiments/probes/2026-09-12-download-directory-aliases*
// recorded marker twins refused and an NFC/NFD pair ending partial on
// the engine route.
type dirAliasCase struct {
	name    string
	objects []string
	// native lists the files routed natively; every other file keeps the
	// engine route and every marker is a directory item.
	native []string
	// refusedKind and refusedName describe the single problem when the
	// selection is refused; empty means admitted. The listing names the
	// byte-order first spelling, which is the NFD form of a pair.
	refusedKind, refusedName string
}

func dirAliasCases() []dirAliasCase {
	nfc, nfd := "Bär", "Bär"
	return []dirAliasCase{
		{name: "marker_twins", objects: []string{"Foo/", "foo/"}},
		{name: "marker_form_twins", objects: []string{nfc + "/", nfd + "/"}},
		{name: "marker_twins_with_children", objects: []string{"Foo/", "foo/", "Foo/a.txt", "foo/b.txt"}, native: []string{"Foo/a.txt", "foo/b.txt"}},
		{name: "marker_beside_implicit", objects: []string{"Foo/", "foo/child.txt"}, native: []string{"foo/child.txt"}},
		{name: "implicit_twins", objects: []string{"Foo/a.txt", "foo/b.txt"}, native: []string{"Foo/a.txt", "foo/b.txt"}},
		{name: "implicit_form_twins", objects: []string{nfc + "/a.txt", nfd + "/b.txt"}, native: []string{nfc + "/a.txt", nfd + "/b.txt"}},
		{name: "deep_implicit_twins", objects: []string{"top/Foo/a.txt", "TOP/foo/b.txt"}, native: []string{"top/Foo/a.txt", "TOP/foo/b.txt"}},
		{name: "three_spellings", objects: []string{"Foo/", "foo/a.txt", "FOO/b.txt"}, native: []string{"foo/a.txt", "FOO/b.txt"}},
		{name: "alias_beside_plain", objects: []string{"Foo/a.txt", "foo/b.txt", "bar/c.txt", "d.txt"}, native: []string{"Foo/a.txt", "foo/b.txt"}},
		{name: "file_twins", objects: []string{"Foo", "foo"}, refusedKind: "case-fold", refusedName: "Foo"},
		{name: "file_twins_inside_aliases", objects: []string{"Foo/x.txt", "foo/x.txt"}, refusedKind: "case-fold", refusedName: "Foo/x.txt"},
		{name: "file_form_twins_inside_aliases", objects: []string{nfc + "/x.txt", nfd + "/x.txt"}, refusedKind: "name-form", refusedName: nfd + "/x.txt"},
		{name: "file_vs_marker", objects: []string{"foo", "Foo/"}, refusedKind: "file-vs-prefix", refusedName: "foo"},
		{name: "file_vs_implicit", objects: []string{"foo", "Foo/child.txt"}, refusedKind: "file-vs-prefix", refusedName: "foo"},
		{name: "file_vs_marker_form", objects: []string{nfc, nfd + "/"}, refusedKind: "file-vs-prefix", refusedName: nfc},
	}
}

func dirAliasBody(name string) string {
	if strings.HasSuffix(name, "/") {
		return ""
	}
	return "bytes for " + name
}

// dirAliasItems builds the plan the preflight is expected to produce,
// in the listing's byte order.
func dirAliasItems(prefix string, c dirAliasCase) []DownloadItem {
	native := map[string]bool{}
	for _, name := range c.native {
		native[name] = true
	}
	names := append([]string(nil), c.objects...)
	sortByBytes(names)
	var items []DownloadItem
	for _, name := range names {
		item := DownloadItem{Name: prefix + name, RelPath: filepath.FromSlash(strings.TrimSuffix(name, "/")), Route: "rclone"}
		switch {
		case strings.HasSuffix(name, "/"):
			item.Dir, item.Route = true, ""
		case native[name]:
			item.Route, item.Size = "native", int64(len(dirAliasBody(name)))
		default:
			item.Size = int64(len(dirAliasBody(name)))
		}
		items = append(items, item)
	}
	return items
}

func sortByBytes(names []string) {
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
}

func TestDownloadDirAliasesPreflight(t *testing.T) {
	for _, c := range dirAliasCases() {
		t.Run(c.name, func(t *testing.T) {
			source, mock := session(t)
			for _, name := range c.objects {
				seed(mock, "media", "scope/"+name, dirAliasBody(name))
			}
			root := t.TempDir()
			writeFile(t, root, "keep", "unrelated bytes")
			before := downloadDestinationSnapshot(t, root)
			plan, err := PreflightDownload(t.Context(), source, "media", "scope/", root)
			if err != nil || plan == nil {
				t.Fatalf("preflight failed outside the namespace policy: %+v %v", plan, err)
			}
			if !reflect.DeepEqual(before, downloadDestinationSnapshot(t, root)) {
				t.Error("preflight changed the selected directory")
			}
			if c.refusedKind != "" {
				if !plan.Rejected() || len(plan.Problems) != 1 {
					t.Fatalf("selection was not refused as one problem: %+v", plan)
				}
				problem := plan.Problems[0]
				if !problem.Reject || problem.Kind != c.refusedKind || problem.Name != "scope/"+c.refusedName {
					t.Fatalf("refusal differs from the delivered rule: %+v", problem)
				}
				return
			}
			if plan.Rejected() || !reflect.DeepEqual(plan.Items, dirAliasItems("scope/", c)) {
				t.Fatalf("admission or routing differs: %+v want=%+v", plan, dirAliasItems("scope/", c))
			}
		})
	}
}

// The executor's admission agrees with preflight on a direct plan built
// from the same listing, and an admitted plan creates every spelling as
// its own directory on this case-sensitive host with exact bytes.
func TestDownloadDirAliasesDirect(t *testing.T) {
	for _, c := range dirAliasCases() {
		t.Run(c.name, func(t *testing.T) {
			source, mock := session(t)
			for _, name := range c.objects {
				seed(mock, "media", "scope/"+name, dirAliasBody(name))
			}
			owned := t.TempDir()
			root := filepath.Join(owned, "selected")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			writeFile(t, root, "keep", "selected sentinel")
			before := downloadDestinationSnapshot(t, owned)
			items := dirAliasItems("scope/", c)
			var total int64
			for _, item := range items {
				total += item.Size
			}
			engine := &downloadRecordingEngine{Engine: &fsEngine{session: source}}
			report, err := Download(t.Context(), source, engine, "remote:media", "media", "scope/", root, "aliases",
				plannedWithIdentities(t, source, "media", &DownloadPreflight{Items: items, Objects: int64(len(items)), TotalBytes: total}), nil)
			after := downloadDestinationSnapshot(t, owned)
			if c.refusedKind != "" {
				if err == nil || report != nil || engine.calls != 0 || !reflect.DeepEqual(before, after) {
					t.Fatalf("executor admitted a refused selection: report=%+v error=%v calls=%d", report, err, engine.calls)
				}
				return
			}
			if err != nil || report == nil || len(report.Failures) != 0 || report.Done != int64(len(items)) || report.NativeRouted != int64(len(c.native)) {
				t.Fatalf("admitted selection did not complete: report=%+v error=%v", report, err)
			}
			engineFiles := 0
			for _, item := range items {
				if !item.Dir && item.Route != "native" {
					engineFiles++
				}
			}
			if engineFiles > 0 && engine.calls != 1 || engineFiles == 0 && engine.calls != 0 {
				t.Fatalf("engine route differs: calls=%d engine files=%d", engine.calls, engineFiles)
			}
			want := map[string]downloadDestinationEntry{}
			for path, entry := range before {
				want[path] = entry
			}
			for _, name := range c.objects {
				rel := filepath.FromSlash(strings.TrimSuffix(name, "/"))
				if strings.HasSuffix(name, "/") {
					want[filepath.Join(root, rel)] = downloadDestinationEntry{mode: after[filepath.Join(root, rel)].mode}
					continue
				}
				want[filepath.Join(root, rel)] = downloadDestinationEntry{mode: after[filepath.Join(root, rel)].mode, body: dirAliasBody(name)}
				for dir := filepath.Dir(rel); dir != "."; dir = filepath.Dir(dir) {
					want[filepath.Join(root, dir)] = downloadDestinationEntry{mode: after[filepath.Join(root, dir)].mode}
				}
			}
			for path, entry := range want {
				got, exists := after[path]
				if !exists || got.body != entry.body || got.link != "" || (entry.body == "" && !got.mode.IsDir()) || (entry.body != "" && !got.mode.IsRegular()) {
					t.Errorf("owned entry %q differs: got=%+v want=%+v", path, got, entry)
				}
			}
			for path := range after {
				if _, expected := want[path]; !expected {
					t.Errorf("unexpected owned entry %q: %+v", path, after[path])
				}
			}
		})
	}
}
