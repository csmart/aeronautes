// Command scenariocov reconciles war-room scenario coverage from a
// `go test -json` event stream. It credits a claim ONLY if the emitting
// top-level test ran and reached a single terminal PASS, that test belongs to
// an expected package, and that package started and passed — so a skipped,
// failed, parallel-paused, or parent-cleanup-skipped test earns no credit.
//
// The coverage DENOMINATOR comes from internal/scenariocontract, imported
// directly into this separately executed process; it is never taken from the
// test stream. A reserved marker anywhere but a running top-level test's own
// output is rejected.
//
// The report is an honest-author progress signal, not authenticated proof of
// application behavior: a test can bypass the harness API and print a claim.
// CI does not gate on zero gaps.
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
	schemaVersion    = 3
	maxProtocolLine  = 1 << 20 // per logical line
	maxTotalPartial  = 8 << 20 // all retained incomplete lines
	maxActiveBuffers = 4096    // concurrently retained per-key buffers
	maxViolations    = 64      // cap the recorded lifecycle-violation list
)

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
	SchemaVersion    int              `json:"schemaVersion"`
	Valid            bool             `json:"valid"`
	ProcessSucceeded bool             `json:"processSucceeded"`
	StreamComplete   bool             `json:"streamComplete"`
	ContractValid    bool             `json:"contractValid"`
	CoverageComplete bool             `json:"coverageComplete"`
	ProtocolError    string           `json:"protocolError,omitempty"`
	CommitSHA        string           `json:"commitSha"`
	GoVersion        string           `json:"goVersion"`
	SAIOConfigured   bool             `json:"saioConfigured"`
	ContractHash     string           `json:"contractHash"`
	TotalActions     int              `json:"totalActions"`
	TotalRequired    int              `json:"totalRequired"`
	TotalCovered     int              `json:"totalCovered"`
	ActionsWithGaps  int              `json:"actionsWithGaps"`
	MissingPackages  []string         `json:"missingPackages,omitempty"`
	FailedPackages   []string         `json:"failedPackages,omitempty"`
	UnexpectedPkgs   []string         `json:"unexpectedPackages,omitempty"`
	Violations       []string         `json:"violations,omitempty"`
	NonJSONLines     int              `json:"nonJSONLines"`
	Actions          []actionCoverage `json:"actions"`
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
	// An implicit zero exit is not evidence: only a value the caller actually
	// supplied counts.
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "test-exit" {
			opts.testExitProvided = true
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

// writeErrorReport emits a minimal schema-3 invalid report for a protocol
// failure, so a broken stream still leaves a machine-readable artifact.
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

// testState / pkgState drive an explicit event automaton over the test2json
// grammar: a test must run before it outputs or terminates and terminate
// exactly once; a package must start, terminate once, have every started test
// terminate first, and not pass with a failed test. Illegal transitions are
// recorded as violations, which invalidate the stream.
type testState struct {
	started  bool
	terminal string
	violated bool
}

type pkgState struct {
	started   bool
	terminal  string
	openTests int // started, not yet terminated
	failed    int // tests that terminally failed
	sawTest   bool
}

// reconcile parses a test2json stream into the coverage report. It returns an
// error for a broken/forged stream (a malformed, trailing-content, subtest, or
// duplicate claim; a misplaced reserved marker; an unterminated marker; an
// over-limit line or budget). A failed run, a truncated stream, an unexpected
// package, or a lifecycle violation is reported as valid:false, not an error.
func reconcile(r io.Reader, opts options) (report, error) {
	expected := map[string]bool{}
	for _, p := range opts.expectedPackages {
		expected[p] = true
	}
	machineValid := len(expected) > 0

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxProtocolLine+1024)

	tests := map[string]*testState{}
	pkgs := map[string]*pkgState{}
	partial := map[string][]byte{}
	totalPartial := 0
	seen := map[string]bool{}
	unexpected := map[string]bool{}
	var claims []emittedClaim
	var violations []string
	nonJSON := 0

	pkgGet := func(pkg string) *pkgState {
		p := pkgs[pkg]
		if p == nil {
			p = &pkgState{}
			pkgs[pkg] = p
		}
		return p
	}
	testGet := func(key string) *testState {
		ts := tests[key]
		if ts == nil {
			ts = &testState{}
			tests[key] = ts
		}
		return ts
	}
	addViolation := func(v string) {
		if len(violations) < maxViolations {
			violations = append(violations, v)
		}
	}

	// processLine scans one COMPLETE test-output line for a claim marker. It is
	// only ever called for a running top-level test's output; a slash-bearing
	// (subtest) claimant, an inventory marker, or a malformed/duplicate claim is
	// rejected.
	processLine := func(key, line string) error {
		if strings.Contains(line, scenariocontract.InventoryMarker) {
			return fmt.Errorf("inventory marker in stream from %q: the denominator comes from the contract", key)
		}
		idx := strings.Index(line, scenariocontract.ClaimMarker)
		if idx < 0 {
			return nil
		}
		_, testName, _ := strings.Cut(key, "\t")
		if strings.Contains(testName, "/") {
			return fmt.Errorf("claim from subtest %q: claims must be emitted from a top-level test", key)
		}
		payload := strings.TrimSpace(line[idx+len(scenariocontract.ClaimMarker):])
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
		if seen[dk] {
			return fmt.Errorf("duplicate claim from %s for %s/%s", key, c.Action, c.Scenario)
		}
		seen[dk] = true
		claims = append(claims, emittedClaim{testKey: key, Action: c.Action, Scenario: c.Scenario, Evidence: c.Evidence})
		return nil
	}

	// flush rejects a residual partial line that still carries a marker and
	// releases the buffer.
	flush := func(key string) error {
		buf, ok := partial[key]
		if !ok {
			return nil
		}
		if hasMarker(string(buf)) {
			return fmt.Errorf("unterminated protocol marker in residual output from %q", key)
		}
		totalPartial -= len(buf)
		delete(partial, key)
		return nil
	}

	// appendFragment adds an output fragment and processes completed lines. It
	// keeps the buffer's spare capacity while a line is still incomplete (so a
	// long line arriving in ~1 KiB test2json fragments reassembles in linear
	// time, not quadratic), and right-sizes only the small remainder once one
	// or more lines have been consumed (releasing the completed lines' memory).
	appendFragment := func(key, frag string) error {
		buf := partial[key]
		if buf == nil && len(partial) >= maxActiveBuffers {
			return fmt.Errorf("too many active per-test buffers (>%d): truncated or garbage stream?", maxActiveBuffers)
		}
		totalPartial -= len(buf)
		buf = append(buf, frag...)
		start := 0
		for {
			rel := bytes.IndexByte(buf[start:], '\n')
			if rel < 0 {
				break
			}
			nl := start + rel
			if nl-start > maxProtocolLine {
				return fmt.Errorf("protocol line exceeded %d bytes for %q", maxProtocolLine, key)
			}
			if err := processLine(key, string(buf[start:nl])); err != nil {
				return err
			}
			start = nl + 1
		}
		rem := buf[start:]
		if len(rem) > maxProtocolLine {
			return fmt.Errorf("protocol line exceeded %d bytes for %q", maxProtocolLine, key)
		}
		switch {
		case len(rem) == 0:
			delete(partial, key) // release the backing array
		case start > 0:
			nb := make([]byte, len(rem)) // consumed a line: right-size the small remainder
			copy(nb, rem)
			partial[key] = nb
			totalPartial += len(nb)
		default:
			partial[key] = buf // still incomplete: keep spare capacity (linear growth)
			totalPartial += len(buf)
		}
		if totalPartial > maxTotalPartial {
			return fmt.Errorf("retained partial output exceeded %d bytes: truncated or garbage stream?", maxTotalPartial)
		}
		return nil
	}

	for sc.Scan() {
		var e event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			nonJSON++
			if machineValid {
				addViolation("non-JSON line in stream")
			}
			continue
		}
		if machineValid && e.Package != "" && !expected[e.Package] {
			unexpected[e.Package] = true
		}
		// A reserved marker may appear only in a running top-level test's own
		// output; anywhere else (a non-output action, or package-level output)
		// it is misplaced and rejected, so it cannot be hidden by changing the
		// action or clearing attribution.
		if (e.Action != "output" || e.Test == "") && hasMarker(e.Output) {
			return report{}, fmt.Errorf("reserved marker in a %q event (test=%q): markers come only from top-level test output", e.Action, e.Test)
		}
		if e.Package == "" {
			continue
		}
		p := pkgGet(e.Package)
		key := e.Package + "\t" + e.Test

		switch e.Action {
		case "start":
			if e.Test == "" {
				if p.started {
					addViolation("duplicate package start: " + e.Package)
				}
				p.started = true
			}
		case "run":
			if e.Test != "" {
				p.sawTest = true
				if !p.started {
					addViolation("test run before package start: " + key)
				}
				if p.terminal != "" {
					addViolation("test run after package terminal: " + key)
				}
				ts := testGet(key)
				if ts.terminal != "" {
					ts.violated = true
					addViolation("run after test terminal: " + key)
				} else if !ts.started {
					ts.started = true
					p.openTests++
				}
			}
		case "pause", "cont":
			if e.Test != "" {
				ts := testGet(key)
				if !ts.started || ts.terminal != "" {
					ts.violated = true
					addViolation(e.Action + " outside a running test: " + key)
				}
			}
		case "pass", "fail", "skip":
			if e.Test != "" {
				p.sawTest = true
				if p.terminal != "" {
					addViolation("test terminal after package terminal: " + key)
				}
				ts := testGet(key)
				if !ts.started {
					ts.violated = true
					addViolation("test terminal without run: " + key)
				}
				if ts.terminal != "" {
					ts.violated = true
					addViolation("duplicate test terminal: " + key)
				} else if ts.started {
					p.openTests--
				}
				ts.terminal = e.Action
				if e.Action == "fail" {
					p.failed++
				}
				if err := flush(key); err != nil {
					return report{}, err
				}
			} else {
				if !p.started {
					addViolation("package terminal without start: " + e.Package)
				}
				if p.terminal != "" {
					addViolation("duplicate package terminal: " + e.Package)
				}
				p.terminal = e.Action
				if p.openTests > 0 {
					addViolation("package terminal with unfinished tests: " + e.Package)
				}
				if e.Action == "pass" && p.failed > 0 {
					addViolation("package pass with a failed test: " + e.Package)
				}
				if err := flush(e.Package + "\t"); err != nil {
					return report{}, err
				}
			}
		case "output":
			if e.Test != "" {
				p.sawTest = true
				if !p.started {
					addViolation("test output before package start: " + e.Package)
				}
				ts := testGet(key)
				if !ts.started {
					ts.violated = true
					addViolation("test output before run: " + key)
				}
				if ts.terminal != "" {
					ts.violated = true
					addViolation("output after test terminal: " + key)
					continue
				}
				if p.terminal != "" {
					addViolation("test output after package terminal: " + e.Package)
				}
				if err := appendFragment(key, e.Output); err != nil {
					return report{}, err
				}
			} else if p.terminal != "" {
				addViolation("package output after package terminal: " + e.Package)
			}
		case "bench":
			// benchmarks carry no scenario coverage; accepted.
		default:
			if machineValid {
				addViolation("unknown action " + strconv.Quote(e.Action))
			}
		}
	}
	if err := sc.Err(); err != nil {
		if err == bufio.ErrTooLong {
			return report{}, fmt.Errorf("a stream line exceeded the protocol limit")
		}
		return report{}, err
	}
	for key := range partial {
		if err := flush(key); err != nil {
			return report{}, err
		}
	}

	contract := scenariocontract.All()
	contractValid := scenariocontract.Validate() == nil && len(contract) > 0

	// Credit a claim only from a cleanly-run, cleanly-passed top-level test
	// whose expected package started and passed.
	credited := map[string]map[string][]emittedClaim{}
	for _, ec := range claims {
		ts := tests[ec.testKey]
		if ts == nil || ts.violated || !ts.started || ts.terminal != "pass" {
			continue
		}
		if machineValid {
			pkg, _, _ := strings.Cut(ec.testKey, "\t")
			if !expected[pkg] {
				continue
			}
			if p := pkgs[pkg]; p == nil || !p.started || p.terminal != "pass" {
				continue
			}
		}
		if credited[ec.Action] == nil {
			credited[ec.Action] = map[string][]emittedClaim{}
		}
		credited[ec.Action][ec.Scenario] = append(credited[ec.Action][ec.Scenario], ec)
	}

	// Package completeness: every expected package must pass, or be a genuine
	// no-test skip (a package-level skip with no test-level events).
	var missing, failed, unexpectedList []string
	if machineValid {
		for pkg := range expected {
			p := pkgs[pkg]
			switch {
			case p == nil || p.terminal == "":
				missing = append(missing, pkg)
			case p.terminal == "pass":
			case p.terminal == "skip" && !p.sawTest:
			default:
				failed = append(failed, pkg)
			}
		}
		for pkg := range unexpected {
			unexpectedList = append(unexpectedList, pkg)
		}
		sort.Strings(missing)
		sort.Strings(failed)
		sort.Strings(unexpectedList)
	}
	streamComplete := machineValid &&
		len(missing) == 0 && len(failed) == 0 && len(unexpectedList) == 0 && len(violations) == 0

	rep := report{
		SchemaVersion:    schemaVersion,
		ProcessSucceeded: opts.testExitProvided && opts.testExit == 0,
		StreamComplete:   streamComplete,
		ContractValid:    contractValid,
		CommitSHA:        opts.commitSHA,
		GoVersion:        runtime.Version(),
		SAIOConfigured:   opts.saioConfigured,
		ContractHash:     scenariocontract.Hash(),
		TotalActions:     len(contract),
		MissingPackages:  missing,
		FailedPackages:   failed,
		UnexpectedPkgs:   unexpectedList,
		Violations:       violations,
		NonJSONLines:     nonJSON,
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

// readExpected reads the expected package list and validates it is non-empty
// and free of duplicates.
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
