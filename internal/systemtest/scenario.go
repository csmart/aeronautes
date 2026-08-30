package systemtest

import "encoding/json"

// Scenario coverage is reconciled OUT OF PROCESS from the `go test -json`
// event stream (codex war-room round-4 F-01/F-02). A test does not commit
// a credit to any in-process registry — it EMITS a claim line, and the
// cmd/scenariocov tool credits that claim only if the emitting test, and
// every ancestor, reached a terminal PASS in the event stream. So a
// skipped, failed, parallel-paused, or later-skipped test can never leave
// a credit behind: the tool sees each test's DEFINITIVE final disposition
// after the whole tree finishes, which no in-process cleanup can observe.
//
// A claim is emitted only through a single-use invocation Receipt returned
// by a harness wrapper (see harness.go). The receipt carries the actual
// action, job id, error, and outcome from that one call, and its Credit*
// methods validate the scenario against THAT evidence — so one invocation
// cannot certify a scenario it did not produce, and evidence from call B
// cannot certify call A.

// These markers tag the two machine-readable lines the tool scans for in
// test output. They are deliberately unlikely to collide with real output.
const (
	scenarioClaimMarker     = "AERONAUTES_SCENARIO_CLAIM "
	scenarioInventoryMarker = "AERONAUTES_SCENARIO_INVENTORY "
)

// claimLine is one emitted (action, scenario) claim with its evidence.
type claimLine struct {
	Action   string `json:"action"`
	Scenario string `json:"scenario"`
	Evidence string `json:"evidence"`
}

// inventoryAction is one action's contract, emitted so the tool can build
// the denominator without importing this test package.
type inventoryAction struct {
	Kind     string   `json:"kind"`
	Required []string `json:"required"`
}

func scenarioRequired(a Action, scenario string) bool {
	for _, r := range a.required() {
		if r == scenario {
			return true
		}
	}
	return false
}

// inventoryJSON renders the whole inventory contract as JSON for emission.
func inventoryJSON() string {
	m := map[string]inventoryAction{}
	for key, a := range Inventory {
		m[key] = inventoryAction{Kind: string(a.Kind), Required: a.required()}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// claimJSON renders one claim as JSON for emission.
func claimJSON(action, scenario, evidence string) string {
	b, _ := json.Marshal(claimLine{Action: action, Scenario: scenario, Evidence: evidence})
	return string(b)
}
