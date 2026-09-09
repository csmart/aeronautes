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
In this producer, four JSON records briefly overlap because Go releases an execution slot before cleanup and the preceding pass; the longest observed overlap is 41 microseconds and the runtime's three-slot limit is unchanged.
Scenario reconciliation remains valid at 40/116 cells with 38/52 actions having gaps.

Ten before/after producers exercise the existing real-Swift `New(t)` entry point with absent authentication settings, refused primary credentials, optional refused secondary credentials, required refused secondary credentials and an ordinary control.
Each retains the exact expected skip, failure or pass and its intended diagnostic; none substitutes a compile failure or race report for the expected result.
These intentionally failing/skipping compatibility producers remain separate from the passing complete suite.

The mock-only producer passes all 318 named tests across 58 top-level mock groups in 149.793 seconds without SAIO settings, failures or skips.
Its complete terminal inventory equals the mock subset of the full run, and all 326 source hashes match.
An overlay restoring the old environment-mutating mock constructor is rejected at the first parallel download case by Go's `t.Setenv`/`t.Parallel` guard; it is an expected runtime refusal, not a compile failure.
The unchanged 58-case positive comparison remains the passing control.
Go vet and formatting checks pass.

Independent local review and protected PR/master delivery passed as recorded below.
No scenario cell, audit package or valuable-data release gate is closed by this experiment.

## Independent review and protected delivery

Independent review approved the admission tests and the separate scheduling commit.
The reviewer reproduced all 2,486 named full-suite passes across twenty packages with no failures or test skips and a 563.2-second system package.
A separate system-package stress run at parallelism eight passed 690 named tests in 348.1 seconds without failures or skips.
The thirteen mock test bodies remain unchanged apart from their parallel scheduling wrappers.

Go releases a parallel execution slot before the finishing group's cleanup and final pass event.
The author's full JSON stream had a longest overlap of 41 microseconds; the reviewer's had one 695-millisecond interval while a mock harness closed its servers.
These are run-specific event intervals, not a fixed timing bound or evidence of a fourth running test body under the three-slot limit.
Each fixture owns its cleanup state and the full inventory remains required.

[PR 70](https://github.com/csmart/aeronautes/pull/70) passed all five required jobs on reviewed source `82f0b3901e315d87fec163bc3c62f33345d3e3df` in the [PR run](https://github.com/csmart/aeronautes/actions/runs/34343266319) with current master incorporated.
The protected merge produced master `ca3439bfc9165c1e46e985cffc63dd94b16507e7`, whose [own run](https://github.com/csmart/aeronautes/actions/runs/34345016975) also passed all five jobs.
The exact source and merged tree is `f512a884624f651bf21a017274b95d627cce17ca` and both logical patches are preserved.
The source branch remained unchanged during delivery, with no ruleset bypass.

| Observed run | System-test package | Integration job |
| --- | ---: | ---: |
| PR 70 | 792.682 s | 1,011 s |
| Resulting master | 826.057 s | 1,064 s |

Both complete integration logs contain 2,484 named passes across twenty passing packages, zero failures and the existing optional `TestRcloneMatrix` and `TestKeychainRoundTrip` skips.
The complete 2,486-test local terminal inventory is retained, including validation of the clock-generated expiry name against each producer's recorded time.
The PR log SHA-256 is `3d59f50f4b078e33902bdf1b3523c435e82216b28c3a19f8452fb3af4257e94a`; the master log SHA-256 is `3101335a59e118b0fd17bfd614a273aa6fd7148a6300dc34f959f4f646d6cde5`.
Within the unchanged 1,200-second package and 1,500-second integration-job limits, minimum observed headroom is 373.943 and 436 seconds respectively.
These measured runs do not promise a fixed hosted duration or production transfer performance.

This delivers the admission regressions and fixture scheduling prerequisite for the separate download recording contract.
Downloads remain record version zero and all wider audit and valuable-data release gates remain in force.
