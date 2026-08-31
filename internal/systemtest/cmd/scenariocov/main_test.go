package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/scenariocontract"
)

// This package is the reconciler's own unit tests, so it legitimately
// constructs marker fixtures (the systemtest marker-hygiene lint exempts
// cmd/scenariocov). These regressions retain the exact attacks from the
// war-room review rounds.

func ev(action, test, output string) string {
	e := event{Action: action, Package: "p", Test: test, Output: output}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

func pkgEvent(action string) string {
	e := event{Action: action, Package: "p"}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

// out emits a test output event whose payload ends in a newline, matching
// how test2json presents a t.Logf line (the reconciler only scans complete,
// newline-terminated lines).
func out(test, payload string) string { return ev("output", test, payload+"\n") }

func claimPayload(action, scenario, evidence string) string {
	b, _ := json.Marshal(scenariocontract.Claim{Action: action, Scenario: scenario, Evidence: evidence})
	return string(b)
}

func claimLine(test, action, scenario string) string {
	return out(test, scenariocontract.ClaimMarker+claimPayload(action, scenario, "ev "+test))
}

func defaultOpts() options { return options{commitSHA: "sha", saioConfigured: true, testExit: 0} }

func run(t *testing.T, stream string, opts options) report {
	t.Helper()
	rep, err := reconcile(strings.NewReader(stream), opts)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return rep
}

func coveredScenarios(rep report, action string) map[string]bool {
	out := map[string]bool{}
	for _, a := range rep.Actions {
		if a.Action == action {
			for _, c := range a.Covered {
				out[c.Scenario] = true
			}
		}
	}
	return out
}

func contractTotals() (actions, required int) {
	c := scenariocontract.All()
	actions = len(c)
	for _, a := range c {
		required += len(a.Required())
	}
	return
}

// TestOnlyPassingTestsAreCredited retains the skipped/failed/parallel/cleanup
// attacks: each reduces to a test whose terminal disposition is not pass, and
// only a passing test's claim is credited.
func TestOnlyPassingTestsAreCredited(t *testing.T) {
	const act = "MutateService.UpdateObjectMetadata" // requires happy, refusal, conflict
	stream := claimLine("TestHappy", act, "happy") + ev("pass", "TestHappy", "") +
		claimLine("TestSkipped", act, "refusal") + ev("skip", "TestSkipped", "") +
		claimLine("TestFailed", act, "conflict") + ev("fail", "TestFailed", "")

	// A failed test means the producer exited nonzero in CI.
	rep := run(t, stream, options{commitSHA: "sha", saioConfigured: true, testExit: 1})
	got := coveredScenarios(rep, act)
	if !got["happy"] {
		t.Error("a passing test's claim was not credited")
	}
	if got["refusal"] {
		t.Error("a SKIPPED test's claim was credited")
	}
	if got["conflict"] {
		t.Error("a FAILED test's claim was credited")
	}
	if rep.Valid {
		t.Error("report marked valid despite the producer exiting nonzero")
	}
}

// TestAncestorMustPass: a claim from a passing subtest whose PARENT failed is
// not credited.
func TestAncestorMustPass(t *testing.T) {
	stream := claimLine("TestParent/child", "AppService.About", "happy") +
		ev("pass", "TestParent/child", "") +
		ev("fail", "TestParent", "")
	if coveredScenarios(run(t, stream, options{testExit: 1}), "AppService.About")["happy"] {
		t.Error("a claim was credited despite its parent failing")
	}
}

// TestSlashSubtestThroughGoTestJSON is the F-04 regression through REAL
// go test -json: a subtest named "a/b" has no "a" node, and its claim must
// still be credited. Synthetic events cannot prove the real hierarchy.
func TestSlashSubtestThroughGoTestJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skips the go test subprocess in -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	cmd := exec.Command("go", "test", "-json", "-v", "-count=1", "-run", "TestSlash", "./testdata/slashsub")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("fixture subprocess failed: %v\n%s", err, out)
	}
	rep, rerr := reconcile(bytes.NewReader(out), defaultOpts())
	if rerr != nil {
		t.Fatalf("reconcile of real stream: %v", rerr)
	}
	if !coveredScenarios(rep, "AppService.About")["happy"] {
		t.Fatalf("slash-named subtest claim was not credited through real go test -json\n%s", out)
	}
}

// TestGapsAndTotals checks the denominator (from the contract) and numerator.
func TestGapsAndTotals(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "")
	rep := run(t, stream, defaultOpts())
	actions, required := contractTotals()
	if rep.TotalActions != actions {
		t.Errorf("TotalActions=%d want %d", rep.TotalActions, actions)
	}
	if rep.TotalRequired != required {
		t.Errorf("TotalRequired=%d want %d", rep.TotalRequired, required)
	}
	if rep.TotalCovered != 1 {
		t.Errorf("TotalCovered=%d want 1", rep.TotalCovered)
	}
	// AppService.About needs only happy, so it is fully covered; every other
	// action still has a gap.
	if rep.ActionsWithGaps != actions-1 {
		t.Errorf("ActionsWithGaps=%d want %d", rep.ActionsWithGaps, actions-1)
	}
	if rep.CoverageComplete {
		t.Error("CoverageComplete should be false with gaps")
	}
	if rep.ContractHash == "" || rep.ContractHash != scenariocontract.Hash() {
		t.Errorf("ContractHash=%q want %q", rep.ContractHash, scenariocontract.Hash())
	}
}

// TestInventoryMarkerRejected: an inventory marker in the stream is a hard
// error — the denominator must never come from test output.
func TestInventoryMarkerRejected(t *testing.T) {
	stream := out("TestX", scenariocontract.InventoryMarker+`{"whatever":1}`) + ev("pass", "TestX", "")
	if _, err := reconcile(strings.NewReader(stream), defaultOpts()); err == nil {
		t.Fatal("expected an error for an inventory marker in the stream")
	}
}

// TestMalformedClaimIsError: once a claim marker is seen, invalid JSON is an
// error, not silently ignored.
func TestMalformedClaimIsError(t *testing.T) {
	stream := out("TestX", scenariocontract.ClaimMarker+`{"action":`) + ev("pass", "TestX", "")
	if _, err := reconcile(strings.NewReader(stream), defaultOpts()); err == nil {
		t.Fatal("expected an error for a malformed claim")
	}
}

// TestUnknownFieldIsError: a claim carrying an unexpected field is rejected.
func TestUnknownFieldIsError(t *testing.T) {
	stream := out("TestX", scenariocontract.ClaimMarker+`{"action":"AppService.About","scenario":"happy","evidence":"e","x":1}`) + ev("pass", "TestX", "")
	if _, err := reconcile(strings.NewReader(stream), defaultOpts()); err == nil {
		t.Fatal("expected an error for an unknown claim field")
	}
}

// TestUnknownActionOrScenarioIsError: a claim for a non-contract action, or a
// real action with a scenario it does not require, is an error.
func TestUnknownActionOrScenarioIsError(t *testing.T) {
	bad := out("TestX", scenariocontract.ClaimMarker+claimPayload("Nope.NotReal", "happy", "e")) + ev("pass", "TestX", "")
	if _, err := reconcile(strings.NewReader(bad), defaultOpts()); err == nil {
		t.Fatal("expected an error for an unknown action")
	}
	wrongScenario := out("TestX", scenariocontract.ClaimMarker+claimPayload("AppService.About", "conflict", "e")) + ev("pass", "TestX", "")
	if _, err := reconcile(strings.NewReader(wrongScenario), defaultOpts()); err == nil {
		t.Fatal("expected an error for a scenario the action does not require")
	}
}

// TestDuplicateClaimIsError: the same test crediting the same cell twice is an
// error, not a double credit.
func TestDuplicateClaimIsError(t *testing.T) {
	stream := claimLine("TestX", "AppService.About", "happy") +
		claimLine("TestX", "AppService.About", "happy") + ev("pass", "TestX", "")
	if _, err := reconcile(strings.NewReader(stream), defaultOpts()); err == nil {
		t.Fatal("expected an error for a duplicate claim")
	}
}

// TestChunkedMarkerReassembled: test2json splits a long line into ~1KB
// output events; a claim split across events must reassemble and credit.
func TestChunkedMarkerReassembled(t *testing.T) {
	full := scenariocontract.ClaimMarker + claimPayload("AppService.About", "happy", "chunked")
	// Emit the claim line in three output fragments with no newline until the
	// end, then a newline, exactly as chunking would present it.
	mid := len(full) / 2
	stream := ev("output", "TestChunk", full[:mid]) +
		ev("output", "TestChunk", full[mid:]) +
		ev("output", "TestChunk", "\n") +
		ev("pass", "TestChunk", "")
	rep := run(t, stream, defaultOpts())
	if !coveredScenarios(rep, "AppService.About")["happy"] {
		t.Fatal("a claim split across output events was not reassembled and credited")
	}
}

// TestNonZeroTestExitInvalidates: a nonzero producer exit sets valid:false
// even when the stream parsed cleanly (the upstream-exit-42 attack).
func TestNonZeroTestExitInvalidates(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "")
	rep := run(t, stream, options{testExit: 42})
	if rep.ProcessSucceeded {
		t.Error("ProcessSucceeded true with exit 42")
	}
	if rep.Valid {
		t.Error("Valid true despite a nonzero producer exit")
	}
}

// TestTruncatedStreamInvalid: an expected package that never terminates makes
// the stream incomplete and the report invalid (the truncation attack).
func TestTruncatedStreamInvalid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	opts := defaultOpts()
	opts.expectedPackages = []string{"p", "q"} // q never appears
	rep := run(t, stream, opts)
	if rep.StreamComplete {
		t.Error("StreamComplete true with a missing expected package")
	}
	if rep.Valid {
		t.Error("Valid true with a truncated stream")
	}
	if len(rep.MissingPackages) != 1 || rep.MissingPackages[0] != "q" {
		t.Errorf("MissingPackages=%v want [q]", rep.MissingPackages)
	}
}

// TestExpectedPackagesCompleteValid: every expected package terminating makes
// a clean run valid.
func TestExpectedPackagesCompleteValid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	opts := defaultOpts()
	opts.expectedPackages = []string{"p"}
	rep := run(t, stream, opts)
	if !rep.StreamComplete || !rep.Valid {
		t.Errorf("expected a valid, complete report: valid=%t streamComplete=%t", rep.Valid, rep.StreamComplete)
	}
}

// TestOversizedProtocolLineIsError: a single unterminated line beyond the
// protocol limit is rejected rather than buffered unboundedly.
func TestOversizedProtocolLineIsError(t *testing.T) {
	huge := strings.Repeat("x", maxProtocolLine+10)
	stream := ev("output", "TestX", huge) // no newline
	if _, err := reconcile(strings.NewReader(stream), defaultOpts()); err == nil {
		t.Fatal("expected an error for an oversized protocol line")
	}
}
