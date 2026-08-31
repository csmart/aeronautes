// Command scenariocov reconciles war-room scenario coverage from a
// `go test -json` event stream. It credits a claim ONLY if the emitting
// top-level test ran, was running (not paused) when the claim line completed,
// reached a single terminal PASS, and belongs to an expected package that
// started and passed — so a skipped, failed, parallel-paused, or
// parent-cleanup-skipped test earns no credit.
//
// The coverage DENOMINATOR comes from internal/scenariocontract, imported
// directly into this separately executed process; it is never taken from the
// test stream. A marker in a non-output event, in package-level output, or from
// a subtest is rejected (including markers split across output fragments); a
// marker emitted while its test is paused is parsed but earns no credit.
//
// SCOPE: this reconciles the authentic records the CI producer profile emits —
// `go test -json -v -race -count=1 ./internal/...` (Go 1.27 test2json: start,
// run, pause, cont, attr, output, and terminals), no `-bench`. Benchmark
// activity is rejected as out of scope. It is not a hostile-input validator for
// a non-gating counter: it does not exhaustively reject every out-of-profile
// shape a real producer never emits, and a future producer action would need a
// one-line addition. The report is an honest-author progress signal, not
// authenticated proof of behavior: a test can bypass the harness API and print
// a claim. CI does not gate on zero gaps.
//
// Usage (CI): go test -json ... | tee events.jsonl ; then
//
//	scenariocov -events events.jsonl -test-exit "$code" \
//	  -expected-packages pkgs.txt -out report.json
//
// -test-exit and a non-empty -expected-packages are REQUIRED for valid:true.
// A stdin/default invocation is a visibly-incomplete local convenience.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/csmart/aeronautes/internal/scenariocontract"
)

const (
	schemaVersion   = 3
	maxProtocolLine = 1 << 20 // per logical line
	maxTotalPartial = 8 << 20 // all retained incomplete lines
	maxActiveBufs   = 4096    // concurrently retained per-key line buffers
	maxViolations   = 64      // recorded lifecycle-violation list
	maxEvents       = 50_000_000
	maxTests        = 2_000_000
	maxClaims       = 2_000_000
	maxEvidence     = 2048 // retained evidence bytes per claim
	maxUnexpected   = 64   // recorded unexpected-package list
	maxWitnesses    = 64   // reported tests/evidence per covered cell
)

var errProtocolLine = errors.New("protocol line exceeded the limit")

type event struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

type emittedClaim struct {
	testKey  string
	Action   string
	Scenario string
	Evidence string
	running  bool // the claim line completed without spanning a paused phase
}

type scenarioCredit struct {
	Scenario string   `json:"scenario"`
	Tests    []string `json:"tests"`
	Evidence []string `json:"evidence"`
}

type actionCoverage struct {
	Action   string           `json:"action"`
	Kind     string           `json:"kind"`
	Required []string         `json:"required"`
	Covered  []scenarioCredit `json:"covered"`
	Gaps     []string         `json:"gaps"`
}

// report keeps the distinct notions of "valid" separate: valid is the
// conjunction of process-succeeded, stream-complete, and contract-valid — it
// deliberately does NOT imply coverage-complete.
type report struct {
	SchemaVersion       int              `json:"schemaVersion"`
	Valid               bool             `json:"valid"`
	ProcessSucceeded    bool             `json:"processSucceeded"`
	StreamComplete      bool             `json:"streamComplete"`
	ContractValid       bool             `json:"contractValid"`
	CoverageComplete    bool             `json:"coverageComplete"`
	ProtocolError       string           `json:"protocolError,omitempty"`
	CommitSHA           string           `json:"commitSha"`
	GoVersion           string           `json:"goVersion"`
	SAIOConfigured      bool             `json:"saioConfigured"`
	ContractHash        string           `json:"contractHash"`
	TotalActions        int              `json:"totalActions"`
	TotalRequired       int              `json:"totalRequired"`
	TotalCovered        int              `json:"totalCovered"`
	ActionsWithGaps     int              `json:"actionsWithGaps"`
	MissingPackages     []string         `json:"missingPackages,omitempty"`
	FailedPackages      []string         `json:"failedPackages,omitempty"`
	UnexpectedPkgs      []string         `json:"unexpectedPackages,omitempty"`
	Violations          []string         `json:"violations,omitempty"`
	ViolationsTruncated int              `json:"violationsTruncated,omitempty"`
	NonJSONLines        int              `json:"nonJSONLines"`
	Actions             []actionCoverage `json:"actions"`
}

type options struct {
	commitSHA        string
	saioConfigured   bool
	testExit         int
	testExitProvided bool
	expectedPackages []string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is the testable entry point: it parses args, reconciles the stream, and
// returns the process exit code, writing diagnostics to stderr.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scenariocov", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "write the JSON report to this path")
	events := fs.String("events", "", "read the go test -json stream from this file (default: stdin)")
	testExit := fs.Int("test-exit", 0, "exit code of the go test process that produced the stream (required for valid:true)")
	expectedPkgs := fs.String("expected-packages", "", "file of expected package import paths, one per line (required for valid:true)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	opts := options{
		commitSHA:      os.Getenv("GITHUB_SHA"),
		saioConfigured: os.Getenv("AERONAUTES_SAIO_AUTH_URL") != "",
		testExit:       *testExit,
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "test-exit" {
			opts.testExitProvided = true // an implicit zero is not evidence
		}
	})

	src := stdin
	if *events != "" {
		f, err := os.Open(*events)
		if err != nil {
			fmt.Fprintf(stderr, "scenariocov: open events: %v\n", err)
			return 2
		}
		defer f.Close()
		src = f
	}
	if *expectedPkgs != "" {
		list, err := readExpected(*expectedPkgs)
		if err != nil {
			writeErrorReport(*out, opts, fmt.Errorf("expected packages: %w", err))
			fmt.Fprintf(stderr, "scenariocov: expected packages: %v\n", err)
			return 2
		}
		opts.expectedPackages = list
	}

	rep, err := reconcile(src, opts)
	if err != nil {
		// A protocol violation (malformed/forged/oversized stream). Write a
		// structured invalid report before returning so the failure class is
		// visible in the artifact, alongside the tee'd raw stream.
		writeErrorReport(*out, opts, err)
		fmt.Fprintf(stderr, "scenariocov: %v\n", err)
		return 2
	}

	data, _ := json.MarshalIndent(rep, "", "  ")
	if *out != "" {
		if werr := os.WriteFile(*out, data, 0o644); werr != nil {
			fmt.Fprintf(stderr, "scenariocov: write %s: %v\n", *out, werr)
			return 2
		}
	}
	fmt.Fprintf(stderr, "SCENARIO COVERAGE: %d/%d cells; %d/%d actions with gaps; valid=%t (process=%t stream=%t contract=%t) saioConfigured=%t\n",
		rep.TotalCovered, rep.TotalRequired, rep.ActionsWithGaps, rep.TotalActions,
		rep.Valid, rep.ProcessSucceeded, rep.StreamComplete, rep.ContractValid, rep.SAIOConfigured)

	if !rep.Valid {
		return 1
	}
	return 0
}

func writeErrorReport(outPath string, opts options, cause error) {
	if outPath == "" {
		return
	}
	rep := report{
		SchemaVersion:  schemaVersion,
		Valid:          false,
		ContractValid:  scenariocontract.Validate() == nil,
		ProtocolError:  cause.Error(),
		CommitSHA:      opts.commitSHA,
		GoVersion:      runtime.Version(),
		SAIOConfigured: opts.saioConfigured,
		ContractHash:   scenariocontract.Hash(),
	}
	data, _ := json.MarshalIndent(rep, "", "  ")
	_ = os.WriteFile(outPath, data, 0o644)
}

func hasMarker(s string) bool {
	return strings.Contains(s, scenariocontract.ClaimMarker) || strings.Contains(s, scenariocontract.InventoryMarker)
}

// --- reassembler ---------------------------------------------------------

// phase is a test's position in the test2json lifecycle.
type phase uint8

const (
	phaseIdle phase = iota
	phaseRunning
	phasePaused
	phaseTerminal
)

// stepPhase is the legal-transition table: it returns the next phase and
// whether `action` is legal from `cur`. Illegal transitions (duplicate run,
// cont-before-pause, terminal without run, ...) return ok=false.
func stepPhase(cur phase, action string) (phase, bool) {
	switch action {
	case "run":
		if cur == phaseIdle {
			return phaseRunning, true
		}
	case "pause":
		if cur == phaseRunning {
			return phasePaused, true
		}
	case "cont":
		if cur == phasePaused {
			return phaseRunning, true
		}
	case "pass", "fail", "skip":
		if cur == phaseRunning || cur == phasePaused {
			return phaseTerminal, true
		}
	case "output":
		if cur == phaseRunning || cur == phasePaused {
			return cur, true
		}
	}
	return cur, false
}

type completedLine struct {
	text    string
	spanned bool // the line touched a paused phase, so it is not "running" output
}

// lineAcc reassembles one key's output. It keeps spare capacity while a line
// is incomplete (linear growth) and right-sizes only a consumed remainder, and
// it marks a line that touched a paused phase so a claim authored across a
// pause is not credited as running.
type lineAcc struct {
	data    []byte
	spanned bool
}

func (a *lineAcc) add(frag string, paused bool) ([]completedLine, int, error) {
	if paused {
		a.spanned = true
	}
	a.data = append(a.data, frag...)
	var lines []completedLine
	start := 0
	for {
		rel := bytes.IndexByte(a.data[start:], '\n')
		if rel < 0 {
			break
		}
		nl := start + rel
		if nl-start > maxProtocolLine {
			return nil, len(a.data), errProtocolLine
		}
		lines = append(lines, completedLine{string(a.data[start:nl]), a.spanned})
		a.spanned = paused // remaining lines from a paused fragment are also paused
		start = nl + 1
	}
	rem := a.data[start:]
	if len(rem) > maxProtocolLine {
		return nil, len(a.data), errProtocolLine
	}
	switch {
	case len(rem) == 0:
		a.data = nil // release the backing array
	case start > 0:
		nb := make([]byte, len(rem)) // consumed a line: right-size the remainder
		copy(nb, rem)
		a.data = nb
	}
	return lines, len(a.data), nil
}

// --- parser --------------------------------------------------------------

type testState struct {
	phase    phase
	terminal string
	violated bool
}

type pkgState struct {
	started  bool
	terminal string
	open     int // tests started, not yet terminated
	failed   int
	sawTest  bool
}

type parser struct {
	opts         options
	expected     map[string]bool
	machineValid bool

	tests    map[string]*testState
	pkgs     map[string]*pkgState
	accs     map[string]*lineAcc
	retained int

	seen       map[string]bool
	claims     []emittedClaim
	unexpected map[string]bool
	violations []string
	vTrunc     int
	nonJSON    int
	events     int
}

func newParser(opts options) *parser {
	expected := map[string]bool{}
	for _, p := range opts.expectedPackages {
		expected[p] = true
	}
	return &parser{
		opts: opts, expected: expected, machineValid: len(expected) > 0,
		tests: map[string]*testState{}, pkgs: map[string]*pkgState{},
		accs: map[string]*lineAcc{}, seen: map[string]bool{}, unexpected: map[string]bool{},
	}
}

func (p *parser) addViolation(v string) {
	if len(p.violations) < maxViolations {
		p.violations = append(p.violations, v)
	} else {
		p.vTrunc++
	}
}

func (p *parser) pkg(name string) *pkgState {
	s := p.pkgs[name]
	if s == nil {
		s = &pkgState{}
		p.pkgs[name] = s
	}
	return s
}

// step validates one decoded event's record shape, then drives the lifecycle
// automaton. It returns an error only for a broken/forged stream; illegal but
// bounded shapes are recorded as violations that invalidate the report.
func (p *parser) step(e event) error {
	p.events++
	if p.events > maxEvents {
		return fmt.Errorf("stream exceeded %d events", maxEvents)
	}
	// A reserved marker may appear only in a test's output, and is scanned via
	// reassembly there; a non-output event must not carry one.
	if e.Action != "output" && hasMarker(e.Output) {
		return fmt.Errorf("reserved marker in a %q event: markers come only from running top-level test output", e.Action)
	}
	// Semantic record shape: every real test2json event names a package.
	if e.Package == "" {
		if hasMarker(e.Output) {
			return fmt.Errorf("reserved marker in an event with no package attribution")
		}
		if p.machineValid {
			p.addViolation("record without a package")
		}
		return nil
	}
	if p.machineValid && !p.expected[e.Package] {
		if len(p.unexpected) < maxUnexpected {
			p.unexpected[e.Package] = true
		}
	}
	pk := p.pkg(e.Package)

	switch e.Action {
	case "start":
		if e.Test != "" {
			p.addViolation("test-level start: " + e.Package + "/" + e.Test)
			return nil
		}
		if pk.started {
			p.addViolation("duplicate package start: " + e.Package)
		}
		pk.started = true
		return nil
	case "bench":
		p.addViolation("benchmark event is out of scope for this CI profile: " + e.Package)
		return nil
	case "attr":
		// Go 1.27's test2json emits `attr` for t.Attr under -v: a test-level
		// metadata event that changes no phase and earns no credit. Modelling
		// it keeps the exact documented producer profile from being rejected.
		if e.Test == "" {
			p.addViolation("package-level attr: " + e.Package)
			return nil
		}
		key := e.Package + "\t" + e.Test
		pk.sawTest = true
		ts, err := p.test(key)
		if err != nil {
			return err
		}
		if ts.phase == phaseIdle || ts.phase == phaseTerminal {
			ts.violated = true
			p.addViolation("attr outside a running test: " + key)
		}
		return nil
	case "run", "pause", "cont":
		if e.Test == "" {
			p.addViolation("package-level " + e.Action + ": " + e.Package)
			return nil
		}
		return p.testEvent(e, pk)
	case "pass", "fail", "skip":
		if e.Test == "" {
			return p.packageTerminal(e, pk)
		}
		return p.testEvent(e, pk)
	case "output":
		return p.output(e, pk)
	default:
		if p.machineValid {
			p.addViolation("unknown action " + strconv.Quote(e.Action))
		}
		return nil
	}
}

func (p *parser) test(key string) (*testState, error) {
	ts := p.tests[key]
	if ts == nil {
		if len(p.tests) >= maxTests {
			return nil, fmt.Errorf("stream exceeded %d tests", maxTests)
		}
		ts = &testState{}
		p.tests[key] = ts
	}
	return ts, nil
}

func (p *parser) testEvent(e event, pk *pkgState) error {
	key := e.Package + "\t" + e.Test
	pk.sawTest = true
	if !pk.started {
		p.addViolation("test event before package start: " + key)
	}
	if pk.terminal != "" {
		p.addViolation("test event after package terminal: " + key)
	}
	ts, err := p.test(key)
	if err != nil {
		return err
	}
	next, ok := stepPhase(ts.phase, e.Action)
	if !ok {
		ts.violated = true
		p.addViolation("illegal " + e.Action + " for " + key)
		return nil
	}
	switch e.Action {
	case "run":
		pk.open++
	case "pass", "fail", "skip":
		pk.open--
		ts.terminal = e.Action
		if e.Action == "fail" {
			pk.failed++
		}
		if err := p.flush(key); err != nil {
			return err
		}
	}
	ts.phase = next
	return nil
}

func (p *parser) packageTerminal(e event, pk *pkgState) error {
	if !pk.started {
		p.addViolation("package terminal without start: " + e.Package)
	}
	if pk.terminal != "" {
		p.addViolation("duplicate package terminal: " + e.Package)
	}
	pk.terminal = e.Action
	if pk.open > 0 {
		p.addViolation("package terminal with unfinished tests: " + e.Package)
	}
	if e.Action == "pass" && pk.failed > 0 {
		p.addViolation("package pass with a failed test: " + e.Package)
	}
	return p.flush(e.Package + "\t")
}

func (p *parser) output(e event, pk *pkgState) error {
	key := e.Package + "\t" + e.Test
	if e.Test == "" {
		// Package-level output: accumulate to detect a marker split across
		// fragments; a marker on a completed package-level line is rejected.
		if pk.terminal != "" {
			p.addViolation("package output after package terminal: " + e.Package)
		}
		return p.accumulate(key, e.Output, false, true)
	}
	pk.sawTest = true
	if !pk.started {
		p.addViolation("test output before package start: " + e.Package)
	}
	ts, err := p.test(key)
	if err != nil {
		return err
	}
	if ts.phase == phaseIdle {
		ts.violated = true
		p.addViolation("test output before run: " + key)
	}
	if ts.phase == phaseTerminal {
		ts.violated = true
		p.addViolation("test output after terminal: " + key)
		return nil
	}
	if pk.terminal != "" {
		p.addViolation("test output after package terminal: " + e.Package)
	}
	return p.accumulate(key, e.Output, ts.phase == phasePaused, false)
}

func (p *parser) accumulate(key, output string, paused, packageLevel bool) error {
	acc := p.accs[key]
	if acc == nil {
		if len(p.accs) >= maxActiveBufs {
			return fmt.Errorf("too many active per-key buffers (>%d): truncated or garbage stream?", maxActiveBufs)
		}
		acc = &lineAcc{}
		p.accs[key] = acc
	}
	p.retained -= len(acc.data)
	lines, retained, err := acc.add(output, paused)
	if err != nil {
		return fmt.Errorf("%w for %q", err, key)
	}
	p.retained += retained
	if retained == 0 {
		delete(p.accs, key)
	}
	if p.retained > maxTotalPartial {
		return fmt.Errorf("retained partial output exceeded %d bytes: truncated or garbage stream?", maxTotalPartial)
	}
	for _, ln := range lines {
		if err := p.processLine(key, ln, packageLevel); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) processLine(key string, ln completedLine, packageLevel bool) error {
	if strings.Contains(ln.text, scenariocontract.InventoryMarker) {
		return fmt.Errorf("inventory marker in stream from %q: the denominator comes from the contract", key)
	}
	idx := strings.Index(ln.text, scenariocontract.ClaimMarker)
	if idx < 0 {
		return nil
	}
	if packageLevel {
		return fmt.Errorf("claim marker in package-level output (%q): unattributable to a test", key)
	}
	_, testName, _ := strings.Cut(key, "\t")
	if strings.Contains(testName, "/") {
		return fmt.Errorf("claim from subtest %q: claims must be emitted from a top-level test", key)
	}
	payload := strings.TrimSpace(ln.text[idx+len(scenariocontract.ClaimMarker):])
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.DisallowUnknownFields()
	var c scenariocontract.Claim
	if err := dec.Decode(&c); err != nil {
		return fmt.Errorf("malformed claim from %s: %v (%q)", key, err, payload)
	}
	var tail json.RawMessage
	if err := dec.Decode(&tail); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing content after claim from %s: %q", key, payload)
	}
	if !scenariocontract.Requires(c.Action, c.Scenario) {
		return fmt.Errorf("claim from %s names an unknown or unrequired action/scenario %q/%q", key, c.Action, c.Scenario)
	}
	dk := key + "\x00" + c.Action + "\x00" + c.Scenario
	if p.seen[dk] {
		return fmt.Errorf("duplicate claim from %s for %s/%s", key, c.Action, c.Scenario)
	}
	p.seen[dk] = true
	if len(p.claims) >= maxClaims {
		return fmt.Errorf("stream exceeded %d claims", maxClaims)
	}
	evidence := c.Evidence
	if len(evidence) > maxEvidence {
		evidence = evidence[:maxEvidence]
	}
	p.claims = append(p.claims, emittedClaim{testKey: key, Action: c.Action, Scenario: c.Scenario, Evidence: evidence, running: !ln.spanned})
	return nil
}

func (p *parser) flush(key string) error {
	acc := p.accs[key]
	if acc == nil {
		return nil
	}
	if hasMarker(string(acc.data)) {
		return fmt.Errorf("unterminated protocol marker in residual output from %q", key)
	}
	p.retained -= len(acc.data)
	delete(p.accs, key)
	return nil
}

// finalize flushes residual buffers and builds the report.
func (p *parser) finalize() (report, error) {
	for key := range p.accs {
		if err := p.flush(key); err != nil {
			return report{}, err
		}
	}

	contract := scenariocontract.All()
	contractValid := scenariocontract.Validate() == nil && len(contract) > 0

	// Credit a claim only from a cleanly-run, running-phase, cleanly-passed
	// top-level test whose expected package started and passed.
	credited := map[string]map[string][]emittedClaim{}
	for _, ec := range p.claims {
		ts := p.tests[ec.testKey]
		if ts == nil || ts.violated || ts.terminal != "pass" || !ec.running {
			continue
		}
		if p.machineValid {
			pkgName, _, _ := strings.Cut(ec.testKey, "\t")
			if !p.expected[pkgName] {
				continue
			}
			if pk := p.pkgs[pkgName]; pk == nil || !pk.started || pk.terminal != "pass" {
				continue
			}
		}
		if credited[ec.Action] == nil {
			credited[ec.Action] = map[string][]emittedClaim{}
		}
		credited[ec.Action][ec.Scenario] = append(credited[ec.Action][ec.Scenario], ec)
	}

	var missing, failed, unexpectedList []string
	if p.machineValid {
		for name := range p.expected {
			pk := p.pkgs[name]
			switch {
			case pk == nil || pk.terminal == "":
				missing = append(missing, name)
			case pk.terminal == "pass":
			case pk.terminal == "skip" && !pk.sawTest:
			default:
				failed = append(failed, name)
			}
		}
		for name := range p.unexpected {
			unexpectedList = append(unexpectedList, name)
		}
		sort.Strings(missing)
		sort.Strings(failed)
		sort.Strings(unexpectedList)
	}
	streamComplete := p.machineValid &&
		len(missing) == 0 && len(failed) == 0 && len(unexpectedList) == 0 && len(p.violations) == 0 && p.vTrunc == 0

	rep := report{
		SchemaVersion:       schemaVersion,
		ProcessSucceeded:    p.opts.testExitProvided && p.opts.testExit == 0,
		StreamComplete:      streamComplete,
		ContractValid:       contractValid,
		CommitSHA:           p.opts.commitSHA,
		GoVersion:           runtime.Version(),
		SAIOConfigured:      p.opts.saioConfigured,
		ContractHash:        scenariocontract.Hash(),
		TotalActions:        len(contract),
		MissingPackages:     missing,
		FailedPackages:      failed,
		UnexpectedPkgs:      unexpectedList,
		Violations:          p.violations,
		ViolationsTruncated: p.vTrunc,
		NonJSONLines:        p.nonJSON,
	}

	for _, action := range sortedKeys(contract) {
		a := contract[action]
		required := a.Required()
		rep.TotalRequired += len(required)
		have := credited[action]
		var covered []scenarioCredit
		var gaps []string
		for _, scenario := range required {
			ecs := have[scenario]
			if len(ecs) == 0 {
				gaps = append(gaps, scenario)
				continue
			}
			sortByTest(ecs)
			cr := scenarioCredit{Scenario: scenario}
			for _, ec := range ecs {
				if len(cr.Tests) >= maxWitnesses { // bound reported witnesses per cell
					break
				}
				cr.Tests = append(cr.Tests, ec.testKey)
				cr.Evidence = append(cr.Evidence, ec.Evidence)
			}
			covered = append(covered, cr)
			rep.TotalCovered++
		}
		if len(gaps) > 0 {
			rep.ActionsWithGaps++
		}
		rep.Actions = append(rep.Actions, actionCoverage{
			Action: action, Kind: string(a.Kind), Required: required, Covered: covered, Gaps: gaps,
		})
	}

	rep.CoverageComplete = rep.ActionsWithGaps == 0
	rep.Valid = rep.ProcessSucceeded && rep.StreamComplete && rep.ContractValid
	return rep, nil
}

// reconcile parses a test2json stream into the coverage report. It returns an
// error for a broken/forged stream (a malformed, trailing-content, subtest, or
// duplicate claim; a misplaced or unattributable marker; an over-limit line or
// budget). A failed run, a truncated stream, an unexpected package, or a
// lifecycle violation is reported as valid:false, not an error.
func reconcile(r io.Reader, opts options) (report, error) {
	p := newParser(opts)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxProtocolLine+1024)
	for sc.Scan() {
		var e event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			p.nonJSON++
			if p.machineValid {
				p.addViolation("non-JSON line in stream")
			}
			continue
		}
		if err := p.step(e); err != nil {
			return report{}, err
		}
	}
	if err := sc.Err(); err != nil {
		if err == bufio.ErrTooLong {
			return report{}, fmt.Errorf("a stream line exceeded the protocol limit")
		}
		return report{}, err
	}
	return p.finalize()
}

func sortedKeys(m map[string]scenariocontract.Action) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortByTest(ecs []emittedClaim) {
	sort.Slice(ecs, func(i, j int) bool { return ecs[i].testKey < ecs[j].testKey })
}

func readExpected(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lines []string
	seen := map[string]bool{}
	for _, ln := range strings.Split(string(data), "\n") {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		if seen[s] {
			return nil, fmt.Errorf("duplicate expected package %q", s)
		}
		seen[s] = true
		lines = append(lines, s)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("no expected packages listed in %s", path)
	}
	return lines, nil
}
