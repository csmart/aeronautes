package systemtest

import "sync"

// The scenario registry ties action/scenario COVERAGE to actual test
// EXECUTION (codex war-room F-04/R-01). A credit is granted only when:
//   - the test invoked the bridge action through a harness wrapper (so the
//     invocation is OBSERVED, not self-attested — a no-op test that only
//     names an action cannot earn credit), AND
//   - the test finished without failing OR skipping, AND
//   - all of the test's resource cleanup ran first (the credit is
//     committed by a finalizer registered before those cleanups, so it
//     runs after them — see Harness.finalizeScenarios).
//
// Known limitation, stated plainly: this proves a passing test invoked the
// action and reached the record call after its assertions; it does not by
// itself prove the assertion is strong enough. The oracle's negative space
// and each test's assertions carry that; a fully machine-checked "this test
// proves scenario X to strength Y" contract is future work.

// recordedCredit is one pending (action, scenario) credit for a test,
// committed to the registry only if the test ultimately passes.
type recordedCredit struct{ action, scenario string }

var (
	scenarioMu   sync.Mutex
	scenarioSeen = map[string]map[string]string{} // action -> scenario -> t.Name()
)

// scenarioRequired reports whether scenario is one the action requires.
func scenarioRequired(a Action, scenario string) bool {
	for _, r := range a.required() {
		if r == scenario {
			return true
		}
	}
	return false
}

// creditScenario commits one (action, scenario) credit attributed to
// testName. The caller (a passing test's finalizer) has already decided
// the test earned it.
func creditScenario(action, scenario, testName string) {
	scenarioMu.Lock()
	defer scenarioMu.Unlock()
	if scenarioSeen[action] == nil {
		scenarioSeen[action] = map[string]string{}
	}
	scenarioSeen[action][scenario] = testName
}
