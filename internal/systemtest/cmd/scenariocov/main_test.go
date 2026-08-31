package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/scenariocontract"
)

// This package is the reconciler's own unit tests, so it legitimately
// constructs marker fixtures (the systemtest marker-hygiene lint exempts
// cmd/scenariocov). Valid fixtures follow the authentic test2json order
// (package start, test run, output, test terminal, package terminal);
// malformed fixtures deliberately break it and must be rejected.

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

func startEvent(pkg string) string { return evPkg(pkg, "start", "", "") }
func runEvent(test string) string  { return ev("run", test, "") }

// out emits a test output event whose payload ends in a newline.
func out(test, payload string) string { return ev("output", test, payload+"\n") }

func claimPayload(action, scenario, evidence string) string {
	b, _ := json.Marshal(scenariocontract.Claim{Action: action, Scenario: scenario, Evidence: evidence})
	return string(b)
}

func claimLine(test, action, scenario string) string {
	return out(test, scenariocontract.ClaimMarker+claimPayload(action, scenario, "ev "+test))
}

// tSeg is one test's authentic segment in package p: run, body lines, terminal.
func tSeg(test, terminal string, body ...string) string {
	s := runEvent(test)
	for _, b := range body {
		s += b
	}
	return s + ev(terminal, test, "")
}

// pkgP wraps test segments in a well-formed package "p": start ... terminal.
func pkgP(pkgTerminal string, segs ...string) string {
	s := startEvent("p")
	for _, seg := range segs {
		s += seg
	}
	return s + pkgEventFor("p", pkgTerminal)
}

// validStream is a canonical well-formed single-test package that credits one
// top-level happy claim.
func validStream() string {
	return pkgP("pass", tSeg("TestH", "pass", claimLine("TestH", "AppService.About", "happy")))
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

// --- happy path + terminal-disposition attacks ---

func TestExpectedPackagesCompleteValid(t *testing.T) {
	rep := reconcileOK(t, validStream(), validOpts("p"))
	if !rep.StreamComplete || !rep.Valid {
		t.Errorf("valid=%t streamComplete=%t violations=%v", rep.Valid, rep.StreamComplete, rep.Violations)
	}
	if !coveredScenarios(rep, "AppService.About")["happy"] {
		t.Error("the top-level claim was not credited")
	}
}

func TestOnlyPassingTestsAreCredited(t *testing.T) {
	const act = "MutateService.UpdateObjectMetadata" // requires happy, refusal, conflict
	stream := pkgP("fail",
		tSeg("TestHappy", "pass", claimLine("TestHappy", act, "happy")),
		tSeg("TestSkipped", "skip", claimLine("TestSkipped", act, "refusal")),
		tSeg("TestFailed", "fail", claimLine("TestFailed", act, "conflict")),
	)
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

func TestSubtestClaimIsRejected(t *testing.T) {
	mustError(t, startEvent("p")+runEvent("TestParent")+runEvent("TestParent/child")+
		out("TestParent/child", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e")), baseOpts())
}

// --- T-02: explicit lifecycle automaton ---

func TestRunAfterTerminalRejected(t *testing.T) {
	// claim; TestH pass; TestH run AGAIN; package pass.
	stream := startEvent("p") + runEvent("TestH") + claimLine("TestH", "AppService.About", "happy") +
		ev("pass", "TestH", "") + runEvent("TestH") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.Valid || coveredScenarios(rep, "AppService.About")["happy"] {
		t.Errorf("run-after-terminal should invalidate and not credit: valid=%t", rep.Valid)
	}
}

func TestDanglingTestRejected(t *testing.T) {
	// A started test that never terminates, beneath a package pass.
	stream := startEvent("p") + runEvent("TestDangling") +
		tSeg("TestGood", "pass", claimLine("TestGood", "AppService.About", "happy")) + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.Valid {
		t.Error("a dangling started test should invalidate the package")
	}
}

func TestFailedTestUnderPackagePassRejected(t *testing.T) {
	stream := pkgP("pass",
		tSeg("TestBad", "fail"),
		tSeg("TestGood", "pass", claimLine("TestGood", "AppService.About", "happy")),
	)
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.Valid {
		t.Error("a package pass with a failed test should invalidate the stream")
	}
}

func TestClaimWithoutRunRejected(t *testing.T) {
	// output + terminal for a test that never ran.
	stream := startEvent("p") + claimLine("TestNoRun", "AppService.About", "happy") +
		ev("pass", "TestNoRun", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.Valid || coveredScenarios(rep, "AppService.About")["happy"] {
		t.Errorf("a claim from a test that never ran must not be valid or credited: valid=%t", rep.Valid)
	}
}

func TestOutputAfterTestTerminalRejected(t *testing.T) {
	stream := startEvent("p") + runEvent("TestLate") + ev("pass", "TestLate", "") +
		claimLine("TestLate", "AppService.About", "happy") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.Valid || coveredScenarios(rep, "AppService.About")["happy"] {
		t.Errorf("post-terminal output should invalidate and not credit: valid=%t", rep.Valid)
	}
}

func TestConflictingTerminalsRejected(t *testing.T) {
	stream := startEvent("p") + runEvent("TestFlip") + claimLine("TestFlip", "AppService.About", "happy") +
		ev("fail", "TestFlip", "") + ev("pass", "TestFlip", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.Valid || coveredScenarios(rep, "AppService.About")["happy"] {
		t.Errorf("conflicting terminals should invalidate and not credit: valid=%t", rep.Valid)
	}
}

func TestPackageOutputAfterTerminalRejected(t *testing.T) {
	stream := validStream() + evPkg("p", "output", "", "trailing package output\n")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.Valid {
		t.Error("package output after the package terminal should invalidate")
	}
}

func TestPackageWithoutStartRejected(t *testing.T) {
	// No package start event at all.
	stream := runEvent("TestH") + claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.Valid {
		t.Error("a package terminal without a start should invalidate")
	}
}

func TestRunThenPackageSkipIsTested(t *testing.T) {
	stream := startEvent("p") + runEvent("TestX") + pkgEvent("skip")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.StreamComplete || rep.Valid {
		t.Error("a tested package that skipped should not count complete")
	}
	if len(rep.FailedPackages) != 1 || rep.FailedPackages[0] != "p" {
		t.Errorf("FailedPackages=%v want [p]", rep.FailedPackages)
	}
}

// --- T-01: exact expected-package ownership ---

func TestExtraPackageClaimNotCredited(t *testing.T) {
	// The extra package claims a DIFFERENT cell than validStream, so its
	// non-crediting is observable.
	stream := startEvent("extra") + runEvent("TestExtra") +
		evPkg("extra", "output", "TestExtra", scenariocontract.ClaimMarker+claimPayload("BookmarkService.List", "happy", "e")+"\n") +
		evPkg("extra", "pass", "TestExtra", "") + pkgEventFor("extra", "pass") +
		validStream()
	rep := reconcileOK(t, stream, validOpts("p"))
	if coveredScenarios(rep, "BookmarkService.List")["happy"] {
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

// --- T-03: non-JSON and unknown/misplaced records fail closed ---

func TestNonJSONLineInvalidates(t *testing.T) {
	stream := "this-is-not-json\n" + validStream()
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.Valid || rep.NonJSONLines != 1 {
		t.Errorf("a non-JSON line must invalidate: valid=%t nonJSONLines=%d", rep.Valid, rep.NonJSONLines)
	}
}

func TestUnknownActionInvalidates(t *testing.T) {
	stream := validStream() + ev("mystery", "TestH", "")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.Valid {
		t.Error("an unknown action must invalidate in machine-valid mode")
	}
}

func TestMarkerOnUnknownActionRejected(t *testing.T) {
	mustError(t, startEvent("p")+ev("mystery", "TestH", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e")), validOpts("p"))
}

func TestMarkerInEmptyPackageOutputRejected(t *testing.T) {
	e := event{Action: "output", Output: scenariocontract.ClaimMarker + claimPayload("AppService.About", "happy", "e") + "\n"}
	b, _ := json.Marshal(e)
	mustError(t, string(b)+"\n"+validStream(), validOpts("p"))
}

// --- T-01: reassembly is linear, not quadratic ---

func TestReassemblyIsLinear(t *testing.T) {
	// A ~512-KiB logical line delivered in 1-KiB fragments (the real test2json
	// shape) must allocate O(n), not O(n^2). The quadratic version allocated
	// >300 MB for this exact input.
	const frags = 512
	var b strings.Builder
	b.WriteString(startEvent("p") + runEvent("TestLin"))
	for i := 0; i < frags; i++ {
		b.WriteString(ev("output", "TestLin", strings.Repeat("a", 1024))) // 1 KiB, no newline
	}
	b.WriteString(ev("output", "TestLin", "\n")) // complete the big line
	b.WriteString(ev("pass", "TestLin", "") + pkgEvent("pass"))
	stream := b.String()

	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)
	if _, err := reconcile(strings.NewReader(stream), validOpts("p")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	runtime.ReadMemStats(&m2)
	if allocated := m2.TotalAlloc - m1.TotalAlloc; allocated > 64<<20 {
		t.Fatalf("reassembly allocated %d bytes for a %d-fragment line — quadratic?", allocated, frags)
	}
}

// --- bounds ---

func TestOversizedCompletedLineRejected(t *testing.T) {
	frag1 := strings.Repeat("x", 700<<10) // no newline
	frag2 := strings.Repeat("y", 700<<10) + scenariocontract.ClaimMarker + claimPayload("AppService.About", "happy", "e") + "\n"
	stream := startEvent("p") + runEvent("TestBig") + ev("output", "TestBig", frag1) + ev("output", "TestBig", frag2)
	mustError(t, stream, validOpts("p"))
}

func TestAggregateBudgetExceeded(t *testing.T) {
	var b strings.Builder
	b.WriteString(startEvent("p"))
	chunk := strings.Repeat("z", 900<<10)
	for i := 0; i < 10; i++ { // 9 MiB > 8 MiB
		test := "TestAgg" + strconv.Itoa(i)
		b.WriteString(runEvent(test) + ev("output", test, chunk))
	}
	mustError(t, b.String(), validOpts("p"))
}

func TestActiveBufferCapExceeded(t *testing.T) {
	var b strings.Builder
	b.WriteString(startEvent("p"))
	for i := 0; i < maxActiveBufs+1; i++ {
		test := "T" + strconv.Itoa(i)
		b.WriteString(runEvent(test) + ev("output", test, "y")) // tiny incomplete line each
	}
	mustError(t, b.String(), validOpts("p"))
}

// --- round-9: phase automaton, record shapes, benchmark scope ---

// TestStepPhaseTransitions is the transition-table conformance suite.
func TestStepPhaseTransitions(t *testing.T) {
	legal := []struct {
		from phase
		act  string
		to   phase
	}{
		{phaseIdle, "run", phaseRunning},
		{phaseRunning, "pause", phasePaused},
		{phasePaused, "cont", phaseRunning},
		{phaseRunning, "output", phaseRunning},
		{phasePaused, "output", phasePaused},
		{phaseRunning, "pass", phaseTerminal},
		{phasePaused, "pass", phaseTerminal},
		{phaseRunning, "fail", phaseTerminal},
		{phaseRunning, "skip", phaseTerminal},
	}
	for _, c := range legal {
		if got, ok := stepPhase(c.from, c.act); !ok || got != c.to {
			t.Errorf("stepPhase(%d,%q)=(%d,%t) want (%d,true)", c.from, c.act, got, ok, c.to)
		}
	}
	illegal := []struct {
		from phase
		act  string
	}{
		{phaseRunning, "run"},     // duplicate run
		{phasePaused, "run"},      // run while paused
		{phaseTerminal, "run"},    // run after terminal
		{phaseRunning, "cont"},    // cont before pause
		{phaseIdle, "cont"},       // cont before run
		{phasePaused, "pause"},    // duplicate pause
		{phaseIdle, "pause"},      // pause before run
		{phaseIdle, "output"},     // output before run
		{phaseTerminal, "output"}, // output after terminal
		{phaseIdle, "pass"},       // terminal without run
		{phaseTerminal, "pass"},   // duplicate terminal
	}
	for _, c := range illegal {
		if _, ok := stepPhase(c.from, c.act); ok {
			t.Errorf("stepPhase(%d,%q) unexpectedly legal", c.from, c.act)
		}
	}
}

// TestPausedClaimNotCredited: an authentic parallel order where TestA's claim
// is logged from a background goroutine while TestA is paused. The stream is
// legal (so valid stays true), but the paused claim is NOT running-phase and
// earns no credit.
func TestPausedClaimNotCredited(t *testing.T) {
	stream := startEvent("p") +
		runEvent("TestA") + ev("pause", "TestA", "") +
		runEvent("TestB") +
		claimLine("TestA", "AppService.About", "happy") + // logged while TestA paused
		ev("pass", "TestB", "") +
		ev("cont", "TestA", "") + ev("pass", "TestA", "") +
		pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if coveredScenarios(rep, "AppService.About")["happy"] {
		t.Error("a claim logged while its test was paused was credited as running")
	}
	if !rep.Valid {
		t.Errorf("an ordinary paused-output stream should stay valid: violations=%v", rep.Violations)
	}
}

// TestRunningClaimStillCredited: the same shape but the claim is logged after
// cont, while running, is credited — proving the paused guard is not blanket.
func TestRunningClaimStillCredited(t *testing.T) {
	stream := startEvent("p") +
		runEvent("TestA") + ev("pause", "TestA", "") +
		runEvent("TestB") + ev("pass", "TestB", "") +
		ev("cont", "TestA", "") +
		claimLine("TestA", "AppService.About", "happy") + // logged after cont, running
		ev("pass", "TestA", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if !coveredScenarios(rep, "AppService.About")["happy"] {
		t.Error("a running-phase claim after cont was not credited")
	}
	if !rep.Valid {
		t.Errorf("valid=false unexpectedly: violations=%v", rep.Violations)
	}
}

func TestDuplicateRunRejected(t *testing.T) {
	stream := startEvent("p") + runEvent("TestH") + runEvent("TestH") +
		claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.Valid || coveredScenarios(rep, "AppService.About")["happy"] {
		t.Errorf("a duplicate run should invalidate and not credit: valid=%t", rep.Valid)
	}
}

func TestContBeforePauseRejected(t *testing.T) {
	stream := startEvent("p") + runEvent("TestH") + ev("cont", "TestH", "") +
		claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	if reconcileOK(t, stream, validOpts("p")).Valid {
		t.Error("cont before pause should invalidate")
	}
}

func TestEmptyRecordInvalidates(t *testing.T) {
	for _, rec := range []string{"{}", "null"} {
		stream := rec + "\n" + validStream()
		if reconcileOK(t, stream, validOpts("p")).Valid {
			t.Errorf("a semantically empty record %q should invalidate", rec)
		}
	}
}

func TestAttributedEmptyPackageMarkerRejected(t *testing.T) {
	e := event{Action: "output", Test: "TestGhost", Output: scenariocontract.ClaimMarker + claimPayload("AppService.About", "happy", "e") + "\n"}
	b, _ := json.Marshal(e)
	mustError(t, string(b)+"\n"+validStream(), validOpts("p"))
}

func TestSplitPackageMarkerRejected(t *testing.T) {
	full := scenariocontract.InventoryMarker + `{"x":1}`
	mid := len(full) / 2
	stream := startEvent("p") +
		evPkg("p", "output", "", full[:mid]) + evPkg("p", "output", "", full[mid:]+"\n") + pkgEvent("pass")
	mustError(t, stream, validOpts("p"))
}

func TestBenchmarkRejected(t *testing.T) {
	stream := startEvent("p") + ev("bench", "BenchmarkX", "") + pkgEvent("pass")
	if reconcileOK(t, stream, validOpts("p")).Valid {
		t.Error("a benchmark event should invalidate (out of scope for this profile)")
	}
}

func TestMisshapenActionsInvalidate(t *testing.T) {
	// test-level start and package-level run are impossible shapes.
	testLevelStart := startEvent("p") + ev("start", "TestH", "") +
		tSeg("TestH", "pass", claimLine("TestH", "AppService.About", "happy")) + pkgEvent("pass")
	if reconcileOK(t, testLevelStart, validOpts("p")).Valid {
		t.Error("a test-level start should invalidate")
	}
	pkgLevelRun := startEvent("p") + ev("run", "", "") + pkgEvent("pass")
	if reconcileOK(t, pkgLevelRun, validOpts("p")).Valid {
		t.Error("a package-level run should invalidate")
	}
}

func TestCompletedLinesDoNotRetain(t *testing.T) {
	var b strings.Builder
	b.WriteString(startEvent("p") + runEvent("TestStream"))
	line := strings.Repeat("d", 130<<10) + "\n"
	for i := 0; i < 100; i++ { // ~13 MiB total, all completed lines
		b.WriteString(ev("output", "TestStream", line))
	}
	b.WriteString(ev("pass", "TestStream", "") + pkgEvent("pass"))
	reconcileOK(t, b.String(), validOpts("p")) // must not error (buffers released)
}

// --- retained protocol attacks ---

func TestInventoryMarkerRejected(t *testing.T) {
	mustError(t, startEvent("p")+runEvent("TestX")+out("TestX", scenariocontract.InventoryMarker+`{"whatever":1}`), baseOpts())
}

func TestMalformedClaimIsError(t *testing.T) {
	mustError(t, startEvent("p")+runEvent("TestX")+out("TestX", scenariocontract.ClaimMarker+`{"action":`), baseOpts())
}

func TestUnknownFieldIsError(t *testing.T) {
	mustError(t, startEvent("p")+runEvent("TestX")+out("TestX", scenariocontract.ClaimMarker+`{"action":"AppService.About","scenario":"happy","evidence":"e","x":1}`), baseOpts())
}

func TestUnknownActionOrScenarioIsError(t *testing.T) {
	mustError(t, startEvent("p")+runEvent("TestX")+out("TestX", scenariocontract.ClaimMarker+claimPayload("Nope.NotReal", "happy", "e")), baseOpts())
	mustError(t, startEvent("p")+runEvent("TestX")+out("TestX", scenariocontract.ClaimMarker+claimPayload("AppService.About", "conflict", "e")), baseOpts())
}

func TestDuplicateClaimIsError(t *testing.T) {
	mustError(t, startEvent("p")+runEvent("TestX")+claimLine("TestX", "AppService.About", "happy")+claimLine("TestX", "AppService.About", "happy"), baseOpts())
}

func TestTrailingContentAfterClaimIsError(t *testing.T) {
	mustError(t, startEvent("p")+runEvent("TestX")+out("TestX", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e")+" trailing-garbage"), baseOpts())
	mustError(t, startEvent("p")+runEvent("TestX")+out("TestX", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e")+claimPayload("AppService.Info", "happy", "e")), baseOpts())
}

func TestUnterminatedMarkerAtEOFIsError(t *testing.T) {
	mustError(t, startEvent("p")+runEvent("TestX")+ev("output", "TestX", scenariocontract.ClaimMarker+`{"action":`), baseOpts())
}

func TestPackageLevelMarkersRejected(t *testing.T) {
	mustError(t, startEvent("p")+evPkg("p", "output", "", scenariocontract.InventoryMarker+`{"x":1}`+"\n")+pkgEvent("pass"), validOpts("p"))
	mustError(t, startEvent("p")+evPkg("p", "output", "", scenariocontract.ClaimMarker+claimPayload("AppService.About", "happy", "e")+"\n")+pkgEvent("pass"), validOpts("p"))
}

func TestChunkedMarkerReassembled(t *testing.T) {
	full := scenariocontract.ClaimMarker + claimPayload("AppService.About", "happy", "chunked")
	mid := len(full) / 2
	stream := startEvent("p") + runEvent("TestChunk") +
		ev("output", "TestChunk", full[:mid]) + ev("output", "TestChunk", full[mid:]) + ev("output", "TestChunk", "\n") +
		ev("pass", "TestChunk", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if !coveredScenarios(rep, "AppService.About")["happy"] {
		t.Fatal("a claim split across output events was not reassembled and credited")
	}
}

// --- safe inputs / package completeness ---

func TestUnsafeDefaultsAreInvalid(t *testing.T) {
	rep := reconcileOK(t, validStream(), options{}) // nothing provided
	if rep.ProcessSucceeded || rep.StreamComplete || rep.Valid {
		t.Errorf("unsafe defaults must be invalid: process=%t stream=%t valid=%t", rep.ProcessSucceeded, rep.StreamComplete, rep.Valid)
	}
}

func TestMissingExpectedPackagesInvalid(t *testing.T) {
	if reconcileOK(t, validStream(), baseOpts()).Valid {
		t.Error("valid true without an expected package set")
	}
}

func TestExpectedPackageFailIsIncomplete(t *testing.T) {
	stream := pkgP("fail", tSeg("TestH", "pass", claimLine("TestH", "AppService.About", "happy")))
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.StreamComplete || rep.Valid {
		t.Error("an expected package that failed should be incomplete")
	}
	if len(rep.FailedPackages) != 1 || rep.FailedPackages[0] != "p" {
		t.Errorf("FailedPackages=%v want [p]", rep.FailedPackages)
	}
}

func TestNoTestPackageSkipIsComplete(t *testing.T) {
	stream := validStream() + startEvent("q") + pkgEventFor("q", "skip") // q: started, no tests, skip
	rep := reconcileOK(t, stream, validOpts("p", "q"))
	if !rep.StreamComplete || !rep.Valid {
		t.Errorf("a no-test package skip should be complete: streamComplete=%t valid=%t violations=%v", rep.StreamComplete, rep.Valid, rep.Violations)
	}
}

func TestUnexpectedPackageInvalid(t *testing.T) {
	stream := validStream() + startEvent("r") + pkgEventFor("r", "pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.StreamComplete || rep.Valid {
		t.Error("an unexpected package should invalidate the stream")
	}
}

func TestDuplicatePackageTerminalInvalid(t *testing.T) {
	stream := validStream() + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if rep.StreamComplete || rep.Valid || len(rep.Violations) == 0 {
		t.Error("a duplicate package terminal should invalidate the stream")
	}
}

func TestNonZeroTestExitInvalidates(t *testing.T) {
	opts := validOpts("p")
	opts.testExit = 42
	rep := reconcileOK(t, validStream(), opts)
	if rep.ProcessSucceeded || rep.Valid {
		t.Error("a nonzero producer exit must invalidate")
	}
}

func TestTruncatedStreamInvalid(t *testing.T) {
	rep := reconcileOK(t, validStream(), validOpts("p", "q")) // q never appears
	if rep.StreamComplete || len(rep.MissingPackages) != 1 || rep.MissingPackages[0] != "q" {
		t.Errorf("truncated stream: streamComplete=%t missing=%v", rep.StreamComplete, rep.MissingPackages)
	}
}

func TestOversizedProtocolLineIsError(t *testing.T) {
	mustError(t, startEvent("p")+runEvent("TestX")+ev("output", "TestX", strings.Repeat("x", maxProtocolLine+10)), baseOpts())
}

func TestGapsAndTotals(t *testing.T) {
	rep := reconcileOK(t, validStream(), validOpts("p"))
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

// --- command-level tests through run() ---

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runCLI(t *testing.T, stdin string, args ...string) int {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return run(args, strings.NewReader(stdin), &stdout, &stderr)
}

func TestRunValidWithFlags(t *testing.T) {
	dir := t.TempDir()
	events := writeFile(t, dir, "events.jsonl", validStream())
	expected := writeFile(t, dir, "pkgs.txt", "p\n")
	outPath := filepath.Join(dir, "rep.json")
	if code := runCLI(t, "", "-events", events, "-expected-packages", expected, "-test-exit", "0", "-out", outPath); code != 0 {
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
	if code := runCLI(t, validStream(), "-out", outPath); code != 1 {
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
	if code := runCLI(t, "", "-events", events, "-expected-packages", expected); code != 1 {
		t.Errorf("omitted -test-exit should be invalid: code=%d", code)
	}
	if code := runCLI(t, "", "-events", events, "-expected-packages", expected, "-test-exit", "0"); code != 0 {
		t.Errorf("explicit -test-exit 0 should be valid: code=%d", code)
	}
}

func TestRunBadExpectedFileReturns2(t *testing.T) {
	dir := t.TempDir()
	events := writeFile(t, dir, "events.jsonl", validStream())
	dup := writeFile(t, dir, "dup.txt", "p\np\n")
	if code := runCLI(t, "", "-events", events, "-expected-packages", dup, "-test-exit", "0"); code != 2 {
		t.Errorf("a duplicate expected-package file should return 2: code=%d", code)
	}
	empty := writeFile(t, dir, "empty.txt", "\n \n")
	if code := runCLI(t, "", "-events", events, "-expected-packages", empty, "-test-exit", "0"); code != 2 {
		t.Errorf("an empty expected-package file should return 2: code=%d", code)
	}
}

func TestRunMissingEventsFileReturns2(t *testing.T) {
	if code := runCLI(t, "", "-events", filepath.Join(t.TempDir(), "nope.jsonl")); code != 2 {
		t.Errorf("a missing events file should return 2: code=%d", code)
	}
}

func TestRunReportWriteFailureReturns2(t *testing.T) {
	dir := t.TempDir()
	events := writeFile(t, dir, "events.jsonl", validStream())
	expected := writeFile(t, dir, "pkgs.txt", "p\n")
	badOut := filepath.Join(dir, "nonexistent-dir", "rep.json") // parent does not exist
	if code := runCLI(t, "", "-events", events, "-expected-packages", expected, "-test-exit", "0", "-out", badOut); code != 2 {
		t.Errorf("an unwritable report path should return 2: code=%d", code)
	}
}

func TestRunProtocolErrorWritesReport(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "rep.json")
	bad := startEvent("p") + runEvent("TestX") + out("TestX", scenariocontract.InventoryMarker+`{"x":1}`)
	if code := runCLI(t, bad, "-out", outPath, "-test-exit", "0"); code != 2 {
		t.Fatalf("a protocol error should return 2")
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

func runFixture(t *testing.T, pkg, runArg string) []byte {
	t.Helper()
	if testing.Short() {
		t.Skip("skips the go test subprocess in -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out, err := exec.Command("go", "test", "-json", "-v", "-count=1", "-run", runArg, pkg).Output()
	if err != nil {
		t.Fatalf("fixture subprocess (%s %s) failed: %v\n%s", pkg, runArg, err, out)
	}
	return out
}

func firstPackage(t *testing.T, stream []byte) string {
	t.Helper()
	for _, ln := range strings.Split(string(stream), "\n") {
		if ln == "" {
			continue
		}
		var e event
		if json.Unmarshal([]byte(ln), &e) == nil && e.Package != "" {
			return e.Package
		}
	}
	t.Fatal("no package in fixture stream")
	return ""
}

func TestTopLevelClaimThroughGoTestJSON(t *testing.T) {
	rep, err := reconcile(bytes.NewReader(runFixture(t, "./testdata/slashsub", "^TestTopLevel$")), baseOpts())
	if err != nil {
		t.Fatalf("reconcile of real top-level stream: %v", err)
	}
	if !coveredScenarios(rep, "AppService.About")["happy"] {
		t.Fatal("a real top-level claim was not credited")
	}
}

func TestSubtestClaimRejectedThroughGoTestJSON(t *testing.T) {
	if _, err := reconcile(bytes.NewReader(runFixture(t, "./testdata/slashsub", "^TestSlash$")), baseOpts()); err == nil {
		t.Fatal("a real subtest claim was not rejected")
	}
}

// TestAttrThroughGoTestJSON: Go 1.27's real producer emits Action:"attr" for
// t.Attr under -v. The reconciler must model it, not reject the exact CI
// command's output as an unknown action.
func TestAttrThroughGoTestJSON(t *testing.T) {
	stream := runFixture(t, "./testdata/attrprobe", "^TestCodexAttr$")
	pkg := firstPackage(t, stream)
	rep, err := reconcile(bytes.NewReader(stream), validOpts(pkg))
	if err != nil {
		t.Fatalf("a real t.Attr stream was rejected: %v", err)
	}
	if !rep.Valid {
		t.Fatalf("a real t.Attr stream was not valid: violations=%v", rep.Violations)
	}
}

// TestAttrEventTolerated (synthetic): an attr event between run and the claim
// does not invalidate the stream or block credit.
func TestAttrEventTolerated(t *testing.T) {
	stream := startEvent("p") + runEvent("TestH") + ev("attr", "TestH", "") +
		claimLine("TestH", "AppService.About", "happy") + ev("pass", "TestH", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if !rep.Valid || !coveredScenarios(rep, "AppService.About")["happy"] {
		t.Errorf("attr should be tolerated: valid=%t violations=%v", rep.Valid, rep.Violations)
	}
}

// TestMultiLinePausedFragmentNotCredited: two logical lines in one paused
// output fragment — the claim on the second line must still be treated as
// paused (not credited), i.e. `spanned` is not cleared mid-fragment.
func TestMultiLinePausedFragmentNotCredited(t *testing.T) {
	twoLines := "first line\n" + scenariocontract.ClaimMarker + claimPayload("AppService.About", "happy", "e") + "\n"
	stream := startEvent("p") + runEvent("TestP") + ev("pause", "TestP", "") +
		ev("output", "TestP", twoLines) + // whole fragment received while paused
		ev("cont", "TestP", "") + ev("pass", "TestP", "") + pkgEvent("pass")
	rep := reconcileOK(t, stream, validOpts("p"))
	if coveredScenarios(rep, "AppService.About")["happy"] {
		t.Error("a claim on the second line of a paused fragment was credited")
	}
}
