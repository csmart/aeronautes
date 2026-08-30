package systemtest

import "sync"

// The scenario registry ties action/scenario COVERAGE to actual test
// EXECUTION and OUTCOME (codex war-room R-01/F-01..F-03). A credit is
// granted only when ALL of these hold:
//   - the test invoked the bridge action through a harness wrapper, so the
//     invocation is OBSERVED (a no-op test that only names an action earns
//     nothing);
//   - the scenario matches OBJECTIVE OUTCOME EVIDENCE — credit comes from
//     scenario-specific helpers (CreditHappyJob requires a COMPLETE job,
//     CreditRefusal a returned error, CreditConflict a "conflict" outcome),
//     never a free-form label, so one refusal cannot certify six scenarios;
//   - the test finished without failing OR skipping, judged by RunScenario
//     from OUTSIDE the subtest after its entire lifecycle (all cleanups)
//     has completed, so a late skip or a cleanup failure blocks credit;
//   - the ledger is mutex-guarded, safe under the concurrency the war room
//     exists to drive.
//
// Known limitation, stated plainly: a credit proves a passing test invoked
// the action and observed the coarse outcome the helper checks; it does not
// prove the test's negative-space and delta assertions are exhaustive. The
// oracle and each test carry that; a richer per-invocation evidence ledger
// (request/variant/oracle identity) is future work.

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
