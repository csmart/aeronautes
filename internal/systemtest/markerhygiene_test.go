package systemtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This is a HYGIENE lint, not a security boundary. Under the cooperating-
// author model a determined test can forge a
// coverage claim — build the marker dynamically, or fabricate a Receipt —
// and this guard cannot stop that. What it does stop is the accidental or
// copy-pasted forge: a top-level system test that prints a claim marker
// directly instead of going through the Receipt API. It catches both the
// literal marker and the emission-helper identifiers, which is the exact
// shape of the no-op forge the review reproduced. cmd/scenariocov is out of
// scope: its parser unit tests legitimately construct marker fixtures.

// forbiddenEmissionTokens are the strings a top-level system test must not
// contain: emitting claims is the harness's job, on the non-test side.
var forbiddenEmissionTokens = []string{
	"AERONAUTES_SCENARIO_CLAIM ", // the raw claim marker literal
	"ClaimMarker",                // scenariocontract.ClaimMarker
	"claimJSON",                  // the unexported emission helper
}

// hygieneExempt files legitimately name the forbidden tokens: this lint
// itself (its token list and fixture) is the only one.
var hygieneExempt = map[string]bool{
	"markerhygiene_test.go": true,
}

func offendingTokens(content string) []string {
	var found []string
	for _, tok := range forbiddenEmissionTokens {
		if strings.Contains(content, tok) {
			found = append(found, tok)
		}
	}
	return found
}

// TestMarkerHygiene fails if any top-level system test source emits a
// coverage claim directly instead of through the Receipt API. It scans the
// package working directory (go test's cwd is the package's source dir),
// which — unlike a runtime.Caller path — survives the standard -trimpath
// build flag.
func TestMarkerHygiene(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, path := range matches {
		base := filepath.Base(path)
		if hygieneExempt[base] {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", base, err)
		}
		scanned++
		if bad := offendingTokens(string(data)); len(bad) > 0 {
			t.Errorf("%s emits a coverage claim directly (%s) — credit through the Receipt API instead", base, strings.Join(bad, ", "))
		}
	}
	if scanned == 0 {
		t.Fatal("marker-hygiene lint scanned no files — glob or path is wrong")
	}
}

// TestMarkerHygieneCatchesForge is the negative fixture: the same detector
// must flag a synthetic direct-emission forge, so a broken detector that
// silently passes everything is itself caught.
func TestMarkerHygieneCatchesForge(t *testing.T) {
	// A no-op forge in the shape the review reproduced: print the marker with a
	// hand-built claim, no application action invoked. Assembled here from
	// fragments so this fixture does not itself trip the file lint.
	forge := "t.Logf(\"%s%s\", scenario" + "ClaimMarker, claim" + "JSON(a, s, \"nothing ran\"))"
	if bad := offendingTokens(forge); len(bad) == 0 {
		t.Fatal("the hygiene detector failed to flag a direct-emission forge")
	}
}
