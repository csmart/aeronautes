package local

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// Names that differ only by Unicode normalization form address one
// location on a normalizing filesystem, exactly as case-folded names do
// on a case-insensitive one. Both admission boundaries must refuse the
// whole selection before any effect, and every refusal the established
// case identity already produces must survive unchanged.

const (
	// The decomposed spelling is built from an explicit escape: typed as
	// a literal it would be normalized on the way into this file and the
	// fixture would compare one name with itself.
	nfcEtude = "\u00e9tude"
	nfdEtude = "e" + combiningAcute + "tude"
)

type nameFormCase struct {
	name  string
	names []string
	// ancestor is the selected file spelling a file-vs-prefix refusal must
	// name; twinKind is the expected kind when the refusal is a twin group.
	ancestor string
	twinKind string
	// pair and via record which single relation is expected to carry the
	// collision, so a case cannot pass through the other one by accident.
	pair [2]string
	via  string // "lowercase" or "caseless"
}

var nameFormCases = []nameFormCase{
	// Twin matrix: the five pairs, each recording the relation that carries it.
	{"twin_file", []string{"000-safe", nfcEtude + ".txt", nfdEtude + ".txt"}, "", "name-form",
		[2]string{nfcEtude + ".txt", nfdEtude + ".txt"}, "caseless"},
	{"twin_shared_parent", []string{"000-safe", "p/" + nfcEtude, "p/" + nfdEtude}, "", "name-form",
		[2]string{"p/" + nfcEtude, "p/" + nfdEtude}, "caseless"},
	{"twin_dotted_i_canonical", []string{"000-safe", dottedCapitalI, "I" + combiningDot}, "", "name-form",
		[2]string{dottedCapitalI, "I" + combiningDot}, "caseless"},
	{"twin_mixed_case_and_form", []string{"000-safe", "\u00c9tude", nfdEtude}, "", "name-form",
		[2]string{"\u00c9tude", nfdEtude}, "caseless"},
	{"twin_dotted_i_case", []string{"000-safe", "I" + combiningDot, "i" + combiningDot}, "", "case-fold",
		[2]string{"I" + combiningDot, "i" + combiningDot}, "lowercase"},
	{"twin_unicode_case", []string{"000-safe", "\u00c9tude", "\u00e9tude"}, "", "case-fold",
		[2]string{"\u00c9tude", "\u00e9tude"}, "lowercase"},
	{"twin_dotted_i_plain", []string{"000-safe", dottedCapitalI, "i"}, "", "case-fold",
		[2]string{dottedCapitalI, "i"}, "lowercase"},

	// Ancestor matrix: every pair in both directions, so the selected file
	// and the required ancestor exchange spellings.
	{"ancestor_precomposed_file", []string{"000-safe", nfcEtude, nfdEtude + "/child"}, nfcEtude, "",
		[2]string{nfcEtude, nfdEtude}, "caseless"},
	{"ancestor_decomposed_file", []string{"000-safe", nfdEtude, nfcEtude + "/child"}, nfdEtude, "",
		[2]string{nfdEtude, nfcEtude}, "caseless"},
	{"ancestor_marker", []string{"000-safe/", nfcEtude, nfdEtude + "/"}, nfcEtude, "",
		[2]string{nfcEtude, nfdEtude}, "caseless"},
	{"ancestor_nonadjacent", []string{"000-safe", nfcEtude, nfcEtude + ".txt", nfdEtude + "/child"}, nfcEtude, "",
		[2]string{nfcEtude, nfdEtude}, "caseless"},
	{"ancestor_mixed_upper_file", []string{"000-safe", "\u00c9tude", nfdEtude + "/child"}, "\u00c9tude", "",
		[2]string{"\u00c9tude", nfdEtude}, "caseless"},
	{"ancestor_mixed_decomposed_file", []string{"000-safe", nfdEtude, "\u00c9tude/child"}, nfdEtude, "",
		[2]string{nfdEtude, "\u00c9tude"}, "caseless"},
	{"ancestor_dotted_i_canonical_precomposed_file", []string{"000-safe", dottedCapitalI, "I" + combiningDot + "/child"}, dottedCapitalI, "",
		[2]string{dottedCapitalI, "I" + combiningDot}, "caseless"},
	{"ancestor_dotted_i_canonical_decomposed_file", []string{"000-safe", "I" + combiningDot, dottedCapitalI + "/child"}, "I" + combiningDot, "",
		[2]string{"I" + combiningDot, dottedCapitalI}, "caseless"},
	{"ancestor_unicode_case_upper_file", []string{"000-safe", "\u00c9tude", "\u00e9tude/child"}, "\u00c9tude", "",
		[2]string{"\u00c9tude", "\u00e9tude"}, "lowercase"},
	{"ancestor_unicode_case_lower_file", []string{"000-safe", "\u00e9tude", "\u00c9tude/child"}, "\u00e9tude", "",
		[2]string{"\u00e9tude", "\u00c9tude"}, "lowercase"},
	{"ancestor_dotted_i_case_upper_file", []string{"000-safe", "I" + combiningDot, "i" + combiningDot + "/child"}, "I" + combiningDot, "",
		[2]string{"I" + combiningDot, "i" + combiningDot}, "lowercase"},
	{"ancestor_dotted_i_case_lower_file", []string{"000-safe", "i" + combiningDot, "I" + combiningDot + "/child"}, "i" + combiningDot, "",
		[2]string{"i" + combiningDot, "I" + combiningDot}, "lowercase"},
	{"ancestor_dotted_i_plain_precomposed_file", []string{"000-safe", dottedCapitalI, "i/child"}, dottedCapitalI, "",
		[2]string{dottedCapitalI, "i"}, "lowercase"},
	{"ancestor_dotted_i_plain_plain_file", []string{"000-safe", "i", dottedCapitalI + "/child"}, "i", "",
		[2]string{"i", dottedCapitalI}, "lowercase"},

	// Accepted controls.
	{"safe_distinct_names", []string{"000-safe", "resume", "r\u00e9sum\u00e9"}, "", "", [2]string{}, ""},
	{"safe_sharp_s", []string{"000-safe", "\u00df", "ss"}, "", "", [2]string{}, ""},
	{"safe_ligature", []string{"000-safe", ligatureFi + "le", "file"}, "", "", [2]string{}, ""},
	{"safe_lone_decomposed", []string{"000-safe", nfdEtude + ".txt"}, "", "", [2]string{}, ""},
	{"safe_lone_precomposed", []string{"000-safe", nfcEtude + ".txt"}, "", "", [2]string{}, ""},
	{"safe_shared_parent", []string{"000-safe", nfcEtude + "/one", nfcEtude + "/two"}, "", "", [2]string{}, ""},
	{"safe_plain_i_vs_i_dot", []string{"000-safe", "i", "i" + combiningDot}, "", "", [2]string{}, ""},
}

func (c nameFormCase) refused() bool { return c.ancestor != "" || c.twinKind != "" }

func nameFormBody(name string) string {
	if strings.HasSuffix(name, "/") {
		return ""
	}
	return "complete source bytes\x00\xff " + name
}

func nameFormItems(prefix, route string, fixture nameFormCase) []DownloadItem {
	var items []DownloadItem
	for _, name := range fixture.names {
		item := DownloadItem{Name: prefix + name, RelPath: filepath.FromSlash(strings.TrimSuffix(name, "/")), Route: route}
		if strings.HasSuffix(name, "/") {
			item.Dir, item.Route = true, ""
		} else {
			item.Size = int64(len(nameFormBody(name)))
		}
		items = append(items, item)
	}
	return items
}

func TestDownloadNameFormPreflight(t *testing.T) {
	for _, route := range []string{"rclone", "native"} {
		for _, fixture := range nameFormCases {
			t.Run(route+"/"+fixture.name, func(t *testing.T) {
				source, mock := session(t)
				prefix := "scope/"
				if route == "native" {
					prefix = "scope\n/"
				}
				for _, name := range fixture.names {
					seed(mock, "media", prefix+name, nameFormBody(name))
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
				// A refusal must arrive through the relation the fixture
				// names, so a case cannot pass through the other one.
				if fixture.via != "" {
					first, second := fixture.pair[0], fixture.pair[1]
					lower := lowercaseIdentity(first) == lowercaseIdentity(second)
					caseless := caselessIdentity(first) == caselessIdentity(second)
					switch fixture.via {
					case "lowercase":
						if !lower {
							t.Fatalf("fixture claims the lowercase relation but it does not hold for %q and %q", first, second)
						}
					case "caseless":
						if lower {
							t.Fatalf("fixture claims the caseless relation but the lowercase relation already holds for %q and %q", first, second)
						}
						if !caseless {
							t.Fatalf("fixture claims the caseless relation but it does not hold for %q and %q", first, second)
						}
					default:
						t.Fatalf("unknown relation %q", fixture.via)
					}
				}
				if !fixture.refused() {
					// The plan follows listing order, so compare the exact
					// set rather than the fixture's declaration order.
					want := nameFormItems(prefix, route, fixture)
					got := append([]DownloadItem(nil), plan.Items...)
					byName := func(items []DownloadItem) {
						sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
					}
					byName(want)
					byName(got)
					if plan.Rejected() || !reflect.DeepEqual(got, want) {
						t.Fatalf("safe control lost its exact plan: %+v want=%+v", plan, want)
					}
					return
				}
				if !plan.Rejected() || len(plan.Problems) != 1 {
					t.Fatalf("collision was not refused: %+v", plan)
				}
				problem := plan.Problems[0]
				if !problem.Reject {
					t.Fatalf("collision was warned rather than refused: %+v", problem)
				}
				if fixture.ancestor != "" {
					if problem.Kind != "file-vs-prefix" || problem.Name != prefix+fixture.ancestor ||
						!strings.Contains(problem.Detail, fmt.Sprintf("%q", fixture.ancestor)) {
						t.Fatalf("refusal lost the selected file spelling: %+v", problem)
					}
					return
				}
				if problem.Kind != fixture.twinKind {
					t.Fatalf("twin refusal used kind %q, want %q: %+v", problem.Kind, fixture.twinKind, problem)
				}
				// The refusal must quote the exact selected spellings and
				// must never quote a normalized or lowercased rewrite.
				for _, name := range fixture.names[1:] {
					if !strings.Contains(problem.Detail, name) {
						t.Fatalf("refusal lost the exact spelling %q: %+v", name, problem)
					}
				}
			})
		}
	}
}

func TestDownloadNameFormDirect(t *testing.T) {
	for _, route := range []string{"rclone", "native"} {
		for _, fixture := range nameFormCases {
			for _, recorded := range []bool{false, true} {
				for _, reverse := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/recorded_%v/reverse_%v", route, fixture.name, recorded, reverse), func(t *testing.T) {
						source, mock := session(t)
						prefix := "scope/"
						if route == "native" {
							prefix = "scope\n/"
						}
						for _, name := range fixture.names {
							seed(mock, "media", prefix+name, nameFormBody(name))
						}
						owned := t.TempDir()
						root := filepath.Join(owned, "selected")
						if err := os.Mkdir(root, 0o700); err != nil {
							t.Fatal(err)
						}
						writeFile(t, root, "keep", "selected sentinel\x00\xff")
						writeFile(t, owned, "outside", "outside sentinel\x00\xff")
						items := nameFormItems(prefix, route, fixture)
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
						report, err := Download(t.Context(), source, engine, "remote:media", "media", prefix, root, "fixture",
							&DownloadPreflight{Items: items}, func(int64, int64, int64) { progressCalls++ }, recorders...)
						after := downloadDestinationSnapshot(t, owned)
						if !fixture.refused() {
							// An accepted control must prove the download
							// happened, not merely that admission returned.
							// A no-op executor that admits and returns an
							// empty successful report must fail here.
							var total, fileCount int64
							for _, item := range items {
								total += item.Size
								if !item.Dir {
									fileCount++
								}
							}
							want := &Report{Done: int64(len(items)), Bytes: total}
							wantCalls := 0
							if route == "native" {
								want.NativeRouted = fileCount
							} else if fileCount > 0 {
								wantCalls = 1
								want.StageCleanup = &StageCleanupResult{}
							}
							if err != nil || !reflect.DeepEqual(report, want) || engine.calls != wantCalls || progressCalls == 0 {
								t.Fatalf("accepted control lost its exact outcome: report=%+v want=%+v error=%v Copy calls=%d want=%d progress=%d",
									report, want, err, engine.calls, wantCalls, progressCalls)
							}
							if recorded {
								if len(admissions) != len(items) || len(executed) != len(items) || len(verified) != len(items) {
									t.Fatalf("accepted control lost records: admissions=%+v executed=%+v verified=%+v", admissions, executed, verified)
								}
								for index, item := range items {
									a := admissions[index]
									x, v := executed[item], verified[item]
									if a.Item != item || x.Result != transfercopy.ResultCopied || x.Effect != transfercopy.EffectReportedApplied ||
										x.CopiedBytes != item.Size || x.Diagnostic != (transfercopy.Diagnostic{}) ||
										v.Result != transfercopy.ResultVerified || v.Effect != transfercopy.EffectNotAttempted ||
										v.Diagnostic != (transfercopy.Diagnostic{}) || v.CopiedBytes != 0 {
										t.Fatalf("accepted control lost identity or observations: item=%+v admission=%+v execution=%+v verification=%+v", item, a, x, v)
									}
									if item.Dir && a.StagingPath != "" ||
										!item.Dir && route == "rclone" && a.StagingPath != filepath.Join(root, ".aeronautes-stage-fixture", item.RelPath) ||
										!item.Dir && route == "native" && (filepath.Dir(a.StagingPath) != filepath.Dir(filepath.Join(root, item.RelPath)) ||
											!strings.HasPrefix(filepath.Base(a.StagingPath), ".aeronautes-part-")) {
										t.Fatalf("accepted control lost staging identity: %+v", a)
									}
								}
							}
							// Every owned entry that existed must be
							// unchanged, every selected name must be present
							// with its exact bytes, and nothing else may
							// appear.
							remaining := map[string]downloadDestinationEntry{}
							for path, state := range after {
								remaining[path] = state
							}
							for path, state := range before {
								if remaining[path] != state {
									t.Errorf("accepted control changed existing entry %q: %+v -> %+v", path, state, remaining[path])
								}
								delete(remaining, path)
							}
							for _, name := range fixture.names {
								path := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(name, "/")))
								if strings.HasSuffix(name, "/") {
									continue
								}
								state, ok := remaining[path]
								if !ok || !state.mode.IsRegular() || state.body != nameFormBody(name) {
									t.Fatalf("accepted control lost the full file bytes for %q: %+v", name, state)
								}
								delete(remaining, path)
							}
							permitted := map[string]bool{}
							for _, name := range fixture.names {
								path := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(name, "/")))
								if !strings.HasSuffix(name, "/") {
									path = filepath.Dir(path)
								}
								for path != root && strings.HasPrefix(path, root) {
									permitted[path] = true
									path = filepath.Dir(path)
								}
							}
							for path := range permitted {
								if state, ok := remaining[path]; !ok || !state.mode.IsDir() {
									t.Errorf("accepted control is missing the directory %q: %+v", path, state)
								}
								delete(remaining, path)
							}
							if len(remaining) != 0 {
								t.Errorf("accepted control left unexpected entries: %+v", remaining)
							}
							return
						}
						if err == nil || report != nil || engine.calls != 0 || mock.Requests() != requests ||
							len(admissions)+len(executed)+len(verified)+progressCalls != 0 {
							t.Errorf("collision admitted a direct plan: report=%+v error=%v Copy calls=%d source requests=%d admissions=%+v executed=%+v verified=%+v progress=%d",
								report, err, engine.calls, mock.Requests()-requests, admissions, executed, verified, progressCalls)
						}
						if !reflect.DeepEqual(before, after) {
							t.Errorf("refused direct plan changed owned entries: before=%+v after=%+v", before, after)
						}
					})
				}
			}
		}
	}
}
