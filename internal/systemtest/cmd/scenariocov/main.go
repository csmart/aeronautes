// Command scenariocov reconciles war-room scenario coverage from a
// `go test -json` event stream. It credits a claim ONLY if the emitting
// test and every observed ancestor reached a terminal PASS, so a test that
// skipped, failed, paused at t.Parallel, or was skipped by a later parent
// cleanup earns no credit — the tool judges each test from its definitive
// final disposition after the whole tree finishes.
//
// The coverage DENOMINATOR comes from internal/scenariocontract, imported
// directly into this separately executed process; it is never taken from
// the test stream. Any inventory marker in the stream is REJECTED so an
// obsolete or forged producer is visible immediately.
//
// The report is an honest-author progress signal, not authenticated proof
// of application behavior: a test can bypass the harness API and print a
// claim. CI does not gate on zero gaps. See the war-room design notes.
//
// Usage (CI): go test -json ... | tee events.jsonl ; then
//
//	scenariocov -events events.jsonl -test-exit "$code" \
//	  -expected-packages pkgs.txt -out report.json
//
// Reading stdin with default -test-exit 0 stays convenient locally.
package main

import (
	"bufio"
	"encoding/json"
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
	schemaVersion   = 3
	maxProtocolLine = 1 << 20 // 1 MiB cap on a single reassembled line
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

// report keeps the distinct notions of "valid" separate (codex war-room
// round-5): valid is the conjunction of process-succeeded, stream-complete,
// and contract-valid — it deliberately does NOT imply coverage-complete.
type report struct {
	SchemaVersion    int              `json:"schemaVersion"`
	Valid            bool             `json:"valid"`
	ProcessSucceeded bool             `json:"processSucceeded"`
	StreamComplete   bool             `json:"streamComplete"`
	ContractValid    bool             `json:"contractValid"`
	CoverageComplete bool             `json:"coverageComplete"`
	CommitSHA        string           `json:"commitSha"`
	GoVersion        string           `json:"goVersion"`
	SAIOConfigured   bool             `json:"saioConfigured"`
	ContractHash     string           `json:"contractHash"`
	TotalActions     int              `json:"totalActions"`
	TotalRequired    int              `json:"totalRequired"`
	TotalCovered     int              `json:"totalCovered"`
	ActionsWithGaps  int              `json:"actionsWithGaps"`
	MissingPackages  []string         `json:"missingPackages,omitempty"`
	Actions          []actionCoverage `json:"actions"`
}

type options struct {
	commitSHA        string
	saioConfigured   bool
	testExit         int
	expectedPackages []string // if empty, package completeness is not enforced
}

func main() {
	out := flag.String("out", "", "write the JSON report to this path")
	events := flag.String("events", "", "read the go test -json stream from this file (default: stdin)")
	testExit := flag.Int("test-exit", 0, "exit code of the go test process that produced the stream")
	expectedPkgs := flag.String("expected-packages", "", "file of expected package import paths (one per line); each must terminate")
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
	if *expectedPkgs != "" {
		list, err := readLines(*expectedPkgs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scenariocov: read expected packages: %v\n", err)
			os.Exit(2)
		}
		opts.expectedPackages = list
	}

	rep, err := reconcile(src, opts)
	if err != nil {
		// A protocol violation (malformed/forged/oversized stream). The raw
		// stream remains in the tee'd artifact for diagnosis.
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

// reconcile parses a test2json stream and builds the coverage report. It
// returns an error only for a broken/forged stream (malformed or duplicate
// claim, an inventory marker, an oversized protocol line); a failed test run
// or a truncated stream is reported as valid:false, not an error.
func reconcile(r io.Reader, opts options) (report, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxProtocolLine+1024)

	status := map[string]string{}        // "pkg\tTest" -> pass|fail|skip
	pkgTerminated := map[string]string{} // package import path -> terminal action
	partial := map[string]*strings.Builder{}
	seen := map[string]bool{} // duplicate-claim guard, keyed testKey+action+scenario
	var claims []emittedClaim

	// processLine scans one complete output line for markers, strictly.
	processLine := func(testKey, line string) error {
		if strings.Contains(line, scenariocontract.InventoryMarker) {
			return fmt.Errorf("inventory marker in stream from %s: the denominator comes from the contract, not test output", testKey)
		}
		idx := strings.Index(line, scenariocontract.ClaimMarker)
		if idx < 0 {
			return nil
		}
		payload := strings.TrimSpace(line[idx+len(scenariocontract.ClaimMarker):])
		dec := json.NewDecoder(strings.NewReader(payload))
		dec.DisallowUnknownFields()
		var c scenariocontract.Claim
		if err := dec.Decode(&c); err != nil {
			return fmt.Errorf("malformed claim from %s: %v (%q)", testKey, err, payload)
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

	for sc.Scan() {
		var e event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue // a non-JSON banner line (e.g. a build error) — not protocol
		}
		switch e.Action {
		case "pass", "fail", "skip":
			if e.Test != "" {
				status[e.Package+"\t"+e.Test] = e.Action
			} else if e.Package != "" {
				pkgTerminated[e.Package] = e.Action
			}
		case "output":
			if e.Test == "" {
				continue // package-level banner; claims are emitted from tests
			}
			key := e.Package + "\t" + e.Test
			buf := partial[key]
			if buf == nil {
				buf = &strings.Builder{}
				partial[key] = buf
			}
			buf.WriteString(e.Output)
			s := buf.String()
			for {
				nl := strings.IndexByte(s, '\n')
				if nl < 0 {
					break
				}
				if err := processLine(key, s[:nl]); err != nil {
					return report{}, err
				}
				s = s[nl+1:]
			}
			if len(s) > maxProtocolLine {
				return report{}, fmt.Errorf("protocol line exceeded %d bytes for %s (garbage or truncated stream?)", maxProtocolLine, key)
			}
			buf.Reset()
			buf.WriteString(s)
		}
	}
	if err := sc.Err(); err != nil {
		if err == bufio.ErrTooLong {
			return report{}, fmt.Errorf("a stream line exceeded the protocol limit")
		}
		return report{}, err
	}

	// Denominator: the trusted, non-test contract.
	contract := scenariocontract.All()
	contractValid := scenariocontract.Validate() == nil && len(contract) > 0

	// Credit claims from tests (and every observed ancestor) that passed.
	credited := map[string]map[string][]emittedClaim{}
	for _, ec := range claims {
		if !allPassed(ec.testKey, status) {
			continue
		}
		if credited[ec.Action] == nil {
			credited[ec.Action] = map[string][]emittedClaim{}
		}
		credited[ec.Action][ec.Scenario] = append(credited[ec.Action][ec.Scenario], ec)
	}

	// Stream completeness: every expected package must have terminated.
	streamComplete := len(status) > 0 || len(pkgTerminated) > 0
	var missing []string
	if len(opts.expectedPackages) > 0 {
		for _, pkg := range opts.expectedPackages {
			if _, ok := pkgTerminated[pkg]; !ok {
				missing = append(missing, pkg)
			}
		}
		sort.Strings(missing)
		streamComplete = len(missing) == 0
	}

	rep := report{
		SchemaVersion:    schemaVersion,
		ProcessSucceeded: opts.testExit == 0,
		StreamComplete:   streamComplete,
		ContractValid:    contractValid,
		CommitSHA:        opts.commitSHA,
		GoVersion:        runtime.Version(),
		SAIOConfigured:   opts.saioConfigured,
		ContractHash:     scenariocontract.Hash(),
		TotalActions:     len(contract),
		MissingPackages:  missing,
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

// allPassed reports whether the exact test node passed and every ancestor
// prefix that ACTUALLY EXISTS as an observed test node also passed. It does
// NOT require a pass for a lexical slash-prefix that was never a real node,
// so a subtest whose name contains a slash (t.Run("a/b")) is not failed for
// a phantom "a" parent (codex war-room round-5 F-04).
func allPassed(key string, status map[string]string) bool {
	if status[key] != "pass" {
		return false
	}
	pkg, test, ok := strings.Cut(key, "\t")
	if !ok {
		return true
	}
	parts := strings.Split(test, "/")
	for i := 1; i < len(parts); i++ { // proper ancestors only
		ancestor := pkg + "\t" + strings.Join(parts[:i], "/")
		if st, observed := status[ancestor]; observed && st != "pass" {
			return false
		}
	}
	return true
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

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, ln := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			lines = append(lines, s)
		}
	}
	return lines, nil
}
