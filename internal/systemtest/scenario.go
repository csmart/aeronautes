package systemtest

import (
	"encoding/json"

	"github.com/csmart/aeronautes/internal/scenariocontract"
)

// Scenario claims are cooperating-test annotations, reconciled OUT OF
// PROCESS from the `go test -json` event stream by cmd/scenariocov. A test
// does not commit a credit to any in-process registry — it EMITS a claim
// line, and the tool credits that claim only if the emitting top-level test
// reached a single terminal PASS in the stream (claims come only from
// top-level tests). That terminal-event reconciliation is what prevents a
// skipped, failed, parallel-paused, or later-skipped test from leaving a
// credit behind, since the tool reads each test's definitive final
// disposition after the whole tree finishes.
//
// What this does NOT provide is authenticated provenance. The test process
// can bypass the harness API and print a claim marker directly, so the
// report is an honest-author progress signal, not independently verified
// evidence of application behavior. The harness Receipt (harness.go) reduces
// accidental action/result mix-ups for a cooperating author using the
// supported API; it is not a trust boundary against a test that sets out to
// forge one. The real trust domain is reviewed repository code plus CI.

// claimJSON renders one claim as JSON for emission. It lives on the non-test
// side of the harness so a claim is emitted through the Receipt API, not
// hand-assembled in a test; the marker-hygiene lint enforces that habit but
// does not (and cannot) make it a boundary.
func claimJSON(action, scenario, evidence string) string {
	b, _ := json.Marshal(scenariocontract.Claim{Action: action, Scenario: scenario, Evidence: evidence})
	return string(b)
}
