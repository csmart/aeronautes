package systemtest

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"testing"
)

// scenarioReportSchema is bumped when the report shape changes, so a
// consumer can reject an unexpected version (codex R-03 provenance).
const scenarioReportSchema = 1

// recordedScenarios snapshots the registry as action -> scenario -> test.
func recordedScenarios() map[string]map[string]string {
	scenarioMu.Lock()
	defer scenarioMu.Unlock()
	out := map[string]map[string]string{}
	for action, scns := range scenarioSeen {
		m := map[string]string{}
		for s, name := range scns {
			m[s] = name
		}
		out[action] = m
	}
	return out
}

// coverageGaps returns, per action, the required scenarios not recorded.
func coverageGaps(recorded map[string]map[string]string) map[string][]string {
	gaps := map[string][]string{}
	for key, action := range Inventory {
		have := recorded[key]
		var missing []string
		for _, req := range action.required() {
			if _, ok := have[req]; !ok {
				missing = append(missing, req)
			}
		}
		if len(missing) > 0 {
			gaps[key] = missing
		}
	}
	return gaps
}

// scenarioCredit is one covered cell WITH the test that earned it, so a
// reviewer can trace a covered cell back to its test (codex R-03).
type scenarioCredit struct {
	Scenario string `json:"scenario"`
	Test     string `json:"test"`
}

type actionCoverage struct {
	Action   string           `json:"action"`
	Kind     string           `json:"kind"`
	Required []string         `json:"required"`
	Covered  []scenarioCredit `json:"covered"`
	Gaps     []string         `json:"gaps"`
}

// scenarioReport is the machine-readable coverage artifact (codex F-05/R-03).
type scenarioReport struct {
	SchemaVersion   int              `json:"schemaVersion"`
	Valid           bool             `json:"valid"` // the test PROCESS passed
	CommitSHA       string           `json:"commitSha"`
	GoVersion       string           `json:"goVersion"`
	SAIO            bool             `json:"saio"`
	TotalRequired   int              `json:"totalRequired"`
	TotalCovered    int              `json:"totalCovered"`
	ActionsWithGaps int              `json:"actionsWithGaps"`
	Actions         []actionCoverage `json:"actions"`
}

func buildScenarioReport(valid bool) scenarioReport {
	recorded := recordedScenarios()
	gaps := coverageGaps(recorded)
	rep := scenarioReport{
		SchemaVersion: scenarioReportSchema,
		Valid:         valid,
		CommitSHA:     os.Getenv("GITHUB_SHA"),
		GoVersion:     runtime.Version(),
		SAIO:          os.Getenv("AERONAUTES_SAIO_AUTH_URL") != "",
	}
	var keys []string
	for k := range Inventory {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := Inventory[k]
		req := a.required()
		rep.TotalRequired += len(req)
		var covered []scenarioCredit
		for s, name := range recorded[k] {
			covered = append(covered, scenarioCredit{Scenario: s, Test: name})
		}
		sort.Slice(covered, func(i, j int) bool { return covered[i].Scenario < covered[j].Scenario })
		rep.TotalCovered += len(covered)
		if len(gaps[k]) > 0 {
			rep.ActionsWithGaps++
		}
		rep.Actions = append(rep.Actions, actionCoverage{
			Action: k, Kind: string(a.Kind), Required: req, Covered: covered, Gaps: gaps[k],
		})
	}
	return rep
}

// TestMain runs the suite, then emits the scenario-coverage artifact from
// what actually executed. Under SAIO the flagship matrices record their
// scenarios; without SAIO they skip and the report honestly shows zero
// system coverage. When AERONAUTES_SCENARIO_REPORT is set the report is
// written there and a marshal/write failure FAILS the process — a
// configured artifact must not silently go missing (codex R-03). The
// report carries a `valid` bit tied to the process result, so a failing
// run's numbers are never mistaken for passing evidence.
func TestMain(m *testing.M) {
	code := m.Run()
	rep := buildScenarioReport(code == 0)
	fmt.Printf("\nSCENARIO COVERAGE (evidence-based): %d/%d required scenarios exercised; %d/%d actions still have gaps; SAIO=%t valid=%t\n",
		rep.TotalCovered, rep.TotalRequired, rep.ActionsWithGaps, len(rep.Actions), rep.SAIO, rep.Valid)
	if path := os.Getenv("AERONAUTES_SCENARIO_REPORT"); path != "" {
		data, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "scenario report marshal failed: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "scenario report write %s failed: %v\n", path, err)
			os.Exit(1)
		}
	}
	os.Exit(code)
}

// TestScenarioCreditLifecycle exercises the REAL testing.T lifecycle (not
// a synthetic bool) through subtests: a passing test's credit lands, a
// SKIPPED test's does not, and the finalizer runs AFTER a resource cleanup
// registered later — so a cleanup that fails is observed before credit
// (codex R-01). The fail branch shares the exact `Failed() || Skipped()`
// guard proven by the skip branch; a failing subtest would fail this
// parent, which Go gives no clean way to isolate.
func TestScenarioCreditLifecycle(t *testing.T) {
	const fake = "SentinelService.Only" // absent from Inventory → never pollutes the report
	clear := func() { scenarioMu.Lock(); delete(scenarioSeen, fake); scenarioMu.Unlock() }
	defer clear()

	// A passing test commits its pending credit.
	t.Run("pass-commits", func(st *testing.T) {
		clear()
		h := &Harness{T: st, pending: []recordedCredit{{fake, "happy"}}}
		st.Cleanup(h.finalizeScenarios)
	})
	if _, ok := recordedScenarios()[fake]; !ok {
		t.Fatal("a passing test did not commit its pending credit")
	}

	// A skipped test commits nothing (a skip is not a pass).
	t.Run("skip-does-not-commit", func(st *testing.T) {
		clear()
		h := &Harness{T: st, pending: []recordedCredit{{fake, "happy"}}}
		st.Cleanup(h.finalizeScenarios)
		st.Skip("deliberate skip")
	})
	if _, ok := recordedScenarios()[fake]; ok {
		t.Fatal("a skipped test committed a credit")
	}

	// The finalizer, registered FIRST like New does, runs LAST — after a
	// resource cleanup registered later. So it observes the post-cleanup
	// state (including any failure a cleanup raised) before crediting.
	clear()
	var order []string
	t.Run("finalizer-runs-after-resource-cleanup", func(st *testing.T) {
		h := &Harness{T: st, pending: []recordedCredit{{fake, "happy"}}}
		st.Cleanup(func() { order = append(order, "finalizer"); h.finalizeScenarios() }) // first → last
		st.Cleanup(func() { order = append(order, "resource") })                         // later → first
	})
	if len(order) != 2 || order[0] != "resource" || order[1] != "finalizer" {
		t.Fatalf("finalizer did not run after the resource cleanup: %v", order)
	}
	if _, ok := recordedScenarios()[fake]; !ok {
		t.Fatal("passing test with a resource cleanup was not credited")
	}
}
