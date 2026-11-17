package local

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

type downloadPortableCase struct {
	name string
	// names are the selected names below the prefix, safe member first.
	names []string
	// refused is the selected name whose component cannot be materialized
	// on every target, empty for a passing control.
	refused string
	// component is the exact spelling the refusal must quote.
	component string
}

var downloadPortableCases = []downloadPortableCase{
	{"device_leaf", []string{"000-safe", "CON"}, "CON", "CON"},
	{"device_case", []string{"000-safe", "NuL"}, "NuL", "NuL"},
	{"device_extension", []string{"000-safe", "NUL.txt"}, "NUL.txt", "NUL.txt"},
	{"device_space_extension", []string{"000-safe", "AUX .txt"}, "AUX .txt", "AUX .txt"},
	{"device_com_digit", []string{"000-safe", "COM1"}, "COM1", "COM1"},
	{"device_superscript", []string{"000-safe", "LPT³"}, "LPT³", "LPT³"},
	{"console_identifier", []string{"000-safe", "CONIN$"}, "CONIN$", "CONIN$"},
	{"console_extension", []string{"000-safe", "conout$.log"}, "conout$.log", "conout$.log"},
	{"device_ancestor", []string{"000-safe", "parent/CON/child"}, "parent/CON/child", "CON"},
	{"device_deep_ancestor", []string{"000-safe", "a/b/PRN/c/d"}, "a/b/PRN/c/d", "PRN"},
	{"device_marker", []string{"000-safe/", "CON/"}, "CON/", "CON"},
	{"restricted_colon", []string{"000-safe", "bad:name"}, "bad:name", "bad:name"},
	{"restricted_angle", []string{"000-safe", "a<b"}, "a<b", "a<b"},
	{"restricted_quote", []string{"000-safe", "quote\"x"}, "quote\"x", "quote\"x"},
	{"restricted_pipe", []string{"000-safe", "pipe|x"}, "pipe|x", "pipe|x"},
	{"restricted_question", []string{"000-safe", "q?"}, "q?", "q?"},
	{"restricted_star_ancestor", []string{"000-safe", "parent/star*/child"}, "parent/star*/child", "star*"},
	{"trailing_dot", []string{"000-safe", "file."}, "file.", "file."},
	{"trailing_dot_ancestor", []string{"000-safe", "parent./child"}, "parent./child", "parent."},
	{"trailing_space_ancestor", []string{"000-safe", "parent /child"}, "parent /child", "parent "},
	{"trailing_dot_marker", []string{"000-safe/", "mark./"}, "mark./", "mark."},
	{"near_miss_devices", []string{"000-safe", "NULx", "CONSOLE", "_CON", "COM10", "COM1x", "COM0", "LPT0", "COM", "LPT", "x.NUL", ".CON", "console.txt", "A¹", "CONIN", "CONOUT", "xCONOUT$"}, "", ""},
	{"near_miss_punctuation", []string{"000-safe", "a-b_c.d", "(x)", "100%", "#1", "a'b"}, "", ""},
	{"near_miss_spacing", []string{"000-safe", "a b", ".hidden", "trail。", "COM¹x", " lead"}, "", ""},
	{"shared_parent", []string{"000-safe", "foo/one", "foo/two"}, "", ""},
	{"marker_child", []string{"000-safe/", "foo/", "foo/one"}, "", ""},
}

// downloadPortableItems builds the exact plan the listing produces: the
// source lists sorted names, so a control expectation cannot assume the
// fixture insertion order.
func downloadPortableItems(prefix, route string, fixture downloadPortableCase) []DownloadItem {
	names := slices.Clone(fixture.names)
	slices.Sort(names)
	var items []DownloadItem
	for _, name := range names {
		item := DownloadItem{Name: prefix + name, RelPath: filepath.FromSlash(strings.TrimSuffix(name, "/")), Route: route}
		if strings.HasSuffix(name, "/") {
			item.Dir, item.Route = true, ""
		} else {
			item.Size = int64(len(downloadPortableBody(name)))
		}
		items = append(items, item)
	}
	return items
}

func downloadPortableBody(name string) string {
	if strings.HasSuffix(name, "/") {
		return ""
	}
	return "complete source bytes\x00\xff " + name
}

func TestDownloadPortableNamesPreflight(t *testing.T) {
	for _, route := range []string{"rclone", "native"} {
		for _, fixture := range downloadPortableCases {
			t.Run(route+"/"+fixture.name, func(t *testing.T) {
				source, mock := session(t)
				prefix := "scope/"
				if route == "native" {
					prefix = "scope\n/"
				}
				for _, name := range fixture.names {
					seed(mock, "media", prefix+name, downloadPortableBody(name))
				}
				root := t.TempDir()
				writeFile(t, root, "keep", "unrelated bytes\x00\xff")
				before := downloadDestinationSnapshot(t, root)
				plan, err := PreflightDownload(t.Context(), source, "media", prefix, root)
				if !reflect.DeepEqual(before, downloadDestinationSnapshot(t, root)) {
					t.Error("preflight changed the selected directory")
				}
				if err != nil || plan == nil {
					t.Fatalf("preflight failed outside the namespace policy: %+v %v", plan, err)
				}
				if fixture.refused != "" {
					if !plan.Rejected() || len(plan.Problems) != 1 {
						t.Fatalf("unportable component was not refused: %+v", plan)
					}
					problem := plan.Problems[0]
					if !problem.Reject || problem.Kind != "unportable-name" || problem.Name != prefix+fixture.refused ||
						!strings.Contains(problem.Detail, fmt.Sprintf("%q", fixture.component)) {
						t.Fatalf("refusal lost the selected spelling: %+v", problem)
					}
					return
				}
				want := downloadPortableItems(prefix, route, fixture)
				if plan.Rejected() || !reflect.DeepEqual(plan.Items, want) {
					t.Fatalf("portable near-miss control lost its exact plan: %+v want=%+v", plan, want)
				}
			})
		}
	}
}

func TestDownloadPortableNamesDirect(t *testing.T) {
	for _, route := range []string{"rclone", "native"} {
		for _, fixture := range downloadPortableCases {
			for _, recorded := range []bool{false, true} {
				for _, reverse := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/recorded_%v/reverse_%v", route, fixture.name, recorded, reverse), func(t *testing.T) {
						source, mock := session(t)
						prefix := "scope/"
						if route == "native" {
							prefix = "scope\n/"
						}
						for _, name := range fixture.names {
							seed(mock, "media", prefix+name, downloadPortableBody(name))
						}
						owned := t.TempDir()
						root := filepath.Join(owned, "selected")
						if err := os.Mkdir(root, 0o700); err != nil {
							t.Fatal(err)
						}
						writeFile(t, root, "keep", "selected sentinel\x00\xff")
						writeFile(t, owned, "outside", "outside sentinel\x00\xff")
						items := downloadPortableItems(prefix, route, fixture)
						if reverse {
							slices.Reverse(items)
						}
						before := downloadDestinationSnapshot(t, owned)
						plan := &DownloadPreflight{Items: items}
						requests := mock.Requests()
						engine := &downloadRecordingEngine{Engine: &fsEngine{session: source}}
						var admissions []DownloadAdmission
						executed, verified := map[DownloadItem]transfercopy.Observation{}, map[DownloadItem]transfercopy.Observation{}
						progressCalls := 0
						var recorders []*DownloadRecorder
						if recorded {
							recorders = []*DownloadRecorder{{
								Admit:   func(admission DownloadAdmission) error { admissions = append(admissions, admission); return nil },
								Execute: func(item DownloadItem, result transfercopy.Observation) error { executed[item] = result; return nil },
								Verify:  func(item DownloadItem, result transfercopy.Observation) error { verified[item] = result; return nil },
							}}
						}
						report, err := Download(t.Context(), source, engine, "remote:media", "media", prefix, root, plan,
							func(int64, int64, int64) { progressCalls++ }, recorders...)
						after := downloadDestinationSnapshot(t, owned)
						if fixture.refused != "" {
							if err == nil || report != nil || engine.calls != 0 || mock.Requests() != requests || len(admissions)+len(executed)+len(verified)+progressCalls != 0 {
								t.Errorf("unportable component admitted a direct plan: report=%+v error=%v Copy calls=%d source requests=%d admissions=%+v executed=%+v verified=%+v progress=%d",
									report, err, engine.calls, mock.Requests()-requests, admissions, executed, verified, progressCalls)
							}
							if err != nil && !strings.Contains(err.Error(), "does not preserve its unique selected name") {
								t.Errorf("direct refusal changed its reported reason: %v", err)
							}
							if !reflect.DeepEqual(before, after) {
								t.Errorf("refused direct plan changed owned entries: before=%+v after=%+v", before, after)
							}
							return
						}
						var total, files int64
						for _, item := range items {
							total += item.Size
							if !item.Dir {
								files++
							}
						}
						want := &Report{Done: int64(len(items)), Verified: int64(len(items)), Bytes: total}
						if route == "native" {
							want.NativeRouted = files
						}
						wantCalls := 0
						if route == "rclone" && files > 0 {
							wantCalls = 1
						}
						if err != nil || !reflect.DeepEqual(report, want) || engine.calls != wantCalls || progressCalls == 0 {
							t.Fatalf("portable near-miss control failed: report=%+v error=%v Copy calls=%d progress=%d", report, err, engine.calls, progressCalls)
						}
						if recorded {
							if len(admissions) != len(items) || len(executed) != len(items) || len(verified) != len(items) {
								t.Fatalf("near-miss control lost records: %+v %+v %+v", admissions, executed, verified)
							}
							for index, item := range items {
								a := admissions[index]
								x, v := executed[item], verified[item]
								if a.Item != item || x.Result != transfercopy.ResultCopied || x.Effect != transfercopy.EffectReportedApplied || x.CopiedBytes != item.Size || x.Diagnostic != (transfercopy.Diagnostic{}) ||
									v.Result != transfercopy.ResultVerified || v.Effect != transfercopy.EffectNotAttempted || v.Diagnostic != (transfercopy.Diagnostic{}) || v.CopiedBytes != 0 {
									t.Fatalf("near-miss control lost unchanged identity or observations: item=%+v admission=%+v execution=%+v verification=%+v", item, a, x, v)
								}
							}
						}
						for path, state := range before {
							if after[path] != state {
								t.Errorf("near-miss control changed existing entry %q: %+v -> %+v", path, state, after[path])
							}
							delete(after, path)
						}
						for _, name := range fixture.names {
							path := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(name, "/")))
							if !strings.HasSuffix(name, "/") {
								state, ok := after[path]
								if !ok || !state.mode.IsRegular() || state.body != downloadPortableBody(name) {
									t.Fatalf("near-miss control full file bytes differ: %q %+v", path, state)
								}
								delete(after, path)
							}
						}
						permittedDirectories := map[string]bool{}
						for _, name := range fixture.names {
							path := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(name, "/")))
							if !strings.HasSuffix(name, "/") {
								path = filepath.Dir(path)
							}
							for path != root {
								permittedDirectories[path] = true
								path = filepath.Dir(path)
							}
						}
						for path := range permittedDirectories {
							if state, ok := after[path]; !ok || !state.mode.IsDir() {
								t.Errorf("near-miss control directory missing: %q %+v", path, state)
							}
							delete(after, path)
						}
						if len(after) != 0 {
							t.Errorf("near-miss control left unexpected entries: %+v", after)
						}
					})
				}
			}
		}
	}
}
