// Command scenariocov reconciles war-room scenario coverage from a
// `go test -json` event stream (codex war-room round-4 F-02). It reads the
// stream on stdin, extracts the inventory and the scenario-claim lines the
// systemtest package emits, and credits a claim ONLY if the emitting test
// and every ancestor reached a terminal PASS in the stream. Because it
// judges each test from the stream AFTER the whole tree has finished, a
// test that skipped, failed, paused at t.Parallel, or was skipped by a
// later parent cleanup cannot leave a credit behind — the exact spoofs an
// in-process collector could not see.
//
// Usage: go test -json [args] ./internal/systemtest/... | scenariocov -out report.json
//
// It exits non-zero if any test failed (so a failure still fails CI through
// the pipe) or if the stream contained no test events (a build failure).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
)

const (
	claimMarker     = "AERONAUTES_SCENARIO_CLAIM "
	inventoryMarker = "AERONAUTES_SCENARIO_INVENTORY "
	schemaVersion   = 2
)

type event struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

type inventoryAction struct {
	Kind     string   `json:"kind"`
	Required []string `json:"required"`
}

type claim struct {
	Action   string `json:"action"`
	Scenario string `json:"scenario"`
	Evidence string `json:"evidence"`
}

type emittedClaim struct {
	testKey string
	claim
}

type scenarioCredit struct {
	Scenario string   `json:"scenario"`
	Tests    []string `json:"tests"`
	Evidence []string `json:"evidence"`
}

type actionCoverage struct {
	Action   string           `json:"action"`
	Kind     string           `json:"kind"`
	Required []string         `json:"required"`
	Covered  []scenarioCredit `json:"covered"`
	Gaps     []string         `json:"gaps"`
}

type report struct {
	SchemaVersion   int              `json:"schemaVersion"`
	Valid           bool             `json:"valid"`
	CommitSHA       string           `json:"commitSha"`
	GoVersion       string           `json:"goVersion"`
	SAIO            bool             `json:"saio"`
	TotalRequired   int              `json:"totalRequired"`
	TotalCovered    int              `json:"totalCovered"`
	ActionsWithGaps int              `json:"actionsWithGaps"`
	Actions         []actionCoverage `json:"actions"`
}

func main() {
	out := flag.String("out", "", "write the JSON report to this path (also printed to stderr summary)")
	flag.Parse()

	rep, anyTest, anyFail, err := reconcile(os.Stdin, os.Getenv("GITHUB_SHA"), os.Getenv("AERONAUTES_SAIO_AUTH_URL") != "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenariocov: %v\n", err)
		os.Exit(2)
	}
	data, _ := json.MarshalIndent(rep, "", "  ")
	if *out != "" {
		if werr := os.WriteFile(*out, data, 0o644); werr != nil {
			fmt.Fprintf(os.Stderr, "scenariocov: write %s: %v\n", *out, werr)
			os.Exit(2)
		}
	}
	fmt.Fprintf(os.Stderr, "SCENARIO COVERAGE: %d/%d cells; %d/%d actions with gaps; valid=%t saio=%t\n",
		rep.TotalCovered, rep.TotalRequired, rep.ActionsWithGaps, len(rep.Actions), rep.Valid, rep.SAIO)

	if !anyTest {
		fmt.Fprintln(os.Stderr, "scenariocov: no test events in the stream (build failure?)")
		os.Exit(1)
	}
	if anyFail {
		os.Exit(1) // a test failed — fail CI through the pipe
	}
}

// reconcile parses a test2json stream and builds the coverage report,
// crediting only claims whose test and all ancestors terminally passed.
func reconcile(r interface{ Read([]byte) (int, error) }, commitSHA string, saio bool) (report, bool, bool, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)

	status := map[string]string{}                 // "pkg.Test" -> pass|fail|skip
	outputByTest := map[string]*strings.Builder{} // reassembled test output
	anyTest, anyFail := false, false

	for sc.Scan() {
		line := sc.Bytes()
		var e event
		if err := json.Unmarshal(line, &e); err != nil {
			continue // non-JSON line (e.g. a build error banner); ignored here
		}
		switch e.Action {
		case "pass", "fail", "skip":
			if e.Test != "" {
				status[e.Package+"\t"+e.Test] = e.Action
				anyTest = true
				if e.Action == "fail" {
					anyFail = true
				}
			} else if e.Action == "fail" {
				anyFail = true // a package failed to build/run
			}
		case "output":
			if e.Test == "" {
				continue
			}
			// test2json splits a single write into 1KB chunks, so a long
			// emitted line (the whole inventory) spans several events.
			// Reassemble each test's output, then scan complete lines below.
			key := e.Package + "\t" + e.Test
			b := outputByTest[key]
			if b == nil {
				b = &strings.Builder{}
				outputByTest[key] = b
			}
			b.WriteString(e.Output)
		}
	}
	if err := sc.Err(); err != nil {
		return report{}, anyTest, anyFail, err
	}

	// Scan the reassembled per-test output for the inventory and claims.
	var claims []emittedClaim
	var inv map[string]inventoryAction
	for key, b := range outputByTest {
		for _, ln := range strings.Split(b.String(), "\n") {
			if i := strings.Index(ln, inventoryMarker); i >= 0 {
				var m map[string]inventoryAction
				if err := json.Unmarshal([]byte(strings.TrimSpace(ln[i+len(inventoryMarker):])), &m); err == nil {
					inv = m
				}
			}
			if i := strings.Index(ln, claimMarker); i >= 0 {
				var c claim
				if err := json.Unmarshal([]byte(strings.TrimSpace(ln[i+len(claimMarker):])), &c); err == nil {
					claims = append(claims, emittedClaim{testKey: key, claim: c})
				}
			}
		}
	}
	if inv == nil {
		return report{}, anyTest, anyFail, fmt.Errorf("no inventory line found in the stream")
	}

	// Credit claims from tests (and all ancestors) that terminally passed.
	credited := map[string]map[string][]emittedClaim{} // action -> scenario -> claims
	for _, ec := range claims {
		if !allPassed(ec.testKey, status) {
			continue
		}
		if credited[ec.Action] == nil {
			credited[ec.Action] = map[string][]emittedClaim{}
		}
		credited[ec.Action][ec.Scenario] = append(credited[ec.Action][ec.Scenario], ec)
	}

	rep := report{
		SchemaVersion: schemaVersion,
		Valid:         anyTest && !anyFail,
		CommitSHA:     commitSHA,
		GoVersion:     runtime.Version(),
		SAIO:          saio,
	}
	var keys []string
	for k := range inv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, action := range keys {
		a := inv[action]
		rep.TotalRequired += len(a.Required)
		have := credited[action]
		var covered []scenarioCredit
		var gaps []string
		for _, scenario := range a.Required {
			ecs := have[scenario]
			if len(ecs) == 0 {
				gaps = append(gaps, scenario)
				continue
			}
			sortByTest(ecs)
			sc := scenarioCredit{Scenario: scenario}
			for _, ec := range ecs {
				sc.Tests = append(sc.Tests, ec.testKey)
				sc.Evidence = append(sc.Evidence, ec.Evidence)
			}
			covered = append(covered, sc)
			rep.TotalCovered++
		}
		if len(gaps) > 0 {
			rep.ActionsWithGaps++
		}
		rep.Actions = append(rep.Actions, actionCoverage{
			Action: action, Kind: a.Kind, Required: a.Required, Covered: covered, Gaps: gaps,
		})
	}
	return rep, anyTest, anyFail, nil
}

// allPassed reports whether the test and every ancestor terminally passed.
func allPassed(key string, status map[string]string) bool {
	pkg, test, ok := strings.Cut(key, "\t")
	if !ok {
		return status[key] == "pass"
	}
	parts := strings.Split(test, "/")
	for i := range parts {
		ancestor := pkg + "\t" + strings.Join(parts[:i+1], "/")
		if status[ancestor] != "pass" {
			return false
		}
	}
	return true
}

func sortByTest(ecs []emittedClaim) {
	sort.Slice(ecs, func(i, j int) bool { return ecs[i].testKey < ecs[j].testKey })
}
