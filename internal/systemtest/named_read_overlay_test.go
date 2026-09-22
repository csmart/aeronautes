package systemtest

// The named-read session proof (ADR 0066) has to pause scopedSession
// between validating the caller's entry and returning its session, then
// complete a public Connect before the read continues. Production carries
// no callback for that, so the scheduled test runs in a child `go test`
// whose build overlays a copy of connections.go with one observation call
// after the generation comparison, plus a package-app file defining that
// call. The parent test builds the overlay, runs the child, and reports its
// result; the child, marked by an environment variable, runs the scheduled
// cases directly. A missing or ambiguous insertion point, a compilation
// failure, a missing child result or an unreached barrier is a failed run,
// never a pass.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// namedReadCheckpointKey is the context key the overlaid production code
// consults; the key is a plain string so the test package and the overlay
// file in package app agree without a shared exported type.
const namedReadCheckpointKey = "aeronautes.test.namedReadCheckpoint"

const namedReadChildEnv = "AERONAUTES_NAMED_READ_CHILD"

// namedReadCheckpointSource is the package-app file the overlay adds.
const namedReadCheckpointSource = `package app

import "context"

// namedReadCheckpoint exists only in the test overlay built by
// internal/systemtest/named_read_overlay_test.go. It lets the ADR 0066
// proof pause a named read after its entry has been validated.
func namedReadCheckpoint(ctx context.Context) {
	if pause, ok := ctx.Value("` + namedReadCheckpointKey + `").(func()); ok {
		pause()
	}
}
`

// namedReadAnchor is the exact production text the observation call
// follows: the generation comparison inside scopedSession. Source drift
// that moves or duplicates it fails the instrumentation rather than
// silently proving nothing.
const namedReadAnchor = "\tif entry.generation != connectedGeneration {\n\t\treturn nil, ListingScopeDTO{}, ErrAccountScopeChanged\n\t}\n"

// namedReadRunsInChild returns false when this process is the instrumented
// child, in which case the caller runs the scheduled cases directly.
// Otherwise it builds the overlay, runs the child for the named test, and
// returns true after reporting the child's result on t.
func namedReadRunsInChild(t *testing.T, testName string) bool {
	t.Helper()
	if os.Getenv(namedReadChildEnv) == "1" {
		return false
	}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("instrumented child needs the go tool: %v", err)
	}
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(root, "internal", "app", "connections.go"))
	if err != nil {
		t.Fatal(err)
	}
	instrumented, err := namedReadInstrument(string(source))
	if err != nil {
		t.Fatalf("instrumentation refused: %v", err)
	}
	dir := t.TempDir()
	connections := filepath.Join(dir, "connections.go")
	checkpoint := filepath.Join(dir, "zz_named_read_checkpoint_overlay.go")
	if err := os.WriteFile(connections, []byte(instrumented), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkpoint, []byte(namedReadCheckpointSource), 0o600); err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": {
		filepath.Join(root, "internal", "app", "connections.go"):                      connections,
		filepath.Join(root, "internal", "app", "zz_named_read_checkpoint_overlay.go"): checkpoint,
	}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBinary, "test", "-race", "-count=1", "-json",
		"-overlay="+overlayPath, "-run", "^"+testName+"$", "./internal/systemtest")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), namedReadChildEnv+"=1")
	out, runErr := cmd.CombinedOutput()
	result, logs := namedReadChildResult(out, testName)
	switch result {
	case "pass":
		t.Logf("instrumented child passed %s:\n%s", testName, logs)
		return true
	case "skip":
		t.Skipf("instrumented child skipped %s:\n%s", testName, logs)
	case "fail":
		t.Fatalf("instrumented child failed %s:\n%s", testName, logs)
	}
	t.Fatalf("instrumented child produced no result for %s (%v):\n%s", testName, runErr, string(out))
	return true
}

// namedReadInstrument inserts the observation call after the unique
// generation comparison of scopedSession and refuses anything else.
func namedReadInstrument(source string) (string, error) {
	start := strings.Index(source, "func (c *Connections) scopedSession(")
	if start < 0 {
		return "", errors.New("scopedSession not found")
	}
	body := source[start:]
	end := strings.Index(body, "\n}\n")
	if end < 0 {
		return "", errors.New("scopedSession end not found")
	}
	body = body[:end+3]
	if strings.Count(body, namedReadAnchor) != 1 {
		return "", errors.New("generation comparison anchor is missing or ambiguous in scopedSession")
	}
	if strings.Count(source, namedReadAnchor) != 1 {
		return "", errors.New("generation comparison anchor also appears outside scopedSession")
	}
	return strings.Replace(source, namedReadAnchor, namedReadAnchor+"\tnamedReadCheckpoint(ctx)\n", 1), nil
}

// namedReadChildResult reads the child's -json stream for the named test's
// terminal action and returns it with the child's captured output lines.
func namedReadChildResult(out []byte, testName string) (string, string) {
	result := ""
	var logs strings.Builder
	for _, line := range bytes.Split(out, []byte("\n")) {
		var event struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
			Output string `json:"Output"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			if len(bytes.TrimSpace(line)) > 0 {
				logs.Write(line)
				logs.WriteByte('\n')
			}
			continue
		}
		if event.Action == "output" {
			logs.WriteString(event.Output)
		}
		if event.Test == testName && (event.Action == "pass" || event.Action == "fail" || event.Action == "skip") {
			result = event.Action
		}
	}
	return result, logs.String()
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("module root not found above the test directory")
		}
		dir = parent
	}
}
