package local

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
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
	{"mirrored_file_parent", []string{"000-safe", "foo", "Foo/child"}, "foo"},
	{"mirrored_deep_parent", []string{"000-safe", "top/foo", "TOP/Foo/a/child"}, "top/foo"},
	{"mirrored_marker", []string{"000-safe/", "foo", "Foo/"}, "foo"},
	{"mixed_parent", []string{"000-safe", "fOO", "FoO/child"}, "fOO"},
	{"shared_parent", []string{"000-safe", "foo/one", "foo/two"}, ""},
	{"marker_child", []string{"000-safe/", "foo/", "foo/one"}, ""},
	{"disjoint", []string{"000-safe", "Foo", "bar/child"}, ""},
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
				want := downloadNamespaceItems(prefix, route, fixture.names)
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
			refusal := ""
			if fixture.conflict != "" {
				refusal = "download selection"
			}
			testDownloadNamespaceDirect(t, route, fixture.name, fixture.names, refusal)
		}
	}
}
