package local

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// A path component longer than 255 bytes is refused at preflight and at
// the executor's item admission as one lexical rule that names the length
// and the limit, wherever the component sits; a 255-byte component,
// leading spaces and compatibility look-alikes stay admitted. The
// archived before probe under docs/experiments/probes/2026-09-12-
// download-name-limits* recorded the host's own refusal for a first
// component only and admission for a nested over-long component.
func TestDownloadNameLimits(t *testing.T) {
	long255 := strings.Repeat("b", 255)
	long256 := strings.Repeat("c", 256)
	long300 := strings.Repeat("a", 300)
	long400 := strings.Repeat("é", 200) // 400 bytes, 200 UTF-16 units
	for _, c := range []struct {
		name     string
		objects  []string
		items    int
		problems int
		detail   string
	}{
		{"component_255_bytes", []string{"scope/" + long255}, 1, 0, ""},
		{"component_256_bytes", []string{"scope/" + long256}, 0, 1, "path component of 256 bytes exceeds the 255-byte name limit"},
		{"component_300_bytes", []string{"scope/" + long300}, 0, 1, "path component of 300 bytes exceeds the 255-byte name limit"},
		{"component_400_bytes_200_units", []string{"scope/" + long400}, 0, 1, "path component of 400 bytes exceeds the 255-byte name limit"},
		{"nested_component_300_bytes", []string{"scope/dir/" + long300 + "/leaf.txt"}, 0, 1, "path component of 300 bytes exceeds the 255-byte name limit"},
		{"marker_component_300_bytes", []string{"scope/" + long300 + "/"}, 0, 1, "path component of 300 bytes exceeds the 255-byte name limit"},
		{"long_component_beside_safe", []string{"scope/safe.txt", "scope/" + long300}, 1, 1, "path component of 300 bytes exceeds the 255-byte name limit"},
		{"leading_space", []string{"scope/ a.txt", "scope/a.txt"}, 2, 0, ""},
		{"leading_space_device", []string{"scope/ CON"}, 1, 0, ""},
		{"compatibility_ligature", []string{"scope/ﬁle.txt", "scope/file.txt"}, 2, 0, ""},
		{"compatibility_fullwidth_letter", []string{"scope/Ａ.txt", "scope/A.txt"}, 2, 0, ""},
		{"compatibility_superscript", []string{"scope/x².txt", "scope/x2.txt"}, 2, 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			source, mock := session(t)
			for _, name := range c.objects {
				seed(mock, "media", name, "bytes for "+name)
			}
			root := t.TempDir()
			plan, err := PreflightDownload(t.Context(), source, "media", "scope/", root)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Items) != c.items || len(plan.Problems) != c.problems {
				t.Fatalf("admission differs: %d items %+v", len(plan.Items), plan.Problems)
			}
			if c.problems > 0 {
				problem := plan.Problems[0]
				if problem.Kind != "name-length" || !problem.Reject || !strings.Contains(problem.Detail, c.detail) || !strings.HasSuffix(problem.Detail, "refused on every platform") {
					t.Fatalf("refusal is not the named length rule: %+v", problem)
				}
			}
			if entries := listDir(t, root); len(entries) != 0 {
				t.Fatalf("preflight changed the destination: %v", entries)
			}
		})
	}
}

// The executor's item admission repeats the rule, so a plan that smuggles
// an over-long component past preflight is refused before any effect.
func TestDownloadNameLimitsExecutorAdmission(t *testing.T) {
	long300 := strings.Repeat("a", 300)
	for _, c := range []struct {
		name  string
		item  DownloadItem
		valid bool
	}{
		{"over_long_file", DownloadItem{Name: "scope/" + long300, RelPath: long300, Size: 1, Route: "rclone"}, false},
		{"over_long_nested", DownloadItem{Name: "scope/dir/" + long300 + "/leaf.txt", RelPath: "dir/" + long300 + "/leaf.txt", Size: 1, Route: "rclone"}, false},
		{"over_long_marker", DownloadItem{Name: "scope/" + long300 + "/", RelPath: long300, Dir: true}, false},
		{"limit_file", DownloadItem{Name: "scope/" + strings.Repeat("b", 255), RelPath: strings.Repeat("b", 255), Size: 1, Route: "rclone"}, true},
		{"leading_space", DownloadItem{Name: "scope/ a.txt", RelPath: " a.txt", Size: 1, Route: "rclone"}, true},
		{"ligature", DownloadItem{Name: "scope/ﬁle.txt", RelPath: "ﬁle.txt", Size: 1, Route: "rclone"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := validDownloadItem("scope/", c.item); got != c.valid {
				t.Fatalf("validDownloadItem = %v, want %v", got, c.valid)
			}
		})
	}
	// Through the executor itself: nothing is written and the engine is
	// never invoked for a plan carrying an over-long component.
	source, mock := session(t)
	seed(mock, "media", "scope/"+long300, "bytes")
	root := t.TempDir()
	engine := &countedEngine{}
	plan := &DownloadPreflight{Items: []DownloadItem{{Name: "scope/" + long300, RelPath: long300, Size: 5, Route: "rclone"}}, Objects: 1, TotalBytes: 5}
	report, err := Download(t.Context(), source, engine, "aeronautes_src:media", "media", "scope/", root, "name-limits", plan, nil)
	if err == nil || report != nil || engine.calls != 0 {
		t.Fatalf("executor admitted an over-long component: %+v %v calls=%d", report, err, engine.calls)
	}
	if entries := listDir(t, root); len(entries) != 0 {
		t.Fatalf("refused plan changed the destination: %v", entries)
	}
}

type countedEngine struct{ calls int }

func (e *countedEngine) Copy(context.Context, string, string, []string, func(rclone.Event)) (*rclone.Result, error) {
	e.calls++
	return &rclone.Result{}, nil
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
