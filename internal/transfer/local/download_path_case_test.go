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

type downloadPathCase struct {
	name     string
	names    []string
	conflict string
}

var downloadPathCases = []downloadPathCase{
	{"file_parent", []string{"000-safe", "Foo", "foo/child"}, "Foo"},
	{"deep_parent", []string{"000-safe", "parent/Foo", "parent/foo/a/child"}, "parent/Foo"},
	{"folded_path", []string{"000-safe", "TOP/Foo", "top/foo/child"}, "TOP/Foo"},
	{"nonadjacent", []string{"000-safe", "Foo", "Foo.txt", "foo/child"}, "Foo"},
	{"marker", []string{"000-safe/", "Foo", "foo/"}, "Foo"},
	{"deep_marker", []string{"000-safe/", "parent/Foo", "parent/foo/child/"}, "parent/Foo"},
	{"shared_parent", []string{"000-safe", "foo/one", "foo/two"}, ""},
	{"marker_child", []string{"000-safe/", "foo/", "foo/one"}, ""},
	{"disjoint", []string{"000-safe", "Foo", "bar/child"}, ""},
}

func downloadPathCaseItems(prefix, route string, fixture downloadPathCase) []DownloadItem {
	var items []DownloadItem
	for _, name := range fixture.names {
		item := DownloadItem{Name: prefix + name, RelPath: filepath.FromSlash(strings.TrimSuffix(name, "/")), Route: route}
		if strings.HasSuffix(name, "/") {
			item.Dir, item.Route = true, ""
		} else {
			item.Size = int64(len(downloadPathCaseBody(name)))
		}
		items = append(items, item)
	}
	return items
}

func downloadPathCaseBody(name string) string {
	if strings.HasSuffix(name, "/") {
		return ""
	}
	return "complete source bytes\x00\xff " + name
}

func TestDownloadPathCasePreflight(t *testing.T) {
	for _, route := range []string{"rclone", "native"} {
		for _, fixture := range downloadPathCases {
			t.Run(route+"/"+fixture.name, func(t *testing.T) {
				source, mock := session(t)
				prefix := "scope/"
				if route == "native" {
					prefix = "scope\n/"
				}
				for _, name := range fixture.names {
					seed(mock, "media", prefix+name, downloadPathCaseBody(name))
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
				if fixture.conflict != "" {
					if !plan.Rejected() || len(plan.Problems) != 1 {
						t.Fatalf("case-folded file/ancestor conflict was not refused: %+v", plan)
					}
					problem := plan.Problems[0]
					if !problem.Reject || problem.Kind != "file-vs-prefix" || problem.Name != prefix+fixture.conflict || !strings.Contains(problem.Detail, fmt.Sprintf("%q", fixture.conflict)) {
						t.Fatalf("refusal lost the selected file spelling: %+v", problem)
					}
					return
				}
				want := downloadPathCaseItems(prefix, route, fixture)
				if plan.Rejected() || !reflect.DeepEqual(plan.Items, want) {
					t.Fatalf("safe shared-parent control lost its exact plan: %+v want=%+v", plan, want)
				}
			})
		}
	}
}

func TestDownloadPathCaseDirect(t *testing.T) {
	for _, route := range []string{"rclone", "native"} {
		for _, fixture := range downloadPathCases {
			for _, recorded := range []bool{false, true} {
				for _, reverse := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/recorded_%v/reverse_%v", route, fixture.name, recorded, reverse), func(t *testing.T) {
						source, mock := session(t)
						prefix := "scope/"
						if route == "native" {
							prefix = "scope\n/"
						}
						for _, name := range fixture.names {
							seed(mock, "media", prefix+name, downloadPathCaseBody(name))
						}
						owned := t.TempDir()
						root := filepath.Join(owned, "selected")
						if err := os.Mkdir(root, 0o700); err != nil {
							t.Fatal(err)
						}
						writeFile(t, root, "keep", "selected sentinel\x00\xff")
						writeFile(t, owned, "outside", "outside sentinel\x00\xff")
						items := downloadPathCaseItems(prefix, route, fixture)
						if reverse {
							slices.Reverse(items)
						}
						before := downloadDestinationSnapshot(t, owned)
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
						report, err := Download(t.Context(), source, engine, "remote:media", "media", prefix, root, "fixture", &DownloadPreflight{Items: items},
							func(int64, int64, int64) { progressCalls++ }, recorders...)
						after := downloadDestinationSnapshot(t, owned)
						if fixture.conflict != "" {
							if err == nil || report != nil || engine.calls != 0 || mock.Requests() != requests || len(admissions)+len(executed)+len(verified)+progressCalls != 0 {
								t.Errorf("case-folded ancestor admitted a direct plan: report=%+v error=%v Copy calls=%d source requests=%d admissions=%+v executed=%+v verified=%+v progress=%d", report, err, engine.calls, mock.Requests()-requests, admissions, executed, verified, progressCalls)
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
						want := &Report{Done: int64(len(items)), Bytes: total}
						if route == "native" {
							want.NativeRouted = files
						}
						wantCalls := 0
						if route == "rclone" && files > 0 {
							wantCalls = 1
							want.StageCleanup = &StageCleanupResult{}
						}
						if err != nil || !reflect.DeepEqual(report, want) || engine.calls != wantCalls || progressCalls == 0 {
							t.Fatalf("safe direct control failed: report=%+v error=%v Copy calls=%d progress=%d", report, err, engine.calls, progressCalls)
						}
						if recorded {
							if len(admissions) != len(items) || len(executed) != len(items) || len(verified) != len(items) {
								t.Fatalf("safe control lost records: %+v %+v %+v", admissions, executed, verified)
							}
							for index, item := range items {
								a := admissions[index]
								x, v := executed[item], verified[item]
								if a.Item != item || x.Result != transfercopy.ResultCopied || x.Effect != transfercopy.EffectReportedApplied || x.CopiedBytes != item.Size || x.Diagnostic != (transfercopy.Diagnostic{}) ||
									v.Result != transfercopy.ResultVerified || v.Effect != transfercopy.EffectNotAttempted || v.Diagnostic != (transfercopy.Diagnostic{}) || v.CopiedBytes != 0 {
									t.Fatalf("safe control lost unchanged identity or observations: item=%+v admission=%+v execution=%+v verification=%+v", item, a, x, v)
								}
								if item.Dir && a.StagingPath != "" || !item.Dir && route == "rclone" && a.StagingPath != filepath.Join(root, ".aeronautes-stage-fixture", item.RelPath) ||
									!item.Dir && route == "native" && (filepath.Dir(a.StagingPath) != filepath.Dir(filepath.Join(root, item.RelPath)) || !strings.HasPrefix(filepath.Base(a.StagingPath), ".aeronautes-part-")) {
									t.Fatalf("safe control lost staging identity: %+v", a)
								}
							}
						}
						for path, state := range before {
							if after[path] != state {
								t.Errorf("safe control changed existing entry %q: %+v -> %+v", path, state, after[path])
							}
							delete(after, path)
						}
						for _, name := range fixture.names {
							path := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(name, "/")))
							if !strings.HasSuffix(name, "/") {
								state, ok := after[path]
								if !ok || !state.mode.IsRegular() || state.body != downloadPathCaseBody(name) {
									t.Fatalf("safe control full file bytes differ: %q %+v", path, state)
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
								t.Errorf("safe control directory missing: %q %+v", path, state)
							}
							delete(after, path)
						}
						if len(after) != 0 {
							t.Errorf("safe control left unexpected entries: %+v", after)
						}
					})
				}
			}
		}
	}
}
