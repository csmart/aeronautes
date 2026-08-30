package systemtest

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
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

// fakeNew mimics New's disposition-capture (a first-registered, so
// last-running, cleanup) without SAIO, with one pending credit queued.
func fakeNew(fake string) func(*testing.T) *Harness {
	return func(st *testing.T) *Harness {
		h := &Harness{T: st, pending: []recordedCredit{{fake, "happy"}}}
		st.Cleanup(func() {
			h.finalFailed = st.Failed()
			h.finalSkipped = st.Skipped()
		})
		return h
	}
}

// TestRunScenarioDisposition exercises the REAL testing.T lifecycle through
// runScenario with an injected harness (codex war-room F-02): a passing
// subtest commits, a subtest that SKIPS in its body does not, and — the
// exact reproduction — a subtest that SKIPS in a LATE CLEANUP (registered
// after the harness, so it runs before the disposition capture) also
// commits nothing. A body failure shares the same Failed() guard; a
// failing subtest would fail this parent, which Go cannot cleanly isolate.
func TestRunScenarioDisposition(t *testing.T) {
	const fake = "SentinelService.Only" // absent from Inventory → never pollutes the report
	clear := func() { scenarioMu.Lock(); delete(scenarioSeen, fake); scenarioMu.Unlock() }
	defer clear()

	clear()
	runScenario(t, fakeNew(fake), func(st *testing.T, h *Harness) {})
	if _, ok := recordedScenarios()[fake]; !ok {
		t.Error("a passing scenario did not commit its credit")
	}

	clear()
	runScenario(t, fakeNew(fake), func(st *testing.T, h *Harness) { st.Skip("body skip") })
	if _, ok := recordedScenarios()[fake]; ok {
		t.Error("a body-skipped scenario earned credit")
	}

	clear()
	runScenario(t, fakeNew(fake), func(st *testing.T, h *Harness) {
		st.Cleanup(func() { st.Skip("late cleanup skip") }) // runs before the disposition capture
	})
	if _, ok := recordedScenarios()[fake]; ok {
		t.Error("a late-cleanup-skip scenario earned credit (F-02 reproduction)")
	}
}

// TestLedgerConcurrencySafe drives one harness ledger from many goroutines,
// so `go test -race` proves the invocation/credit bookkeeping is safe under
// the concurrency this war room exists to exercise (codex war-room F-03).
func TestLedgerConcurrencySafe(t *testing.T) {
	const action = "TransferService.StartCopy"
	h := &Harness{T: t}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.markInvoked(action)
			h.claim(action, "happy")
		}()
	}
	wg.Wait()
	h.mu.Lock()
	got := len(h.pending)
	h.mu.Unlock()
	if got != 16 {
		t.Fatalf("expected 16 pending claims, got %d", got)
	}
}
