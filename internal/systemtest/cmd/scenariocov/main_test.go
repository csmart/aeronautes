package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/scenariocontract"
)

// This package is the reconciler's own unit tests, so it legitimately
// constructs marker fixtures (the systemtest marker-hygiene lint exempts
// cmd/scenariocov). These regressions retain the reproduced attacks from the
// review rounds.

func ev(action, test, output string) string {
	e := event{Action: action, Package: "p", Test: test, Output: output}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

func evPkg(pkg, action, test, output string) string {
	e := event{Action: action, Package: pkg, Test: test, Output: output}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

func pkgEvent(action string) string { return pkgEventFor("p", action) }

func pkgEventFor(pkg, action string) string {
	e := event{Action: action, Package: pkg}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

// out emits a test output event whose payload ends in a newline.
func out(test, payload string) string { return ev("output", test, payload+"\n") }

func claimPayload(action, scenario, evidence string) string {
	b, _ := json.Marshal(scenariocontract.Claim{Action: action, Scenario: scenario, Evidence: evidence})
	return string(b)
}

func claimLine(test, action, scenario string) string {
	return out(test, scenariocontract.ClaimMarker+claimPayload(action, scenario, "ev "+test))
}

func baseOpts() options {
	return options{commitSHA: "sha", saioConfigured: true, testExit: 0, testExitProvided: true}
}

func validOpts(pkgs ...string) options {
	o := baseOpts()
	o.expectedPackages = pkgs
	return o
}

func reconcileOK(t *testing.T, stream string, opts options) report {
	t.Helper()
	rep, err := reconcile(strings.NewReader(stream), opts)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return rep
}

func mustError(t *testing.T, stream string, opts options) {
	t.Helper()
	if _, err := reconcile(strings.NewReader(stream), opts); err == nil {
		t.Fatal("expected an error, got none")
	}
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

// --- terminal-disposition attacks (skipped/failed/parallel/cleanup) ---

func TestOnlyPassingTestsAreCredited(t *testing.T) {
	const act = "MutateService.UpdateObjectMetadata" // requires happy, refusal, conflict
	stream := claimLine("TestHappy", act, "happy") + ev("pass", "TestHappy", "") +
		claimLine("TestSkipped", act, "refusal") + ev("skip", "TestSkipped", "") +
		claimLine("TestFailed", act, "conflict") + ev("fail", "TestFailed", "")

	opts := baseOpts()
	opts.testExit = 1
	rep := reconcileOK(t, stream, opts)
	got := coveredScenarios(rep, act)
	if !got["happy"] {
		t.Error("a passing test's claim was not credited")
	}
	if got["refusal"] || got["conflict"] {
		t.Error("a skipped or failed test's claim was credited")
	}
	if rep.Valid {
		t.Error("report marked valid despite the producer exiting nonzero")
	}
}

// --- claims are top-level only; subtest claims are rejected ---

func TestSubtestClaimIsRejected(t *testing.T) {
	mustError(t, out("TestParent/child", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e"))+ev("pass", "TestParent/child", ""), baseOpts())
}

// --- T-01: claims are bound to the exact expected package event set ---

func TestExtraPackageClaimNotCredited(t *testing.T) {
	// An extra package emits a claim and a passing test but no package
	// terminal; expected set is only [p]. The claim must not be credited and
	// the stream must be invalid.
	stream := evPkg("extra", "output", "TestExtra", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e")+"\n") +
		evPkg("extra", "pass", "TestExtra", "") +
		claimLine("TestH", "AppService.Info", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if coveredScenarios(rep, "AppService.About")["happy"] {
		t.Error("a claim from an unexpected package was credited")
	}
	if rep.StreamComplete || rep.Valid {
		t.Error("unexpected package activity should invalidate the stream")
	}
	found := false
	for _, u := range rep.UnexpectedPkgs {
		if u == "extra" {
			found = true
		}
	}
	if !found {
		t.Errorf("UnexpectedPkgs=%v want it to include extra", rep.UnexpectedPkgs)
	}
}

// --- T-02: one-terminal lifecycle; no post-terminal or conflicting events ---

func TestOutputAfterTestTerminalRejected(t *testing.T) {
	// The claim output arrives AFTER the test's terminal.
	stream := ev("pass", "TestLate", "") + claimLine("TestLate", "AppService.About", "happy") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if coveredScenarios(rep, "AppService.About")["happy"] {
		t.Error("a post-terminal claim was credited")
	}
	if rep.Valid || len(rep.Violations) == 0 {
		t.Errorf("post-terminal output should invalidate: valid=%t violations=%v", rep.Valid, rep.Violations)
	}
}

func TestConflictingTerminalsRejected(t *testing.T) {
	// A test that terminally fails and then terminally passes.
	stream := claimLine("TestFlip", "AppService.About", "happy") +
		ev("fail", "TestFlip", "") + ev("pass", "TestFlip", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if coveredScenarios(rep, "AppService.About")["happy"] {
		t.Error("a claim from a test with conflicting terminals was credited")
	}
	if rep.Valid || len(rep.Violations) == 0 {
		t.Errorf("conflicting terminals should invalidate: valid=%t violations=%v", rep.Valid, rep.Violations)
	}
}

func TestRunThenPackageSkipIsTested(t *testing.T) {
	// A test-level run followed by a package-level skip is NOT a genuine
	// no-test package: sawTest must be set on the run event.
	stream := ev("run", "TestX", "") + pkgEvent("skip")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.StreamComplete || rep.Valid {
		t.Error("a tested package that skipped should not count complete")
	}
	if len(rep.FailedPackages) != 1 || rep.FailedPackages[0] != "p" {
		t.Errorf("FailedPackages=%v want [p]", rep.FailedPackages)
	}
}

// --- T-04: package-level protocol markers are rejected ---

func TestPackageLevelInventoryMarkerRejected(t *testing.T) {
	mustError(t, evPkg("p", "output", "", scenariocontract.InventoryMarker+`{"x":1}`+"\n")+pkgEvent("pass"), validOpts("p"))
}

func TestPackageLevelClaimMarkerRejected(t *testing.T) {
	mustError(t, evPkg("p", "output", "", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e")+"\n")+pkgEvent("pass"), validOpts("p"))
}

// --- T-03: bounds are enforced before processing; memory is bounded ---

func TestOversizedCompletedLineRejected(t *testing.T) {
	// A ~1.4 MiB logical line assembled across two fragments (each event line
	// well under the scanner limit), ending in a valid claim and a newline,
	// must be rejected before its claim is processed.
	frag1 := strings.Repeat("x", 700<<10) // no newline
	frag2 := strings.Repeat("y", 700<<10) + scenariocontract.ClaimMarker + claimPayload("AppService.About", "happy", "e") + "\n"
	stream := ev("output", "TestBig", frag1) + ev("output", "TestBig", frag2) + ev("pass", "TestBig", "") + pkgEvent("pass")
	mustError(t, stream, validOpts("p"))
}

func TestAggregateBudgetExceeded(t *testing.T) {
	var b strings.Builder
	chunk := strings.Repeat("z", 900<<10) // 900 KiB incomplete line per test
	for i := 0; i < 10; i++ {             // 9 MiB > 8 MiB aggregate cap
		b.WriteString(ev("output", "TestAgg"+strconv.Itoa(i), chunk))
	}
	mustError(t, b.String(), baseOpts())
}

func TestActiveBufferCapExceeded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxActiveBuffers+1; i++ {
		b.WriteString(ev("output", "T"+strconv.Itoa(i), "y")) // tiny incomplete line, unique test
	}
	mustError(t, b.String(), baseOpts())
}

func TestCompletedLinesDoNotRetain(t *testing.T) {
	// One test emits far more than the aggregate cap, but every line is
	// newline-terminated, so completed lines are released and no cap trips.
	var b strings.Builder
	line := strings.Repeat("d", 130<<10) + "\n" // 130 KiB complete lines
	for i := 0; i < 100; i++ {                  // ~13 MiB total, all completed
		b.WriteString(ev("output", "TestStream", line))
	}
	b.WriteString(ev("pass", "TestStream", "") + pkgEvent("pass"))
	reconcileOK(t, b.String(), validOpts("p")) // must not error
}

// --- retained protocol attacks ---

func TestInventoryMarkerRejected(t *testing.T) {
	mustError(t, out("TestX", scenariocontract.InventoryMarker+`{"whatever":1}`)+ev("pass", "TestX", ""), baseOpts())
}

func TestMalformedClaimIsError(t *testing.T) {
	mustError(t, out("TestX", scenariocontract.ClaimMarker+`{"action":`)+ev("pass", "TestX", ""), baseOpts())
}

func TestUnknownFieldIsError(t *testing.T) {
	mustError(t, out("TestX", scenariocontract.ClaimMarker+`{"action":"AppService.About","scenario":"happy","evidence":"e","x":1}`)+ev("pass", "TestX", ""), baseOpts())
}

func TestUnknownActionOrScenarioIsError(t *testing.T) {
	mustError(t, out("TestX", scenariocontract.ClaimMarker+claimPayload("Nope.NotReal", "happy", "e"))+ev("pass", "TestX", ""), baseOpts())
	mustError(t, out("TestX", scenariocontract.ClaimMarker+claimPayload("AppService.About", "conflict", "e"))+ev("pass", "TestX", ""), baseOpts())
}

func TestDuplicateClaimIsError(t *testing.T) {
	mustError(t, claimLine("TestX", "AppService.About", "happy")+claimLine("TestX", "AppService.About", "happy")+ev("pass", "TestX", ""), baseOpts())
}

func TestTrailingContentAfterClaimIsError(t *testing.T) {
	mustError(t, out("TestX", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e")+" trailing-garbage")+ev("pass", "TestX", ""), baseOpts())
	mustError(t, out("TestX", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e")+claimPayload("AppService.Info", "happy", "e"))+ev("pass", "TestX", ""), baseOpts())
}

func TestUnterminatedMarkerIsError(t *testing.T) {
	mustError(t, ev("output", "TestX", scenariocontract.ClaimMarker+`{"action":`)+ev("pass", "TestX", ""), baseOpts())
}

func TestUnterminatedMarkerAtEOFIsError(t *testing.T) {
	mustError(t, ev("output", "TestX", scenariocontract.ClaimMarker+`{"action":`), baseOpts())
}

func TestUnterminatedNonMarkerLineIsDropped(t *testing.T) {
	reconcileOK(t, ev("output", "TestX", "some diagnostic with no newline")+ev("pass", "TestX", ""), baseOpts())
}

func TestChunkedMarkerReassembled(t *testing.T) {
	full := scenariocontract.ClaimMarker + claimPayload("AppService.About", "happy", "chunked")
	mid := len(full) / 2
	stream := ev("output", "TestChunk", full[:mid]) + ev("output", "TestChunk", full[mid:]) +
		ev("output", "TestChunk", "\n") + ev("pass", "TestChunk", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if !coveredScenarios(rep, "AppService.About")["happy"] {
		t.Fatal("a claim split across output events was not reassembled and credited")
	}
}

// --- safe inputs / package completeness ---

func TestUnsafeDefaultsAreInvalid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, options{}) // nothing provided
	if rep.ProcessSucceeded || rep.StreamComplete || rep.Valid {
		t.Errorf("unsafe defaults must be invalid: process=%t stream=%t valid=%t", rep.ProcessSucceeded, rep.StreamComplete, rep.Valid)
	}
}

func TestMissingExpectedPackagesInvalid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	if reconcileOK(t, stream, baseOpts()).Valid {
		t.Error("valid true without an expected package set")
	}
}

func TestExpectedPackageFailIsIncomplete(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("fail")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.StreamComplete || rep.Valid {
		t.Error("an expected package that failed should be incomplete")
	}
	if len(rep.FailedPackages) != 1 || rep.FailedPackages[0] != "p" {
		t.Errorf("FailedPackages=%v want [p]", rep.FailedPackages)
	}
}

func TestNoTestPackageSkipIsComplete(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass") + pkgEventFor("q", "skip")
	rep := reconcileOK(t, stream, validOpts("p", "q"))
	if !rep.StreamComplete || !rep.Valid {
		t.Errorf("a no-test package skip should be complete: streamComplete=%t valid=%t", rep.StreamComplete, rep.Valid)
	}
}

func TestUnexpectedPackageInvalid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass") + pkgEventFor("r", "pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.StreamComplete || rep.Valid {
		t.Error("an unexpected package should invalidate the stream")
	}
}

func TestDuplicatePackageTerminalInvalid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.StreamComplete || rep.Valid || len(rep.Violations) == 0 {
		t.Error("a duplicate package terminal should invalidate the stream")
	}
}

func TestNonZeroTestExitInvalidates(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	opts := validOpts("p")
	opts.testExit = 42
	rep := reconcileOK(t, stream, opts)
	if rep.ProcessSucceeded || rep.Valid {
		t.Error("a nonzero producer exit must invalidate")
	}
}

func TestTruncatedStreamInvalid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p", "q"))
	if rep.StreamComplete || len(rep.MissingPackages) != 1 || rep.MissingPackages[0] != "q" {
		t.Errorf("truncated stream: streamComplete=%t missing=%v", rep.StreamComplete, rep.MissingPackages)
	}
}

func TestExpectedPackagesCompleteValid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if !rep.StreamComplete || !rep.Valid {
		t.Errorf("expected a valid, complete report: valid=%t streamComplete=%t", rep.Valid, rep.StreamComplete)
	}
	if !coveredScenarios(rep, "AppService.About")["happy"] {
		t.Error("the top-level claim was not credited")
	}
}

func TestOversizedProtocolLineIsError(t *testing.T) {
	mustError(t, ev("output", "TestX", strings.Repeat("x", maxProtocolLine+10)), baseOpts())
}

func TestGapsAndTotals(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	actions, required := contractTotals()
	if rep.TotalActions != actions || rep.TotalRequired != required || rep.TotalCovered != 1 {
		t.Errorf("totals wrong: actions=%d/%d required=%d/%d covered=%d/1", rep.TotalActions, actions, rep.TotalRequired, required, rep.TotalCovered)
	}
	if rep.ActionsWithGaps != actions-1 || rep.CoverageComplete {
		t.Errorf("gaps wrong: actionsWithGaps=%d coverageComplete=%t", rep.ActionsWithGaps, rep.CoverageComplete)
	}
	if rep.ContractHash != scenariocontract.Hash() {
		t.Errorf("ContractHash=%q want %q", rep.ContractHash, scenariocontract.Hash())
	}
}

// --- T-05: command-level tests through run() ---

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runCLI(t *testing.T, stdin string, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stderr.String()
}

func validStream() string {
	return claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
}

func TestRunValidWithFlags(t *testing.T) {
	dir := t.TempDir()
	events := writeFile(t, dir, "events.jsonl", validStream())
	expected := writeFile(t, dir, "pkgs.txt", "p\n")
	outPath := filepath.Join(dir, "rep.json")
	code, _ := runCLI(t, "", "-events", events, "-expected-packages", expected, "-test-exit", "0", "-out", outPath)
	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	var rep report
	data, _ := os.ReadFile(outPath)
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("report unreadable: %v", err)
	}
	if !rep.Valid || rep.SchemaVersion != schemaVersion {
		t.Errorf("report not valid schema-3: %+v", rep)
	}
}

func TestRunUnsafeDefaultsInvalidNonzero(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "rep.json")
	// No -test-exit and no -expected-packages: cannot be valid.
	code, _ := runCLI(t, validStream(), "-out", outPath)
	if code != 1 {
		t.Fatalf("run returned %d, want 1 (invalid)", code)
	}
	var rep report
	data, _ := os.ReadFile(outPath)
	_ = json.Unmarshal(data, &rep)
	if rep.Valid {
		t.Error("unsafe defaults produced a valid report")
	}
}

func TestRunExplicitTestExitZeroCounts(t *testing.T) {
	dir := t.TempDir()
	events := writeFile(t, dir, "events.jsonl", validStream())
	expected := writeFile(t, dir, "pkgs.txt", "p\n")
	// WITHOUT -test-exit: processSucceeded false -> invalid -> exit 1.
	if code, _ := runCLI(t, "", "-events", events, "-expected-packages", expected); code != 1 {
		t.Errorf("omitted -test-exit should be invalid: code=%d", code)
	}
	// WITH explicit -test-exit 0: flag.Visit sees it -> valid -> exit 0.
	if code, _ := runCLI(t, "", "-events", events, "-expected-packages", expected, "-test-exit", "0"); code != 0 {
		t.Errorf("explicit -test-exit 0 should be valid: code=%d", code)
	}
}

func TestRunBadExpectedFileReturns2(t *testing.T) {
	dir := t.TempDir()
	events := writeFile(t, dir, "events.jsonl", validStream())
	dup := writeFile(t, dir, "dup.txt", "p\np\n")
	if code, _ := runCLI(t, "", "-events", events, "-expected-packages", dup, "-test-exit", "0"); code != 2 {
		t.Errorf("a duplicate expected-package file should return 2: code=%d", code)
	}
	empty := writeFile(t, dir, "empty.txt", "\n \n")
	if code, _ := runCLI(t, "", "-events", events, "-expected-packages", empty, "-test-exit", "0"); code != 2 {
		t.Errorf("an empty expected-package file should return 2: code=%d", code)
	}
}

func TestRunMissingEventsFileReturns2(t *testing.T) {
	if code, _ := runCLI(t, "", "-events", filepath.Join(t.TempDir(), "nope.jsonl")); code != 2 {
		t.Errorf("a missing events file should return 2: code=%d", code)
	}
}

func TestRunProtocolErrorWritesReport(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "rep.json")
	bad := out("TestX", scenariocontract.InventoryMarker+`{"x":1}`) + ev("pass", "TestX", "")
	code, _ := runCLI(t, bad, "-out", outPath, "-test-exit", "0")
	if code != 2 {
		t.Fatalf("a protocol error should return 2: code=%d", code)
	}
	var rep report
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("no report written on protocol error: %v", err)
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("error report not JSON: %v", err)
	}
	if rep.Valid || rep.ProtocolError == "" || rep.SchemaVersion != schemaVersion {
		t.Errorf("protocol-error report wrong: valid=%t protocolError=%q schema=%d", rep.Valid, rep.ProtocolError, rep.SchemaVersion)
	}
}

// --- real go test -json subprocess regressions ---

func runFixture(t *testing.T, runArg string) []byte {
	t.Helper()
	if testing.Short() {
		t.Skip("skips the go test subprocess in -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out, err := exec.Command("go", "test", "-json", "-v", "-count=1", "-run", runArg, "./testdata/slashsub").Output()
	if err != nil {
		t.Fatalf("fixture subprocess (%s) failed: %v\n%s", runArg, err, out)
	}
	return out
}

func TestTopLevelClaimThroughGoTestJSON(t *testing.T) {
	rep, err := reconcile(bytes.NewReader(runFixture(t, "^TestTopLevel$")), baseOpts())
	if err != nil {
		t.Fatalf("reconcile of real top-level stream: %v", err)
	}
	if !coveredScenarios(rep, "AppService.About")["happy"] {
		t.Fatal("a real top-level claim was not credited")
	}
}

func TestSubtestClaimRejectedThroughGoTestJSON(t *testing.T) {
	if _, err := reconcile(bytes.NewReader(runFixture(t, "^TestSlash$")), baseOpts()); err == nil {
		t.Fatal("a real subtest claim was not rejected")
	}
}
