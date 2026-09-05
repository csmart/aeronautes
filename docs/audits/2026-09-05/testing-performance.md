# Testing, performance and documentation assessment

Audited revision: `cc6465ac69ec00756d9976bbf6cf7531b07da66d`.
These findings supplement the [data](data-safety.md), [security](security-release.md), and [UI](ui-product.md) reports.
Exact baseline evidence and limits are in [verification.md](verification.md).

## TEST-01 — High: the independent oracle hides stored Content-Encoding changes

Disposition: new, directly reproduced against the raw-HTTP system-test oracle.
Code: `internal/systemtest/oracle/oracle.go:45`, `:282`, `:288`, `:303`.
Oracle: DESIGN §65.10's independent before/after content/header comparison, and the stored HTTP representation.

The oracle constructs an `http.Client` without disabling automatic response decompression.
With the default transport, a gzip response is transparently decoded and `Content-Encoding` is removed before `snapshotObject` records it.
The probe serves one object with gzip storage encoding, then changes the stored object to unencoded bytes with the same decoded content.
Both snapshots report empty `ContentEncoding` and identical content digests, so the delta is empty.
This is a false negative in the test oracle itself, independent of whether the production copy code currently makes this exact mutation.
Go documents the transport's automatic gzip behaviour in [net/http.Transport](https://pkg.go.dev/net/http#Transport).

The probe `TestAuditOracleDetectsContentEncodingChange` fails with both `stored encoding lost` and `oracle declared no change` diagnostics.
Its [source](probes/oracle_encoding_probe_test.go.txt) and [log](evidence/oracle-probe.log) are preserved.

Remedy: explicitly choose stored versus decoded representation semantics, prevent implicit transport rewriting, and independently capture the stored header/digest evidence.
If decoded logical bytes are also needed, compute that as a separate, explicit field rather than replacing the stored representation.
Acceptance: gzip/plain transitions, changed compressed bytes, malformed compressed bodies, ranged responses, and omitted/changed encoding headers produce the specified independent state delta or an explicit oracle refusal.
Do this before relying on the expanded harness to certify metadata/content fidelity.

## TEST-02 — High: system coverage is honest scaffolding, not a release safety gate

Disposition: documented §65.10/ADR 0013 gap, revalidated in source and this audit run.
Code: `internal/scenariocontract/contract.go`, `internal/systemtest/harness.go`, `internal/systemtest/cmd/scenariocov`, `.github/workflows/ci.yml:210`.
Oracle: K-TEST-001, DESIGN §57–59 and §65.10, [ADR 0013](../../adr/0013-scenario-coverage-reconciliation.md).

The contract inventories 47 bound actions and requires 102 conservative-floor cells.
The broader design's literal baseline is approximately 162 before provider/platform variants and UI paths are joined.
CI correctly checks producer exit, complete event stream, package set, claim provenance and contract validity.
Its `valid:true` deliberately does not mean all actions/scenarios are covered.
There is currently no hard requirement for full coverage or a non-regression ratchet on already credited cells.
The frontend's 70% and Go's 75% statement floors both use `continue-on-error`.

This local run produced a valid report with 0/102 credited cells and gaps for all 47 actions because the real-cluster tests skipped.
That is correct reconciliation of incomplete execution, not evidence that the parser accepted a failed suite.
The earlier recorded 8/102 result is historical and was not reproduced here.

Remedy: retain the existing fail-closed stream checks, publish an explicit per-action required/not-applicable contract with reasons, and add a hard ratchet on accepted critical regression cells.
Expand to a complete supported-operation release contract as the tests land.
Keep statement coverage as a diagnostic alongside behavioural coverage, never a substitute for assertions about damage, intent and durable results.
Acceptance: deleting a required regression, skipping a required cluster lane, dropping a claimed cell, breaking artifact capture, or failing a relevant test prevents release.

## TEST-03 — High: the oracle and fault model do not yet span the promised state

Disposition: documented foundation gap with newly reproduced consequences, not a request to discard the current harness.
Code: `internal/systemtest/oracle/oracle.go`, `internal/systemtest/harness.go`, `internal/swifttest/`, `internal/app/transferservice_test.go`.
Oracle: K-XFER-002/003, K-DEL-002, K-SEG-004 and DESIGN §65.10.

The raw-HTTP oracle is a valuable independent layer, but its current scoped snapshots do not encompass all account/container configuration, version archives, arbitrary outside-prefix aliases, local filesystem state, jobs, recovery records and audit outcomes.
It deliberately refuses some nested/ranged SLO forms rather than incorrectly certifying them.
Those refusals are sound; the corresponding application guarantees still need an oracle and scenario coverage.
Snapshots limited to the operation's named prefix cannot prove that an outside alias or neighbouring job was preserved.

Mock request hooks can already plant useful final-HEAD races, as the audit demonstrates.
There is no complete deterministic substrate spanning request acceptance/response loss, durable writes, child-process death/restart, native file placement, and frontend deferred completions.
Polling or sleeps used to observe job completion must not become the means of choosing a safety-critical interleaving.
An ordinary process crash and an OS/power failure have different durability semantics and require different evidence.

Remedy: implement the small [fault/barrier slice](remediation-plan.md#the-faultbarrier-harness-is-included) with typed event boundaries and bounded waits.
Start from the reproduced damage cases and add independent complete-fixture snapshots, including untouched accounts/aliases and the local/durable state relevant to each operation.
Use real subprocess kill/restart tests for process recovery and separately specified lost-history/durability experiments for OS/power failure.
Acceptance: each supported irreversible boundary has a reproducible interruption/response-loss case, explicit allowed deltas, preserved collateral state, exact durable outcomes, and no deadlock after cancellation.

## TEST-04 — High: UI and native-platform evidence are far below product scope

Disposition: measured frontend gap and unexecuted platform requirements.
Code: `frontend/vite.config.ts`, `frontend/src/features/`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `scripts/smoke.sh`.
Oracle: K-UI-*, K-PLAT-*, DESIGN §60/61/65.10.

The frontend suite passes 48 tests but covers only 17.18% of statements (556/3,235).
Inspector, transfer and job components report zero coverage.
The UI audit's deferred-response/component probes found 12 failures despite the baseline remaining green.
This supports adding tests for complete user intent and completion sequences before spending effort on tests that only mirror rendering branches.

The existing Linux headless smoke is useful and should remain.
It cannot establish macOS/Windows webview, native picker, keychain, reparse-point/filesystem, screen-reader, child termination, zoom or installer behaviour.
This audit compiled the pure-Go core for six targets and a Linux production binary, but did not execute those native platform flows or regenerate Wails bindings.
Build-time portability must not be described as six-target runtime acceptance.

Remedy: require identity/preview/cancellation/error component regressions plus representative native end-to-end journeys.
Add a small supported-platform smoke/materialization/secret-store matrix using real runners and packaged artifacts, then grow filesystem and assistive-technology coverage by the supported contract.
Acceptance: each claimed platform installs, launches, connects, uses native pickers/secret storage, completes and cancels a tiny transfer, preserves exact local paths, restarts, and exposes usable keyboard/focus/error behaviour.

## PERF-01 — High at scale: bounding workers does not bound the application's memory

Disposition: source-backed requirement gap; no large-namespace RSS measurement was completed.
Code: DS-12's execution/preflight/recovery paths; `internal/app/findservice.go:27`, `:85`, `:152`; `internal/app/jobrunner.go:79`; `frontend/src/features/browser/browserState.svelte.ts:329`.
Oracle: K-PERF-001, DESIGN §15 and §37/38.

The merge-join planner and bounded list pages are good foundations.
Surrounding paths retain complete download plans/name maps, growing captured/committed/conflict inventories, and all names in some recovery prefixes.
Virtualizing DOM rows does not bound the accumulated browser entries array.
Find bounds matches per scan at 1,000, but the service keeps a map of scans without admission/expiry; a completed scan stays until explicit cancellation.
The runner bounds active jobs but launches a goroutine and cancellation state for every queued submission.
There is no equivalent global bound for queue length or concurrently admitted Find scans.

Remedy: persist/page large identity sets, bound pending work and scan lifetime, and make memory/queue/child/API limits apply across services sharing an endpoint.
Keep complete collision detection by using a scratch-backed index rather than truncating the plan.
Acceptance: measured RSS follows configured page/batch/concurrency limits across increasing namespace sizes; abandoned scans, cancelled queued work and completed jobs release resources; overload produces a clear admission result.

## PERF-02 — Medium: Auto and the benchmark do not implement adaptive maximum-safe throughput

Disposition: explicitly deferred K-PERF-003 and broader performance evidence gap.
Code: `internal/app/profiles.go`, `internal/app/transfersettings.go`, `internal/app/benchmarkservice.go:129`, `:168`, `:174`.
Oracle: K-PERF-003 and DESIGN §38–43/59.

Auto resolves to a fixed Balanced baseline or recorded benchmark recommendation; it is not a feedback controller responding to 429/5xx rates, request latency, memory pressure or endpoint contention.
The current benchmark's default workload is 200 small 8 KiB native objects with PUT/HEAD/DELETE work.
That is useful as a microbenchmark but does not characterize rclone transfers, large DLOs, metadata-heavy copy, high-latency links or sustained multi-job load.
No dedicated Go fuzz targets or `Benchmark*` performance tests were found in the inspected source.
The absence of those particular test mechanisms is not itself a defect; the unproven performance contract is.

Remedy: define representative small/large/mixed workloads and measure throughput, p95 latency, retries, memory and fairness before designing adaptive control.
Then implement endpoint budgets and bounded feedback with explicit operator override semantics.
Acceptance: sustained overload backs off, recovery ramps within ceilings, small control requests remain responsive during bulk jobs, and displayed effective settings agree with actual engine admission.
Benchmark fixtures must remain disposable and their cleanup must satisfy the same safety/accounting rules as other jobs.

## DOC-01 — Medium: status and ownership prose can be read more strongly than the code permits

Disposition: source-backed documentation/contract conflict, already partly recorded in §65.10.
Code: `README.md`, `AGENTS.md`, `docs/DESIGN.md:1683`, `:1924`, `:3908`, `:3937`, `:4170`; `docs/REQUIREMENTS.md` phase gates.

The phase-complete summary coexists with named unimplemented safety requirements.
The README's ownership-proof description and older §34 body imply automatic segment cleanup where the current inspector correctly requires force.
The strongest architectural conflict is the ownership-scan prohibition versus later live-exclusivity requirements discussed in DS-01.
The README's ADR index also stops at 0012 although 0013 exists.

Remedy: reconcile safety guarantees in an ADR, update conflicting normative paragraphs, and publish one supported/deferred/unsafe capability view linked to evidence.
Keep historical test counts dated instead of presenting them as today's measured status.
Do not weaken force-only code to make it match stale prose.
Acceptance: documentation, bridge/API contract, visible controls, test inventory and release gates agree on which operations and platforms are supported and on what concurrency/durability assumptions.
