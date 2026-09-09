# Bounded scheduling of owned Swift fixtures

## Constraint and oracle

The [checked-upload delivery](2026-09-08-checked-upload-records.md#protected-delivery) takes 1,074.871 seconds in the master system-test package, within its unchanged 1,200-second limit.
The eleven longest selected SAIO groups account for a substantial part of that sequential run.
The [download before-fix archive](2026-09-09-download-boundaries.md) adds about 133 seconds locally if promoted into maintained regressions; the observed hosted/local ratio of up to 1.38 leaves insufficient existing headroom.
[ADR 0032](../adr/0032-download-placement-prerequisites.md) therefore requires a measured scheduling correction before adding those cases.

The oracle is the complete existing package/test inventory and every existing behavioural assertion, race/coverage setting, scenario reconciliation and required CI check.
Parallelism must preserve isolated service/database/local-path and remote-namespace ownership.
A shorter run with missing, skipped or weakened tests is not an improvement.

## Scheduling boundary

Eleven existing top-level SAIO groups call `t.Parallel`; their test bodies, cases and assertions remain unchanged.
They cover Copy completion, interruption, item identities, overwrite refusal, unchanged-item records and listing fidelity, plus local completion and upload identity, recovery, recording refusal and recovery-stop cases.
Each group keeps its own subtests sequential and creates a separate harness for each case where it did previously.
Both Go CI invocations pass `-parallel=3`, limiting these groups to three active top-level tests per package.
Use the same flag for local verification of this schedule.

Mock groups remain non-parallel because their harness temporarily changes process-wide environment variables with `t.Setenv`.
Go's testing scheduler waits for non-parallel tests to finish before resuming parallel tests, so those environment changes are restored first.
The SAIO groups use their existing per-harness database, services, local temporary paths and exact prefixed remote namespaces; barriers and assertions remain scoped to the current test and job.
Harness namespace generation now uses random text instead of only a wall-clock timestamp, avoiding dependence on clock resolution when independent fixtures start together.
Neither this prefix nor its randomness grants application cleanup or concurrency authority.

No package, test or subtest is filtered from CI.
The race detector, complete cross-package coverage instrumentation, 1,200-second package limit, 1,500-second integration-job limit, raw event capture, producer-exit validation, scenario reconciler and five required job names remain unchanged.

## Controlled local comparison

The serial control and both scheduling experiments run the same three existing SAIO groups: upload item identities, upload recovery and Copy item identities.
All 29 named tests pass in each run with zero failures or skips; the package-qualified terminal test sets match exactly.
All 311 underlying Go/SQL source hashes match across the producers.
The later experiment's source commit adds only the reviewed download contract documentation.
The private overlays change only the eleven parallel markers and the fixture namespace generation; whitespace-normalised test bodies otherwise match the original source.

| Schedule | Maximum open test records | System package |
| --- | ---: | ---: |
| Original sequential source | 1 | 212.847 s |
| Two-group experiment | 2 | 157.711 s |
| Three-group experiment | 3 | 104.822 s |

The complete Go event streams retain each pause, resume and terminal record.
Open records are not an atomic active-work counter: Go releases a parallel execution slot before reporting the preceding test's pass.
The three-group experiment saves 108.025 seconds in this matched local comparison.
Individual groups can take longer under contention even while the package finishes sooner; this is a scheduling measurement, not a faster-transfer or production-throughput claim.
These three groups do not replace full-suite validation or a hosted PR measurement.

## Complete verification and delivery

The complete maintained Go 1.27.1 race/coverage run passes all 2,291 named tests across twenty internal packages, with no failures or skips and 86.6% combined statement coverage.
The system-test package takes 628.569 seconds locally; all 311 recorded Go/SQL source hashes match the tested files.
Every prior package-qualified test case is retained: 2,290 names match literally, and the unchanged authentication test's one absolute-expiry case is checked against its recorded start time plus one hour.
The initial literal comparison rejected that generated timestamp difference; no test was removed or renamed to make the comparison pass.

All eleven selected groups resume and finish, while non-parallel top-level tests finish before their resumed execution.
The full stream briefly has four open parallel records because the next resume precedes the prior pass by 35 microseconds.
The installed Go 1.27.1 testing source releases the execution slot before emitting that pass; the configured three-slot limit remains in force.
The initial validator incorrectly equated open records with active work; its raw intervals and the source-based correction remain preserved.

Scenario reconciliation is valid with the actual producer's SAIO environment, at the unchanged 40/116 cells and 38/52 actions with gaps.
An initial reconciliation invocation omitted that environment and labelled SAIO unconfigured; its report is preserved separately, and the corrected invocation uses the recorded producer settings.
The raw Go producer, its passing exit and all source hashes are unchanged by these validation corrections.
Workflow lint, formatting, the CI aggregate success control and all nineteen incomplete-result refusals pass.
At the previous conservative 1.38 hosted/local ratio, this package time projects to about 867 seconds; only the actual next PR run can establish hosted headroom.
Independent local review and all five latest PR checks on current master, protected merge and all five resulting master checks remain required.
Record the actual complete-suite package and job times before adding maintained download cases.
Keep the original timeout and failed-run evidence, and investigate a new failure rather than widening a limit to conceal it.

This scheduling change adds no scenario coverage and closes no application audit or valuable-data release gate.
The download prerequisites and separate item-record migration remain required.
