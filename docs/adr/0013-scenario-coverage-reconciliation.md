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
`cmd/scenariocov` reads the `go test -json` stream through an explicit event
automaton over the test2json grammar: a package must `start`, a test must `run`
before it outputs or terminates and terminate exactly once, every started test
must terminate before its package, and a package cannot pass with a failed
test. A cell is credited only when the emitting top-level test ran and reached a
single terminal PASS, belongs to an expected package, and that package started
and passed. In machine-valid mode a non-JSON line, an unknown action, or a
reserved marker outside a running top-level test's own output makes the report
invalid or is rejected. Because it judges each test from its definitive final
disposition, a skipped, failed, parallel-paused, or parent-cleanup-skipped test
earns nothing.

**Fail-closed reporting.** `valid` is the conjunction of three separate
notions — process-succeeded (the real `go test` exit, supplied out of band),
stream-complete (every expected package terminated with pass, or a genuine
no-test skip; no unexpected/duplicate package terminal; no lifecycle
violation), and contract-valid — and deliberately does *not* imply
coverage-complete. Safety inputs (`-test-exit` and a non-empty
`-expected-packages`) are mandatory for `valid:true`, so the local stdin
convenience mode is visibly incomplete. Marker/parser input is strict and the
line accumulator is linearly and globally bounded.

**Hygiene, not a boundary.** A marker-hygiene lint fails the suite if a
top-level system test emits a claim directly instead of through the Receipt
API. It catches the accidental or copy-pasted forge; it is labelled hygiene and
does not pretend to stop a determined author.

## Invariants the mechanism guarantees

- The denominator is well-formed and comes from Go source, not the stream.
- A cell is credited only from a cleanly-passed top-level test in a passed
  expected package.
- A malformed, trailing-content, subtest, duplicate, or unattributable-marker
  stream is rejected; an incomplete/failed/unexpected stream is `valid:false`.
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
