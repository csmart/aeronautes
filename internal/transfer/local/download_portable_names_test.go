package local

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
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
					seed(mock, "media", prefix+name, downloadNamespaceBody(name))
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
				// Listings sort names; fixture declaration order is not their order.
				names := slices.Clone(fixture.names)
				slices.Sort(names)
				want := downloadNamespaceItems(prefix, route, names)
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
			names := slices.Clone(fixture.names)
			slices.Sort(names)
			refusal := ""
			if fixture.refused != "" {
				refusal = "does not preserve its unique selected name"
			}
			testDownloadNamespaceDirect(t, route, fixture.name, names, refusal)
		}
	}
}
