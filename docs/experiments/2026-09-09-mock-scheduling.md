# Bounded scheduling of owned mock fixtures

## Constraint and oracle

The existing mock harness changes six process-wide environment values before calling the real-Swift harness constructor.
That prevents its tests from sharing the [bounded three-slot schedule](2026-09-09-saio-scheduling.md).
The complete reviewed admission run takes 682.678 seconds locally, including 269.420 seconds in thirteen sequential mock groups whose real-Swift counterparts already run in parallel.
The PR69 integration package takes 996.537 seconds and the whole job 1,225 seconds, leaving 203.463 and 275 seconds within the unchanged limits.
Further download recording cases require a measured scheduling improvement first.

The oracle is the complete existing package/test inventory and every behavioural assertion, required-account refusal, race/coverage setting, scenario reconciliation and required CI job.
Each overlapping fixture must retain its own endpoint, two accounts, services, database, local paths and exact owned remote namespaces.
Faster results with missing or weakened tests do not qualify.

## Implementation and proof sequence

1. Keep `New(t)` as the same environment adapter, including existing default account values, absent-authentication skip and required-secondary failure.
   Pass those settings to a constructor with explicit per-instance configuration.
   The owned mock provider supplies its own endpoint and account settings directly, without process-wide environment changes.
2. Opt only the thirteen mock counterparts of already-parallel real-Swift groups into the existing schedule.
   Preserve their sequential subtests, fixture cleanup and complete test bodies.
3. Compare the same fifty-eight download cases before and after scheduling, then check the full mock lane without SAIO configuration and the full owned two-account suite.
   Preserve missing/required-account behaviour, all package-qualified terminal names, raw producer exits, source hashes and scenario reconciliation.
4. Require independent review and protected PR/master delivery with all five jobs successful.
   Measure actual hosted time without widening the 1,200-second package or 1,500-second integration-job limits.

## Serial control

The unchanged `mock-schedule-download-serial-v1` producer passes all 58 named tests in the root, stage and close mock groups, with zero failures or skips and cross-package race/coverage instrumentation.
The package takes 47.267 seconds; the individual groups take 23.960, 15.040 and 7.250 seconds.
Its 326 captured Go/SQL hashes match the independently reviewed admission tree.
This control measures local scheduling.

## Matched comparison and complete verification

The implemented constructor takes explicit endpoint, primary/secondary credentials and required-account settings for each harness.
The existing `New(t)` entry point reads the same environment variables and defaults, while mock fixtures pass their own settings without changing the environment.
Exactly thirteen mock groups gain `t.Parallel`; whitespace-normalised comparison confirms their test bodies are otherwise unchanged.
The existing fourteen real-Swift parallel groups and the limit of three remain unchanged.

The matching `mock-schedule-download-parallel-v1` producer passes the same 58 named tests without failures or skips in 25.810 seconds, a 21.457-second reduction from the serial control.
Both runs retain cross-package coverage, race detection and the complete three-group case inventory.
This is a controlled local scheduling observation; it does not establish production throughput or promise hosted timing.

The complete `mock-schedule-full-v1` producer passes 2,486 named tests across twenty packages with zero failures or skips and 86.9% statement coverage.
Its 326 Go/SQL hashes identify the uncommitted scheduling draft at recorded HEAD `2c57bccccb507614db58ddd6e174f11f6eda396d`.
All preceding test names remain, with the unchanged generated expiry case checked against its recorded start plus one hour.
The system package takes 564.812 seconds; the preceding admission run took 682.678 seconds on this local fixture.
All twenty-seven selected groups resume and finish, and no non-parallel top-level group overlaps them.
As in the earlier experiment, four JSON records briefly overlap because Go releases an execution slot before emitting the preceding pass; the longest observed overlap is 41 microseconds and the runtime's three-slot limit is unchanged.
Scenario reconciliation remains valid at 40/116 cells with 38/52 actions having gaps.

Ten before/after producers exercise the existing real-Swift `New(t)` entry point with absent authentication settings, refused primary credentials, optional refused secondary credentials, required refused secondary credentials and an ordinary control.
Each retains the exact expected skip, failure or pass and its intended diagnostic; none substitutes a compile failure or race report for the expected result.
These intentionally failing/skipping compatibility producers remain separate from the passing complete suite.

The mock-only producer passes all 318 named tests across 58 top-level mock groups in 149.793 seconds without SAIO settings, failures or skips.
Its complete terminal inventory equals the mock subset of the full run, and all 326 source hashes match.
An overlay restoring the old environment-mutating mock constructor is rejected at the first parallel download case by Go's `t.Setenv`/`t.Parallel` guard; it is an expected runtime refusal, not a compile failure.
The unchanged 58-case positive comparison remains the passing control.
Go vet and formatting checks pass.

Independent local review and protected PR/master delivery remain required.
No scenario cell, audit package or valuable-data release gate is closed by this experiment.
