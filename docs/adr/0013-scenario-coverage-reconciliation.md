# ADR 0013 — Scenario coverage reconciliation (war-room §65.10)

**Status:** Accepted (test-infrastructure scaffold; explicitly incomplete, non-gating)

## Context

The §65.10 war room measures behavioral coverage as an *action × scenario*
matrix (a "cell" is, e.g., `TransferService.StartCopy` × `happy`). The design
question was how a test can record "this cell was exercised" without the number
becoming a lie — a test that runs a line without asserting the right outcome,
or a test that never ran at all, must not earn a cell.

Several adversarial review rounds established the threat model:

- **In-process attestation is not a trust boundary.** A coverage number that
  arbitrary test code attests to, from evidence that same process authored,
  cannot be made unforgeable by another mechanism inside that process. A secret
  receipt, signature, or private helper in the same binary is readable and
  reproducible by a forging test. The correct response is to stop claiming an
  anti-forgery boundary exists.
- **The real trust domain is reviewed repository code plus CI/branch
  protection.** The useful engineering target is *independent failure
  mechanisms that catch mistakes and weak assertions* — not cryptographic
  resistance to a malicious committer.

So this is a **cooperating-author** progress signal. It is honest about what it
is, and it is deliberately not a release gate.

## Decision

**Denominator — one trusted, non-test source.** `internal/scenariocontract`
holds the action table and required-scenario rules, exposes `All()` (a
defensive copy), a strict `Validate()`, and a deterministic `Hash()`, and is
imported directly by both the system tests and `cmd/scenariocov`. The
denominator is fixed in Go source; it is never read from the test stream.

**Emission — a single-use Receipt.** Each harness wrapper returns a `*Receipt`
carrying that one call's action, job id, error, and outcome. Its `Credit*`
methods check the scenario against the receipt's own evidence (`CreditHappy`
awaits the job to complete; `CreditRefusal` requires the error;
`CreditConflict`/`CreditApplied` the outcome), so a cooperating author does not
attach one call's evidence to another. Consumption is single-use via an atomic
compare-and-swap. A credit is *emitted* as a `go test -json` output line
tagged with a marker; nothing is committed in process. Claims are emitted only
from top-level tests.

**Reconciliation — out of process, after the whole tree finishes.**
`cmd/scenariocov` reads the `go test -json` stream through an explicit phase
automaton over the test2json grammar. Each test moves `idle → running ⇄ paused
→ terminal` by a table-driven legal-transition function, so a duplicate `run`,
a `cont` before `pause`, a terminal without a `run`, output before `run` or
after terminal, and similar illegal transitions are rejected. A package must
`start`, terminate once, have every started test terminate first, and cannot
pass with a failed test. Records are shape-validated (every event names a
package; `start` is package-level, `run`/`pause`/`cont`/`attr` are test-level;
semantically empty `{}`/`null` records are malformed). All output — test and
package — is reassembled, so a reserved marker split across fragments is still
detected; a marker in a non-output event, in package output, or from a subtest
is rejected, and a marker from a *paused* test is parsed but earns no credit. A
cell is credited only when the emitting top-level test ran, was running (not
paused) when the claim line completed, reached a single terminal PASS, and
belongs to an expected package that started and passed. Retained events, tests,
packages, claims, evidence bytes, and reported witnesses are bounded for the
trusted CI artifact at configured limits — not intended as operational
memory limits against a hostile artifact.

**Scope.** This reconciles the authentic records the CI producer profile emits
— `go test -json -v -race -count=1 ./internal/...` (Go 1.27 test2json: `start`,
`run`, `pause`, `cont`, `attr`, `output`, terminals), no `-bench`. Benchmark
activity is rejected as out of scope. It is deliberately not a hostile-input
validator for a non-gating counter: it does not exhaustively reject every
out-of-profile shape a real producer never emits, and a future producer action
would need a one-line addition. Ordinary paused output is accepted but earns no
credit.

**Fail-closed reporting.** `valid` is the conjunction of three separate
notions — process-succeeded (the real `go test` exit, supplied out of band),
stream-complete (every expected package terminated with pass, or a genuine
no-test skip; no unexpected/duplicate package terminal; no lifecycle
violation), and contract-valid — and deliberately does *not* imply
coverage-complete. Safety inputs (`-test-exit` and a non-empty
`-expected-packages`) are mandatory for `valid:true`, so the local stdin
convenience mode is visibly incomplete. Marker/parser input from the supported
producer is validated, and the line accumulator reassembles linearly, bounded
for the trusted CI artifact at the configured limits.

**Hygiene, not a boundary.** A marker-hygiene lint fails the suite if a
top-level system test emits a claim directly instead of through the Receipt
API. It catches the accidental or copy-pasted forge; it is labelled hygiene and
does not pretend to stop a determined author.

## Invariants the mechanism guarantees

- The denominator is well-formed and comes from Go source, not the stream.
- A cell is credited only from a cleanly-run, running-phase, cleanly-passed
  top-level test in a passed expected package.
- A malformed, trailing-content, subtest, duplicate, or misplaced/unattributable
  marker (including one split across fragments), an illegal lifecycle
  transition, an empty or misshapen record, and benchmark activity are rejected
  or make the stream `valid:false`; an incomplete/failed/unexpected stream is
  `valid:false`.
- Retained state (events, tests, packages, claims, evidence, witnesses) is
  bounded.
- `valid:true` requires the producer exit and expected-package set; it never
  asserts coverage completeness or application behavior.

## Known limitations (deferred, by name)

- The number is a cooperating-author signal, not authenticated proof of
  behavior. A future higher-assurance design would give an orchestrator a
  per-scenario identity and observe bridge calls plus complete
  Swift/local/durable-state deltas independently of what the test prints.
- CI does not gate on `coverageComplete`, a minimum baseline, or the currently
  credited cells; deleting scenario tests, or adding a contract action without
  a system test, can leave CI green. Turning this into a regression gate
  (checked-in covered-cell baseline, required coverage or waiver for new
  actions, growth toward the full contract) is separate, later work.
- The floor (~102 required cells across 47 actions) is below DESIGN's literal
  ~162 baseline and far below the full matrix (data class, direction, hostile
  names, paging, fault points, UI, platform). It is a floor to grow from.

## Origin

The design settled through several rounds of adversarial review that
reproduced concrete forgery and parser defects: in-process spoofs (answered by
moving attestation out of the trust story and reconciliation out of process),
then a series of parser-contract holes — subtest-ancestry ambiguity, packages
that only terminated rather than passed, unbounded and quadratic line handling,
misplaced markers, and lifecycle events that a real `go test -json` producer
never emits. The current mechanism is the response to those, and its guarantees
are exactly the invariants listed above — no more. The blow-by-blow findings
belong in the project's review correspondence, not this record.
