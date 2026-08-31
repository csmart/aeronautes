// Command scenariocov reconciles war-room scenario coverage from a
// `go test -json` event stream. It credits a claim ONLY if the emitting
// top-level test reached a terminal PASS, so a test that skipped, failed,
// paused at t.Parallel, or was skipped by a later parent cleanup earns no
// credit — the tool judges each test from its definitive final disposition
// after the whole tree finishes.
//
// The coverage DENOMINATOR comes from internal/scenariocontract, imported
// directly into this separately executed process; it is never taken from
// the test stream. Any inventory marker in the stream is REJECTED.
//
// Claims are emitted only from top-level tests. A claim whose test name
// contains a slash (a subtest) is rejected: the go test -json stream does
// not label parentage, so a subtest name is indistinguishable from a
// sibling, and crediting it soundly needs a parent protocol that does not
// exist yet. A top-level test can run data subtests and credit after they
// finish.
//
// The report is an honest-author progress signal, not authenticated proof
// of application behavior: a test can bypass the harness API and print a
// claim. CI does not gate on zero gaps.
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
	maxProtocolLine  = 1 << 20 // 1 MiB cap on a single reassembled line
	maxTotalPartial  = 8 << 20 // 8 MiB cap on all retained partial lines
	maxActiveBuffers = 4096    // cap on concurrently retained per-test buffers
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
	out := flag.String("out", "", "write the JSON report to this path")
	events := flag.String("events", "", "read the go test -json stream from this file (default: stdin)")
	testExit := flag.Int("test-exit", 0, "exit code of the go test process that produced the stream (required for valid:true)")
	expectedPkgs := flag.String("expected-packages", "", "file of expected package import paths, one per line (required for valid:true)")
	flag.Parse()

	src := io.Reader(os.Stdin)
	if *events != "" {
		f, err := os.Open(*events)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scenariocov: open events: %v\n", err)
			os.Exit(2)
		}
		defer f.Close()
		src = f
	}

	opts := options{
		commitSHA:      os.Getenv("GITHUB_SHA"),
		saioConfigured: os.Getenv("AERONAUTES_SAIO_AUTH_URL") != "",
		testExit:       *testExit,
	}
	// An implicit zero exit is not evidence: only a value the caller actually
	// supplied counts.
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "test-exit" {
			opts.testExitProvided = true
		}
	})
	if *expectedPkgs != "" {
		list, err := readExpected(*expectedPkgs)
		if err != nil {
			writeErrorReport(*out, opts, fmt.Errorf("expected packages: %w", err))
			fmt.Fprintf(os.Stderr, "scenariocov: expected packages: %v\n", err)
			os.Exit(2)
		}
		opts.expectedPackages = list
	}

	rep, err := reconcile(src, opts)
	if err != nil {
		// A protocol violation (malformed/forged/oversized stream). Write a
		// structured invalid report before exiting so the failure class is
		// visible in the artifact, alongside the tee'd raw stream.
		writeErrorReport(*out, opts, err)
		fmt.Fprintf(os.Stderr, "scenariocov: %v\n", err)
		os.Exit(2)
	}

	data, _ := json.MarshalIndent(rep, "", "  ")
	if *out != "" {
		if werr := os.WriteFile(*out, data, 0o644); werr != nil {
			fmt.Fprintf(os.Stderr, "scenariocov: write %s: %v\n", *out, werr)
			os.Exit(2)
		}
	}
	fmt.Fprintf(os.Stderr, "SCENARIO COVERAGE: %d/%d cells; %d/%d actions with gaps; valid=%t (process=%t stream=%t contract=%t) saioConfigured=%t\n",
		rep.TotalCovered, rep.TotalRequired, rep.ActionsWithGaps, rep.TotalActions,
		rep.Valid, rep.ProcessSucceeded, rep.StreamComplete, rep.ContractValid, rep.SAIOConfigured)

	if !rep.Valid {
		os.Exit(1)
	}
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

// reconcile parses a test2json stream and builds the coverage report. It
// returns an error only for a broken/forged stream (malformed, duplicate, or
// subtest claim; an inventory marker; a trailing-content claim; an
// unterminated marker; an oversized or over-budget stream). A failed test
// run or a truncated stream is reported as valid:false, not an error.
func reconcile(r io.Reader, opts options) (report, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxProtocolLine+1024)

	status := map[string]string{}        // "pkg\tTest" -> pass|fail|skip
	pkgTerminated := map[string]string{} // package import path -> terminal action
	pkgHasTests := map[string]bool{}     // package -> saw a test-level event
	var duplicatePkgs []string
	partial := map[string][]byte{} // per-test incomplete line, linearly compacted
	totalPartial := 0
	seen := map[string]bool{} // duplicate-claim guard, keyed testKey+action+scenario
	var claims []emittedClaim
	nonJSON := 0

	// processLine scans one complete output line for markers, strictly.
	processLine := func(testKey, line string) error {
		if strings.Contains(line, scenariocontract.InventoryMarker) {
			return fmt.Errorf("inventory marker in stream from %s: the denominator comes from the contract, not test output", testKey)
		}
		idx := strings.Index(line, scenariocontract.ClaimMarker)
		if idx < 0 {
			return nil
		}
		if _, testName, _ := strings.Cut(testKey, "\t"); strings.Contains(testName, "/") {
			return fmt.Errorf("claim from subtest %q: claims must be emitted from a top-level test", testKey)
		}
		payload := strings.TrimSpace(line[idx+len(scenariocontract.ClaimMarker):])
		dec := json.NewDecoder(strings.NewReader(payload))
		dec.DisallowUnknownFields()
		var c scenariocontract.Claim
		if err := dec.Decode(&c); err != nil {
			return fmt.Errorf("malformed claim from %s: %v (%q)", testKey, err, payload)
		}
		// Exactly one JSON value: a second decode must be clean EOF, so a
		// valid claim followed by trailing content is rejected.
		var tail json.RawMessage
		if err := dec.Decode(&tail); !errors.Is(err, io.EOF) {
			return fmt.Errorf("trailing content after claim from %s: %q", testKey, payload)
		}
		if !scenariocontract.Requires(c.Action, c.Scenario) {
			return fmt.Errorf("claim from %s names an unknown or unrequired action/scenario %q/%q", testKey, c.Action, c.Scenario)
		}
		dk := testKey + "\x00" + c.Action + "\x00" + c.Scenario
		if seen[dk] {
			return fmt.Errorf("duplicate claim from %s for %s/%s", testKey, c.Action, c.Scenario)
		}
		seen[dk] = true
		claims = append(claims, emittedClaim{testKey: testKey, Action: c.Action, Scenario: c.Scenario, Evidence: c.Evidence})
		return nil
	}

	// flush finalises a test's residual partial line: a leftover that still
	// carries a marker is an unterminated/corrupt claim and is rejected; a
	// non-marker leftover is dropped. The buffer is then released.
	flush := func(key string) error {
		buf, ok := partial[key]
		if !ok {
			return nil
		}
		if bytes.Contains(buf, []byte(scenariocontract.ClaimMarker)) || bytes.Contains(buf, []byte(scenariocontract.InventoryMarker)) {
			return fmt.Errorf("unterminated protocol marker in residual output from %s", key)
		}
		totalPartial -= len(buf)
		delete(partial, key)
		return nil
	}

	// appendFragment adds an output fragment and processes any complete lines
	// it produces, compacting the remainder to the front of the buffer so a
	// long line arriving in chunks stays linear, not quadratic.
	appendFragment := func(key, frag string) error {
		buf := partial[key]
		if buf == nil {
			if len(partial) >= maxActiveBuffers {
				return fmt.Errorf("too many active per-test buffers (>%d): truncated or garbage stream?", maxActiveBuffers)
			}
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
			if err := processLine(key, string(buf[start:nl])); err != nil {
				return err
			}
			start = nl + 1
		}
		if start > 0 {
			n := copy(buf, buf[start:]) // drop the consumed prefix in place
			buf = buf[:n]
		}
		if len(buf) > maxProtocolLine {
			return fmt.Errorf("protocol line exceeded %d bytes for %s (garbage or truncated stream?)", maxProtocolLine, key)
		}
		partial[key] = buf
		totalPartial += len(buf)
		if totalPartial > maxTotalPartial {
			return fmt.Errorf("retained partial output exceeded %d bytes: truncated or garbage stream?", maxTotalPartial)
		}
		return nil
	}

	for sc.Scan() {
		var e event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			nonJSON++ // a non-JSON banner line (e.g. a build error) — recorded, not credited
			continue
		}
		switch e.Action {
		case "pass", "fail", "skip":
			if e.Test != "" {
				key := e.Package + "\t" + e.Test
				status[key] = e.Action
				pkgHasTests[e.Package] = true
				if err := flush(key); err != nil {
					return report{}, err
				}
			} else if e.Package != "" {
				if _, dup := pkgTerminated[e.Package]; dup {
					duplicatePkgs = append(duplicatePkgs, e.Package)
				}
				pkgTerminated[e.Package] = e.Action
			}
		case "output":
			if e.Test == "" {
				continue // package-level banner; claims are emitted from tests
			}
			pkgHasTests[e.Package] = true
			if err := appendFragment(e.Package+"\t"+e.Test, e.Output); err != nil {
				return report{}, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		if err == bufio.ErrTooLong {
			return report{}, fmt.Errorf("a stream line exceeded the protocol limit")
		}
		return report{}, err
	}
	// EOF: reject any marker still stranded in a partial buffer.
	for key := range partial {
		if err := flush(key); err != nil {
			return report{}, err
		}
	}

	contract := scenariocontract.All()
	contractValid := scenariocontract.Validate() == nil && len(contract) > 0

	// Credit claims from top-level tests that terminally passed. Subtest
	// claims were already rejected in processLine, so the exact node passing
	// is the whole check.
	credited := map[string]map[string][]emittedClaim{}
	for _, ec := range claims {
		if status[ec.testKey] != "pass" {
			continue
		}
		if credited[ec.Action] == nil {
			credited[ec.Action] = map[string][]emittedClaim{}
		}
		credited[ec.Action][ec.Scenario] = append(credited[ec.Action][ec.Scenario], ec)
	}

	// Package completeness: every expected package must terminate with
	// pass, or with skip only when it had no tests (go emits a package-level
	// skip for "[no test files]"). A fail — or a skip despite having tests — is
	// not complete. Unexpected or duplicate package terminals break the exact
	// set.
	var missing, failed, unexpected []string
	if len(opts.expectedPackages) > 0 {
		expectedSet := map[string]bool{}
		for _, pkg := range opts.expectedPackages {
			expectedSet[pkg] = true
			term, ok := pkgTerminated[pkg]
			switch {
			case !ok:
				missing = append(missing, pkg)
			case term == "pass":
			case term == "skip" && !pkgHasTests[pkg]:
			default:
				failed = append(failed, pkg)
			}
		}
		for pkg := range pkgTerminated {
			if !expectedSet[pkg] {
				unexpected = append(unexpected, pkg)
			}
		}
		unexpected = append(unexpected, duplicatePkgs...)
		sort.Strings(missing)
		sort.Strings(failed)
		sort.Strings(unexpected)
	}
	streamComplete := len(opts.expectedPackages) > 0 &&
		len(missing) == 0 && len(failed) == 0 && len(unexpected) == 0

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
		UnexpectedPkgs:   unexpected,
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
	// valid requires that the caller actually supplied the safety inputs: a
	// nonzero/absent producer exit or an empty expected set cannot be valid
	//. streamComplete already requires a non-empty expected set.
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
// and free of duplicates, so an empty or malformed set cannot silently pass
// the completeness check.
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
