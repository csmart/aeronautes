package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/scenariocontract"
)

// This package is the reconciler's own unit tests, so it legitimately
// constructs marker fixtures (the systemtest marker-hygiene lint exempts
// cmd/scenariocov). These regressions retain the exact attacks from the
// review rounds.

func ev(action, test, output string) string {
	e := event{Action: action, Package: "p", Test: test, Output: output}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

func pkgEvent(action string) string { return pkgEventFor("p", action) }

func pkgEventFor(pkg, action string) string {
	e := event{Action: action, Package: pkg}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

// out emits a test output event whose payload ends in a newline, matching
// how test2json presents a t.Logf line.
func out(test, payload string) string { return ev("output", test, payload+"\n") }

func claimPayload(action, scenario, evidence string) string {
	b, _ := json.Marshal(scenariocontract.Claim{Action: action, Scenario: scenario, Evidence: evidence})
	return string(b)
}

func claimLine(test, action, scenario string) string {
	return out(test, scenariocontract.ClaimMarker+claimPayload(action, scenario, "ev "+test))
}

// baseOpts supplies the producer exit as actually provided (exit 0) but no
// expected package set.
func baseOpts() options {
	return options{commitSHA: "sha", saioConfigured: true, testExit: 0, testExitProvided: true}
}

// validOpts additionally supplies the expected package set, the combination
// CI uses and the only one that can yield valid:true.
func validOpts(pkgs ...string) options {
	o := baseOpts()
	o.expectedPackages = pkgs
	return o
}

func run(t *testing.T, stream string, opts options) report {
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
	opts.testExit = 1 // a failed test means the producer exited nonzero in CI
	rep := run(t, stream, opts)
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

// --- claims are top-level only; subtest claims are rejected ---

func TestSubtestClaimIsRejected(t *testing.T) {
	// A slash-bearing claimant name is a subtest; parentage is ambiguous, so
	// it must be rejected rather than silently mis-credited or mis-dropped.
	mustError(t, out("TestParent/child", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e"))+ev("pass", "TestParent/child", ""), baseOpts())
}

// --- an expected package must PASS, not merely terminate ---

func TestExpectedPackageFailIsIncomplete(t *testing.T) {
	// The exact attack: a clean claim/test-pass stream, a package-level FAIL,
	// and a supplied exit 0.
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("fail")
	rep := run(t, stream, validOpts("p"))
	if rep.StreamComplete {
		t.Error("StreamComplete true despite the expected package failing")
	}
	if rep.Valid {
		t.Error("Valid true despite the expected package failing")
	}
	if len(rep.FailedPackages) != 1 || rep.FailedPackages[0] != "p" {
		t.Errorf("FailedPackages=%v want [p]", rep.FailedPackages)
	}
}

func TestNoTestPackageSkipIsComplete(t *testing.T) {
	// A package with no test files terminates with a package-level skip and
	// must still count complete (go emits skip for "[no test files]").
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass") +
		pkgEventFor("q", "skip") // q: no tests, package-level skip
	rep := run(t, stream, validOpts("p", "q"))
	if !rep.StreamComplete || !rep.Valid {
		t.Errorf("a no-test package skip should be complete: streamComplete=%t valid=%t missing=%v failed=%v",
			rep.StreamComplete, rep.Valid, rep.MissingPackages, rep.FailedPackages)
	}
}

// --- strict parsing rejects trailing content after the claim ---

func TestTrailingContentAfterClaimIsError(t *testing.T) {
	mustError(t, out("TestX", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e")+" trailing-garbage")+ev("pass", "TestX", ""), baseOpts())
	// A second concatenated JSON value is equally rejected.
	mustError(t, out("TestX", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e")+claimPayload("AppService.Info", "happy", "e"))+ev("pass", "TestX", ""), baseOpts())
}

// --- an unterminated marker line is rejected, not silently lost ---

func TestUnterminatedMarkerIsError(t *testing.T) {
	// A marker with no terminating newline, flushed at the test's terminal
	// event.
	stream := ev("output", "TestX", scenariocontract.ClaimMarker+`{"action":`) + ev("pass", "TestX", "")
	mustError(t, stream, baseOpts())
}

func TestUnterminatedMarkerAtEOFIsError(t *testing.T) {
	// The same, with no terminal event at all: caught by the EOF flush.
	mustError(t, ev("output", "TestX", scenariocontract.ClaimMarker+`{"action":`), baseOpts())
}

func TestUnterminatedNonMarkerLineIsDropped(t *testing.T) {
	// A residual line WITHOUT a marker is ordinary output; dropping it is fine.
	stream := ev("output", "TestX", "some diagnostic with no newline") + ev("pass", "TestX", "")
	run(t, stream, baseOpts()) // must not error
}

// --- safe inputs are mandatory for valid:true ---

func TestUnsafeDefaultsAreInvalid(t *testing.T) {
	// A clean, complete-looking stream with NEITHER a provided exit nor an
	// expected set must not be valid.
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := run(t, stream, options{}) // nothing provided
	if rep.ProcessSucceeded {
		t.Error("ProcessSucceeded true without a provided producer exit")
	}
	if rep.StreamComplete {
		t.Error("StreamComplete true without an expected package set")
	}
	if rep.Valid {
		t.Error("Valid true with unsafe default inputs")
	}
}

func TestMissingExpectedPackagesInvalid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := run(t, stream, baseOpts()) // exit provided, but no expected set
	if rep.Valid {
		t.Error("Valid true without an expected package set")
	}
}

// --- exact expected set (unexpected/duplicate/empty/duplicate file) ---

func TestUnexpectedPackageInvalid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass") + pkgEventFor("r", "pass")
	rep := run(t, stream, validOpts("p"))
	if rep.StreamComplete || rep.Valid {
		t.Error("an unexpected package terminal should invalidate the stream")
	}
	if len(rep.UnexpectedPkgs) != 1 || rep.UnexpectedPkgs[0] != "r" {
		t.Errorf("UnexpectedPkgs=%v want [r]", rep.UnexpectedPkgs)
	}
}

func TestDuplicatePackageTerminalInvalid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass") + pkgEvent("pass")
	rep := run(t, stream, validOpts("p"))
	if rep.StreamComplete || rep.Valid {
		t.Error("a duplicate package terminal should invalidate the stream")
	}
}

func TestEmptyOrDuplicateExpectedFileRejected(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, []byte("\n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readExpected(empty); err == nil {
		t.Error("an empty expected-package file should be rejected")
	}
	dup := filepath.Join(dir, "dup.txt")
	if err := os.WriteFile(dup, []byte("a\nb\na\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readExpected(dup); err == nil {
		t.Error("a duplicate expected-package file should be rejected")
	}
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

func TestChunkedMarkerReassembled(t *testing.T) {
	full := scenariocontract.ClaimMarker + claimPayload("AppService.About", "happy", "chunked")
	mid := len(full) / 2
	stream := ev("output", "TestChunk", full[:mid]) +
		ev("output", "TestChunk", full[mid:]) +
		ev("output", "TestChunk", "\n") +
		ev("pass", "TestChunk", "")
	rep := run(t, stream, baseOpts())
	if !coveredScenarios(rep, "AppService.About")["happy"] {
		t.Fatal("a claim split across output events was not reassembled and credited")
	}
}

func TestNonZeroTestExitInvalidates(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	opts := validOpts("p")
	opts.testExit = 42
	rep := run(t, stream, opts)
	if rep.ProcessSucceeded {
		t.Error("ProcessSucceeded true with exit 42")
	}
	if rep.Valid {
		t.Error("Valid true despite a nonzero producer exit")
	}
}

func TestTruncatedStreamInvalid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := run(t, stream, validOpts("p", "q")) // q never appears
	if rep.StreamComplete {
		t.Error("StreamComplete true with a missing expected package")
	}
	if len(rep.MissingPackages) != 1 || rep.MissingPackages[0] != "q" {
		t.Errorf("MissingPackages=%v want [q]", rep.MissingPackages)
	}
}

func TestExpectedPackagesCompleteValid(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := run(t, stream, validOpts("p"))
	if !rep.StreamComplete || !rep.Valid {
		t.Errorf("expected a valid, complete report: valid=%t streamComplete=%t", rep.Valid, rep.StreamComplete)
	}
}

func TestOversizedProtocolLineIsError(t *testing.T) {
	mustError(t, ev("output", "TestX", strings.Repeat("x", maxProtocolLine+10)), baseOpts())
}

func TestGapsAndTotals(t *testing.T) {
	stream := claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "")
	rep := run(t, stream, baseOpts())
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

// --- a protocol error still leaves a machine-readable invalid report ---

func TestErrorReportWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "err.json")
	writeErrorReport(path, baseOpts(), errInvalidStreamForTest())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no error report written: %v", err)
	}
	var rep report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("error report is not valid JSON: %v", err)
	}
	if rep.SchemaVersion != schemaVersion || rep.Valid || rep.ProtocolError == "" {
		t.Errorf("error report wrong: schema=%d valid=%t protocolError=%q", rep.SchemaVersion, rep.Valid, rep.ProtocolError)
	}
}

func errInvalidStreamForTest() error { return errTest("synthetic protocol failure") }

type errTest string

func (e errTest) Error() string { return string(e) }

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
	// The ambiguous case through REAL go test -json: a/b passes, sibling a
	// skips, and the subtest claim must be rejected — not silently dropped by
	// mistaking the skipped sibling for an ancestor.
	if _, err := reconcile(bytes.NewReader(runFixture(t, "^TestSlash$")), baseOpts()); err == nil {
		t.Fatal("a real subtest claim was not rejected")
	}
}
