package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// ev builds one test2json line.
func ev(action, test, output string) string {
	e := event{Action: action, Package: "p", Test: test, Output: output}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

func invLine() string {
	inv := map[string]inventoryAction{"Svc.Act": {Kind: "job", Required: []string{"happy", "refusal", "conflict"}}}
	b, _ := json.Marshal(inv)
	return ev("output", "TestInv", inventoryMarker+string(b)) + ev("pass", "TestInv", "")
}

func claimLine(test, action, scenario string) string {
	b, _ := json.Marshal(claim{Action: action, Scenario: scenario, Evidence: "evidence for " + test})
	return ev("output", test, claimMarker+string(b))
}

func run(t *testing.T, stream string) report {
	t.Helper()
	rep, _, _, err := reconcile(strings.NewReader(stream), "sha", true)
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

// TestOnlyPassingTestsAreCredited is the core F-02 regression: a claim from
// a test that SKIPPED or FAILED earns no credit, only a passing test does —
// exactly the parallel-child-skip and parent-cleanup-skip spoofs, reduced
// to their observable event: the test's terminal disposition.
func TestOnlyPassingTestsAreCredited(t *testing.T) {
	stream := invLine() +
		claimLine("TestHappy", "Svc.Act", "happy") + ev("pass", "TestHappy", "") +
		claimLine("TestSkipped", "Svc.Act", "refusal") + ev("skip", "TestSkipped", "") +
		claimLine("TestFailed", "Svc.Act", "conflict") + ev("fail", "TestFailed", "")

	rep := run(t, stream)
	got := coveredScenarios(rep, "Svc.Act")
	if !got["happy"] {
		t.Error("a passing test's claim was not credited")
	}
	if got["refusal"] {
		t.Error("a SKIPPED test's claim was credited (F-02)")
	}
	if got["conflict"] {
		t.Error("a FAILED test's claim was credited (F-02)")
	}
	if rep.Valid {
		t.Error("report marked valid despite a failed test")
	}
}

// TestAncestorMustPass: a claim from a passing subtest whose PARENT failed
// is not credited.
func TestAncestorMustPass(t *testing.T) {
	stream := invLine() +
		claimLine("TestParent/child", "Svc.Act", "happy") +
		ev("pass", "TestParent/child", "") +
		ev("fail", "TestParent", "")

	rep := run(t, stream)
	if coveredScenarios(rep, "Svc.Act")["happy"] {
		t.Error("a claim credited despite its parent failing")
	}
}

// TestNoInventoryFails: a stream without the inventory line is an error, so
// a missing denominator cannot silently produce a zero-required report.
func TestNoInventoryFails(t *testing.T) {
	_, _, _, err := reconcile(strings.NewReader(ev("pass", "TestX", "")), "", false)
	if err == nil {
		t.Fatal("expected an error when no inventory line is present")
	}
}

// TestGapsAndTotals sanity-checks the denominator/numerator math.
func TestGapsAndTotals(t *testing.T) {
	stream := invLine() + claimLine("TestH", "Svc.Act", "happy") + ev("pass", "TestH", "")
	rep := run(t, stream)
	if rep.TotalRequired != 3 || rep.TotalCovered != 1 || rep.ActionsWithGaps != 1 {
		t.Fatalf("totals wrong: required=%d covered=%d gaps=%d", rep.TotalRequired, rep.TotalCovered, rep.ActionsWithGaps)
	}
}
