// Command scenariocov reconciles war-room scenario coverage from a
// `go test -json` event stream. It credits a claim ONLY if the emitting
// top-level test reached exactly one terminal PASS, that test belongs to an
// expected package, and that package passed — so a skipped, failed,
// parallel-paused, or parent-cleanup-skipped test earns no credit.
//
// The coverage DENOMINATOR comes from internal/scenariocontract, imported
// directly into this separately executed process; it is never taken from the
// test stream, and any protocol marker in the stream that is not a claim from
// a running top-level test is rejected.
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

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

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

// testState / pkgState track the legal one-terminal lifecycle so the parser
// can reject contradictory or post-terminal events.
type testState struct {
	terminal  string
	terminals int
	violated  bool
}

type pkgState struct {
	terminal  string
	terminals int
	sawTest   bool
}

// reconcile parses a test2json stream and builds the coverage report. It
// returns an error for a broken/forged stream (a malformed, trailing-content,
// subtest, or duplicate claim; a protocol marker that is not an attributable
// claim; an unterminated marker; an over-limit line or budget). A failed run,
// a truncated stream, an unexpected package, or a lifecycle violation is
// reported as valid:false, not an error.
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

	// processLine scans one COMPLETE line for protocol markers. A claim is
	// accepted only from a top-level test (its key's test name has no slash);
	// a marker in package-level output (empty test name) is unattributable and
	// rejected, as is an inventory marker anywhere.
	processLine := func(key, line string) error {
		pkg, testName, _ := strings.Cut(key, "\t")
		if strings.Contains(line, scenariocontract.InventoryMarker) {
			return fmt.Errorf("inventory marker in stream from %q: the denominator comes from the contract", key)
		}
		idx := strings.Index(line, scenariocontract.ClaimMarker)
		if idx < 0 {
			return nil
		}
		if testName == "" {
			return fmt.Errorf("claim marker in package-level output (%s): unattributable to a test", pkg)
		}
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

	// flush rejects a residual partial line that still carries a marker (an
	// unterminated/corrupt claim) and releases the buffer.
	flush := func(key string) error {
		buf, ok := partial[key]
		if !ok {
			return nil
		}
		if bytes.Contains(buf, []byte(scenariocontract.ClaimMarker)) || bytes.Contains(buf, []byte(scenariocontract.InventoryMarker)) {
			return fmt.Errorf("unterminated protocol marker in residual output from %q", key)
		}
		totalPartial -= len(buf)
		delete(partial, key)
		return nil
	}

	// appendFragment adds an output fragment, checks EACH completed logical
	// line's length BEFORE processing it, compacts into a right-sized buffer
	// (releasing the old backing array), and enforces per-line, aggregate, and
	// active-buffer bounds.
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
		if len(rem) == 0 {
			delete(partial, key) // release the backing array
		} else {
			nb := make([]byte, len(rem)) // right-size so a large line's array is freed
			copy(nb, rem)
			partial[key] = nb
			totalPartial += len(nb)
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
			continue
		}
		// Ownership: in machine-valid mode, ANY event naming a package outside
		// the expected set is unexpected activity.
		if machineValid && e.Package != "" && !expected[e.Package] {
			unexpected[e.Package] = true
		}
		switch e.Action {
		case "run", "pause", "cont":
			if e.Test != "" {
				p := pkgGet(e.Package)
				p.sawTest = true // any test-level event, incl. run
				if p.terminal != "" {
					addViolation("test event after package terminal: " + e.Package + "/" + e.Test)
				}
			}
		case "pass", "fail", "skip":
			if e.Test != "" {
				key := e.Package + "\t" + e.Test
				p := pkgGet(e.Package)
				p.sawTest = true
				if p.terminal != "" {
					addViolation("test terminal after package terminal: " + key)
				}
				ts := testGet(key)
				ts.terminals++
				if ts.terminals > 1 {
					ts.violated = true
					addViolation("duplicate/conflicting test terminal: " + key)
				}
				ts.terminal = e.Action
				if err := flush(key); err != nil {
					return report{}, err
				}
			} else if e.Package != "" {
				p := pkgGet(e.Package)
				p.terminals++
				if p.terminals > 1 {
					addViolation("duplicate package terminal: " + e.Package)
				}
				p.terminal = e.Action
				if err := flush(e.Package + "\t"); err != nil {
					return report{}, err
				}
			}
		case "output":
			if e.Test != "" {
				key := e.Package + "\t" + e.Test
				p := pkgGet(e.Package)
				p.sawTest = true
				ts := testGet(key)
				if ts.terminal != "" {
					ts.violated = true
					addViolation("output after test terminal: " + key)
					continue // do not process post-terminal output
				}
				if p.terminal != "" {
					addViolation("test output after package terminal: " + e.Package)
				}
				if err := appendFragment(key, e.Output); err != nil {
					return report{}, err
				}
			} else if e.Package != "" {
				if err := appendFragment(e.Package+"\t", e.Output); err != nil {
					return report{}, err
				}
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

	// Credit a claim only from a cleanly-passed top-level test whose expected
	// package also passed.
	credited := map[string]map[string][]emittedClaim{}
	for _, ec := range claims {
		ts := tests[ec.testKey]
		if ts == nil || ts.violated || ts.terminals != 1 || ts.terminal != "pass" {
			continue
		}
		if machineValid {
			pkg, _, _ := strings.Cut(ec.testKey, "\t")
			if !expected[pkg] {
				continue
			}
			if p := pkgs[pkg]; p == nil || p.terminal != "pass" {
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
