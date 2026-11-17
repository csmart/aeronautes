package local

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

const downloadRootTestRefusal = "the selected destination path cannot be used unchanged; select it again using an absolute UTF-8 path without dot or empty components"

func TestDownloadRejectsChangedDestination(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "selected")
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, dir)
	if err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	for name, captured := range map[string]string{
		"empty": "", "relative": relative,
		"dot":                dir + sep + ".",
		"parent":             dir + sep + "child" + sep + "..",
		"empty_component":    root + sep + sep + "selected",
		"trailing_separator": dir + sep,
		"invalid_utf8":       root + sep + string([]byte{'b', 0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			s, mock := session(t)
			seed(mock, "media", "scope/proof.bin", "selected bytes")
			for _, prefix := range []string{"scope/", "empty/"} {
				t.Run(prefix, func(t *testing.T) {
					report, err := PreflightDownload(t.Context(), s, "media", prefix, captured)
					if err != nil || report == nil || !report.Rejected() || len(report.Items) != 0 || len(report.Problems) != 1 || report.Objects != 0 || report.TotalBytes != 0 {
						t.Errorf("destination was not refused before planning: %+v %v", report, err)
						return
					}
					problem := report.Problems[0]
					if problem.Name != "destination" || problem.Kind != "destination-path" || !problem.Reject || problem.Detail != downloadRootTestRefusal || !utf8.ValidString(problem.Name+problem.Detail) {
						t.Errorf("unsafe destination refusal: %+v", problem)
					}
					encoded, err := json.Marshal(report)
					if err != nil || strings.Contains(string(encoded), root) || strings.ContainsRune(string(encoded), '\ufffd') || strings.Contains(string(encoded), `\ufffd`) {
						t.Errorf("destination refusal leaked or changed path bytes: %s %v", encoded, err)
					}
				})
			}
			// Even an empty internally supplied plan must not bypass the root
			// contract; no source or engine is needed before rejecting it.
			report, err := Download(t.Context(), nil, nil, "", "media", "", captured, &DownloadPreflight{}, nil)
			if report != nil || err == nil || err.Error() != downloadRootTestRefusal {
				t.Errorf("executor admitted a changed destination: %+v %v", report, err)
			}
		})
	}
}
