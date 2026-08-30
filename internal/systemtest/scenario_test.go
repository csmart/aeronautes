package systemtest

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
)

// The scenario registry ties action/scenario COVERAGE to actual test
// EXECUTION (codex war-room F-04). A test that exercises a bridge action
// in a required scenario calls RecordScenario; nothing else grants
// credit. The coverage report is therefore evidence of what ran in this
// invocation, not a hand-maintained claim that could name a scenario no
// test exercises.
//
// Known limitation, stated plainly: this proves a test RAN and asserted
// its own body for that (action, scenario) — it does not by itself prove
// the assertion is strong enough. That is what the oracle's negative
// space and the per-test assertions are for; a fully machine-checked
// "this test proves scenario X" contract is future work.
var (
	scenarioMu   sync.Mutex
	scenarioSeen = map[string]map[string]string{} // action -> scenario -> t.Name()
)

// RecordScenario marks (action, scenario) as exercised by the calling
// test. The action must be inventoried and the scenario one it requires,
// so a typo or an obsolete claim fails loudly (immediately) rather than
// inflating coverage.
//
// Crediting is deferred to test cleanup and skipped if the test FAILED,
// so coverage means "a PASSING test exercised this", not merely
// "RecordScenario ran" — a test whose later assertion (e.g. a closing
// AssertNoCollateral) fails does not leave a credited scenario behind in
// the uploaded report (grok war-room 3 P2).
func RecordScenario(t *testing.T, action, scenario string) {
	t.Helper()
	a, ok := Inventory[action]
	if !ok {
		t.Fatalf("RecordScenario: %q is not an inventoried action", action)
	}
	if !scenarioRequired(a, scenario) {
		t.Fatalf("RecordScenario: %q is not a required scenario for %q (needs %v)", scenario, action, a.required())
	}
	name := t.Name()
	t.Cleanup(func() { creditScenario(action, scenario, name, t.Failed()) })
}

// creditScenario commits a (action, scenario) credit unless the test
// failed. Extracted from the cleanup closure so the credit-only-on-pass
// rule is unit-testable without a real failing test.
func creditScenario(action, scenario, testName string, failed bool) {
	if failed {
		return
	}
	scenarioMu.Lock()
	defer scenarioMu.Unlock()
	if scenarioSeen[action] == nil {
		scenarioSeen[action] = map[string]string{}
	}
	scenarioSeen[action][scenario] = testName
}

// TestScenarioCreditSkipsFailedTests proves coverage is credited only for
// PASSING tests (grok war-room 3 P2). A sentinel action (absent from the
// Inventory, so it never pollutes the report) exercises both outcomes.
func TestScenarioCreditSkipsFailedTests(t *testing.T) {
	const fake = "SentinelService.Only"
	defer func() { scenarioMu.Lock(); delete(scenarioSeen, fake); scenarioMu.Unlock() }()

	creditScenario(fake, "happy", "TestFake", true) // test FAILED → no credit
	if _, ok := recordedScenarios()[fake]; ok {
		t.Fatal("a failed test credited its scenario")
	}
	creditScenario(fake, "happy", "TestFake", false) // test PASSED → credit
	if r := recordedScenarios()[fake]; len(r) != 1 || r[0] != "happy" {
		t.Fatalf("a passing test was not credited: %v", r)
	}
}

func scenarioRequired(a Action, scenario string) bool {
	for _, r := range a.required() {
		if r == scenario {
			return true
		}
	}
	return false
}

// recordedScenarios snapshots the registry as action -> sorted scenarios.
func recordedScenarios() map[string][]string {
	scenarioMu.Lock()
	defer scenarioMu.Unlock()
	out := map[string][]string{}
	for action, scns := range scenarioSeen {
		var list []string
		for s := range scns {
			list = append(list, s)
		}
		sort.Strings(list)
		out[action] = list
	}
	return out
}

// coverageGaps returns, per action, the required scenarios not recorded.
func coverageGaps(recorded map[string][]string) map[string][]string {
	gaps := map[string][]string{}
	for key, action := range Inventory {
		have := map[string]bool{}
		for _, s := range recorded[key] {
			have[s] = true
		}
		var missing []string
		for _, req := range action.required() {
			if !have[req] {
				missing = append(missing, req)
			}
		}
		if len(missing) > 0 {
			gaps[key] = missing
		}
	}
	return gaps
}

// actionCoverage is one row of the machine-readable scenario report.
type actionCoverage struct {
	Action   string   `json:"action"`
	Kind     string   `json:"kind"`
	Required []string `json:"required"`
	Covered  []string `json:"covered"`
	Gaps     []string `json:"gaps"`
}

// scenarioReport is the machine-readable coverage artifact (codex F-05).
type scenarioReport struct {
	TotalRequired   int              `json:"totalRequired"`
	TotalCovered    int              `json:"totalCovered"`
	ActionsWithGaps int              `json:"actionsWithGaps"`
	SAIO            bool             `json:"saio"`
	Actions         []actionCoverage `json:"actions"`
}

func buildScenarioReport() scenarioReport {
	recorded := recordedScenarios()
	gaps := coverageGaps(recorded)
	rep := scenarioReport{SAIO: os.Getenv("AERONAUTES_SAIO_AUTH_URL") != ""}
	var keys []string
	for k := range Inventory {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := Inventory[k]
		req := a.required()
		rep.TotalRequired += len(req)
		rep.TotalCovered += len(recorded[k])
		if len(gaps[k]) > 0 {
			rep.ActionsWithGaps++
		}
		rep.Actions = append(rep.Actions, actionCoverage{
			Action: k, Kind: string(a.Kind), Required: req,
			Covered: recorded[k], Gaps: gaps[k],
		})
	}
	return rep
}

// TestMain runs the suite, then emits the scenario-coverage artifact from
// what actually executed. Under SAIO the flagship matrices record their
// scenarios; without SAIO they skip and the report honestly shows zero
// system coverage. The report is written to AERONAUTES_SCENARIO_REPORT
// when set (CI uploads it) so it never dirties the working tree.
func TestMain(m *testing.M) {
	code := m.Run()
	rep := buildScenarioReport()
	fmt.Printf("\nSCENARIO COVERAGE (evidence-based): %d/%d required scenarios exercised; %d/%d actions still have gaps; SAIO=%t\n",
		rep.TotalCovered, rep.TotalRequired, rep.ActionsWithGaps, len(rep.Actions), rep.SAIO)
	if path := os.Getenv("AERONAUTES_SCENARIO_REPORT"); path != "" {
		if data, err := json.MarshalIndent(rep, "", "  "); err == nil {
			if err := os.WriteFile(path, data, 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "scenario report write %s: %v\n", path, err)
			}
		}
	}
	os.Exit(code)
}
