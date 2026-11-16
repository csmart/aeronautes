package local

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

func TestUploadRejectsChangedSourcePath(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "item", "selected bytes")
	if err := os.Mkdir(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	for name, captured := range map[string]string{
		"relative":        relative,
		"dot":             dir + sep + "." + sep + "item",
		"parent":          dir + sep + "child" + sep + ".." + sep + "item",
		"empty_component": dir + sep + sep + "item",
		"invalid_utf8":    dir + sep + string([]byte{'b', 0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			s, mock := session(t)
			seed(mock, "media", "unselected", "retained bytes")
			witness := uploadWitness(t, mock)
			before := uploadSnapshot(t, witness)
			report, err := PreflightUpload(t.Context(), s, "media", "scope/", []string{captured})
			if err != nil || !report.Rejected() || len(report.Items) != 0 || len(report.Problems) != 1 {
				t.Fatalf("source path was not refused completely: %+v %v", report, err)
			}
			problem := report.Problems[0]
			if problem.Name != "selection 1" || problem.Kind != "source-path" || !problem.Reject ||
				problem.Detail != uploadPathRefusal || !utf8.ValidString(problem.Name) || !utf8.ValidString(problem.Detail) {
				t.Fatalf("source path refusal must be a safe selection label: %+v", problem)
			}
			encoded, err := json.Marshal(report)
			if err != nil || strings.Contains(string(encoded), `\ufffd`) || strings.ContainsRune(string(encoded), '\ufffd') {
				t.Fatalf("source path refusal crossed JSON lossily: %s %v", encoded, err)
			}
			engine := &countedUploadEngine{inner: &fsEngine{s}}
			result, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", report, nil, nil)
			if err == nil || result != nil || engine.calls != 0 {
				t.Errorf("rejected preflight executed: %+v %v calls=%d", result, err, engine.calls)
			}
			// A valid earlier item must not run before a later invalid path
			// is found in an internally supplied preflight.
			bypass := &UploadPreflight{Items: []UploadItem{
				{Path: path, Name: "scope/valid", Size: 14, Route: "native"},
				{Path: captured, Name: "scope/invalid", Size: 14, Route: "rclone"},
			}}
			result, err = Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", bypass, nil, nil)
			if err == nil || result != nil || engine.calls != 0 || err.Error() != uploadPathRefusal {
				t.Errorf("execution bypass admitted a source: %+v %v calls=%d", result, err, engine.calls)
			}
			if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
				t.Errorf("source path refusal changed owned storage: %s", diff)
			}
			if body, err := os.ReadFile(path); err != nil || string(body) != "selected bytes" {
				t.Errorf("source path refusal changed local bytes: %q %v", body, err)
			}
		})
	}
}

func TestUploadPreservesExactCanonicalSource(t *testing.T) {
	for _, prefix := range []string{"scope/", "native\n/"} {
		t.Run(prefix, func(t *testing.T) {
			s, mock := session(t)
			body := "valid source bytes"
			path := writeFile(t, t.TempDir(), "valid\ufffd-é", body)
			preflight, err := PreflightUpload(t.Context(), s, "media", prefix, []string{path})
			if err != nil || preflight.Rejected() || len(preflight.Items) != 1 || preflight.Items[0].Path != path {
				t.Fatalf("canonical source identity changed: %+v %v", preflight, err)
			}
			encoded, err := json.Marshal(preflight)
			if err != nil {
				t.Fatal(err)
			}
			var decoded UploadPreflight
			if err := json.Unmarshal(encoded, &decoded); err != nil || len(decoded.Items) != 1 || decoded.Items[0].Path != path {
				t.Fatalf("valid replacement character changed through JSON: %+v %v", decoded, err)
			}
			wantRoute := "rclone"
			if prefix == "native\n/" {
				wantRoute = "native"
			}
			if preflight.Items[0].Route != wantRoute {
				t.Fatalf("canonical source changed route: %+v", preflight)
			}
			if wantRoute != "native" {
				return
			}
			engine := &countedUploadEngine{inner: &fsEngine{s}}
			report, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", preflight, nil, nil)
			if err != nil || report.Done != 1 || report.Bytes != int64(len(body)) || len(report.Failures) != 0 || engine.calls != 0 {
				t.Fatalf("canonical native upload failed: %+v %v calls=%d", report, err, engine.calls)
			}
			witness := uploadSnapshot(t, uploadWitness(t, mock))
			object, found := witness.Containers["media"].Objects[prefix+filepath.Base(path)]
			if !found || object.Size != int64(len(body)) || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte(body))) {
				t.Errorf("native upload changed canonical bytes: %+v", object)
			}
		})
	}
}
