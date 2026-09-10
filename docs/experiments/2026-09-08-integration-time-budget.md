# Integration suite time budget

## Observed failure and oracle

The [first PR60 integration run](https://github.com/csmart/aeronautes/actions/runs/34237734813/job/102099716973) at reviewed source `8eea75f4622c6ab5b6a75333d20244308984de83` stopped when Go's default ten-minute package alarm expired.
The system-test package reported `600.163s`, with 120 top-level tests already passed and no individual assertion failure.
The final top-level test, `TestUploadRecoveryRealSwift`, had run for 54 seconds; its 201-name `delete_plain` case had run for 31 seconds.
The stack shows that case inspecting the destination namespace through the independent witness after the terminal job assertions, rather than a test stuck at a new item-access operation.
That witness uses a thirty-second HTTP request timeout; its separate requests and complete namespace inspection consume part of the overall package budget.
The package alarm interrupted the remaining recovery cases and the scenario report correctly rejected the incomplete producer.
The aggregate correctly refused the failed integration job, so PR60 remained unmerged and master stayed at `1d4bd66f4f152ae05d277780e4b2f815745ad478`.

The [preceding green master integration run](https://github.com/csmart/aeronautes/actions/runs/34226579443/job/102062066546) completed the system-test package in 594.822 seconds and its final recovery test in 80.70 seconds.
That left only 5.178 seconds below Go's default package alarm.
PR60's seven new read-service tests took 6.52 seconds together; they are ordinary additional work within an already nearly exhausted package budget.
The previously approved local full producer completed the same PR60 system-test package in 488.543 seconds, including the final recovery test in 55.00 seconds.
These observations establish a suite-budget problem; they do not establish a performance improvement or excuse an individual assertion, deadline or request failure.
The behaviour oracle is the complete existing race suite and scenario reconciliation, the captured before-fix CI event stream and the protected merge contract in [CI](../CI.md).

## PR60 correction

The PR60 correction set `-timeout=15m` for each Go package and `timeout-minutes: 20` for setup, execution and report upload together.
The fifteen-minute package budget adds headroom for the measured sequential real-Swift fault and collateral checks while retaining a finite deadline.
The job limit also replaces GitHub's much larger implicit overall job allowance for this lane.
No application code, test assertion, fixture size, request/child deadline, race or coverage flag, package selection, producer-exit check, scenario reconciliation or aggregate refusal changed.
PR concurrency, required check names and protected merge rules remain unchanged.

## PR60 validation and limits

The existing aggregate regression accepted success and rejected all nineteen incomplete-result cases.
The unchanged full internal race suite passed locally with the new package budget, required Secret Service fixture, pinned rclone and owned mock/two-account SAIO fixtures.
It passed 641 top-level tests, 1,864 leaf cases and 2,020 named tests across twenty packages, with zero skips or failures and 85.7% combined statement coverage.
The system-test package completed in 484.689 seconds; local timing does not promise the same performance on a hosted runner.
All 279 Go/SQL source hashes matched the reviewed item-access commit before and after the run.
The scenario reconciler accepted the complete producer at 38/116 cells, with gaps in 38/52 actions; the failed CI stream remains invalid and receives no credit.
All fourteen multiline workflow shell blocks passed Bash syntax checks, the four local documentation links resolve, and inspection confirms that only the two time budgets and their rationale changed in the workflow.
Independent local review, a new successful latest-head PR run and all five resulting master checks were required before this correction and the item-access slice were delivered.
This workflow correction supplies no new behavioural-matrix credit and does not close the valuable-data release hold.

## PR62 measured growth

The [first PR62 integration run](https://github.com/csmart/aeronautes/actions/runs/34262478581/job/102183610142), at reviewed source `c3a98bfb2f04af0815d646add7d519c688d1f318`, reached the fifteen-minute package alarm after 133 top-level system tests and 2,113 named tests across the internal suite had passed.
No individual assertion or test deadline failed before the package alarm.
The system-test package failed at 900.163 seconds while the last top-level test, `TestUploadRecoveryRealSwift`, had been running for 75 seconds.
Its `absent_plain` case had just started; the stack shows runnable SQLite schema creation in a new fixture, before that case's upload.
The stream shows that this sequential group had started its tenth case, after progressing through the 201-name cleanup-refusal case.
The report rejected the failed producer, and the aggregate blocked merging; master remained at the preceding verified green `44623f5dcc1232c2e06c44a6122ef1488bf05864`.

The [preceding master integration run](https://github.com/csmart/aeronautes/actions/runs/34250226792/job/102142468533) completed systemtest in 582.863 seconds.
Comparing completed top-level tests gives the following measured contributions; these are different hosted runs, so the common-test delta cannot be attributed solely to application changes.

| Completed system tests | PR61 master seconds | PR62 seconds |
| --- | ---: | ---: |
| Tests common to both completed sets | 507.21 | 612.34 |
| Eight new checked-record groups | Absent | 212.88 |

The new groups cover failed-name identities on both providers, required-record refusals, DLO recovery-write refusals and 201 unchanged names.
The final upload-recovery group is excluded from both completed sets; it took 74.49 seconds on the preceding master and 54.98 seconds in the successful local PR62 producer.
That complete local producer took 642.590 seconds for systemtest, and independent review repeated the same source successfully in about 643 seconds.
The hosted failure is therefore consistent with measured additional sequential work plus slower execution of existing tests; it does not show a new operation stuck for fifteen minutes.
These timings include fixture creation, exact retained-record reads, complete owned-namespace assertions and orderly reopening, beyond the transfer-only measurements in the [checked Copy experiment](2026-09-08-checked-copy-records.md).

The failed integration job used 19 minutes 17 seconds overall.
Its test step began at 18:21:44 UTC, and systemtest began at 18:25:00 UTC, about three minutes sixteen seconds later; compilation and other package work precede that package's own timer.
This leaves too little whole-job headroom to increase the package allowance alone.

## PR62 bounded correction and validation

The integration package budget is now twenty minutes and the whole-job budget twenty-five minutes.
This adds five minutes to each previous bound while preserving room for the observed setup and compilation cost.
Every test, fixture size, per-request and child deadline, race/coverage flag, package selection, producer-exit check, scenario reconciliation and aggregate refusal is unchanged.
Application and test source are unchanged from the independently approved Copy implementation.
Future suite growth needs measured investigation and a separate test-scheduling change if necessary; this is not permission for repeated unmeasured increases or for weakening durability assertions.

The unchanged final upload-recovery group passed separately against the owned two-account SAIO fixture: fourteen cases plus the parent, no skips or failures, in 54.54 seconds.
All 295 Go/SQL hashes still match the previously successful full producer and the reviewed implementation commit.
The full producer remains evidence of 2,130 named tests across twenty packages with no skips, under the stricter fifteen-minute package limit; it was not rerun or relabelled as a twenty-minute-budget run.
Workflow comparison confirms that only the two budgets and their rationale changed; all fourteen multiline Bash blocks pass syntax checks, and the existing aggregate regression rejects all nineteen incomplete-result cases.
Replaying the captured failed producer through the unchanged scenario reconciler returns failure with `valid: false`, names the failed system-test package and credits zero cells.

The original failed stream remains failed evidence and receives no scenario credit.
The correction required a new successful PR run on the reviewed head and all five resulting master checks.
The [checked Copy delivery record](2026-09-08-checked-copy-records.md#delivery) records those results and both measured integration durations.
Neither time-budget correction changes the valuable-data release hold.

## 2026-09-10 job-cap cancellation and correction

The [master run for the contrast delivery record](https://github.com/csmart/aeronautes/actions/runs/34446406312) was cancelled at the twenty-five-minute job cap on its first attempt.
Nothing failed: the integration job started at 06:42:22Z, its suite step ran for 23.9 minutes and was cancelled at 07:07:35Z, and the aggregate job then reported failure because a required job had not succeeded.
The Go, frontend and smoke jobs of that run succeeded, and the merged commit was documentation only, so no product change is implicated.
Re-running the failed jobs completed all five successfully on the second attempt, which is the only retry recorded here.

Eleven successful integration jobs ran that day across six pull requests and their master pushes, and nine of their event streams were measured.

| Measurement | Minutes |
| --- | ---: |
| Whole job, fastest and slowest of eleven | 20.4 and 22.5 |
| Suite step, fastest and slowest of eleven | 19.1 and 21.4 |
| System-test package, fastest and slowest of nine streams | 16.4 and 18.2 |
| Delay from job start to system-test package start, nine streams | 3.82 to 4.44 |
| Cancelled attempt, delay to system-test package start | 5.91 |
| Cancelled attempt, system-test package before cancellation | 19.28 |

The cancelled attempt's stream starts `internal/app` at 06:43:46.130229758Z and `internal/systemtest` at 06:48:16.549674529Z, and its last system-test event is at 07:07:33.226606874Z.
That package had run for 19.28 minutes without finishing, close to its twenty-minute alarm, and the job cap ended the step first.
Compilation and the other nineteen packages account for the delay before the system-test package starts, and on that slow runner the delay was 5.91 minutes against 3.82 to 4.44 minutes on the measured successful runs.
Every delay here is measured from the job's own start time as the API reports it, one to two seconds before its first setup step.
Both attempts of that run uploaded a scenario coverage artifact: the cancelled attempt is artifact 10140531583 of 303,143 bytes, distinct from the successful retry's 10141315306, and its 14,642 events agree with the same attempt's retained job log.

Two limits were therefore too close at once: the job cap against the observed job durations, and the package alarm against the observed system-test durations.
The correction raises the job cap to thirty-five minutes and the per-package limit to twenty-five minutes.
The package alarm still fires first, which is what preserves a Go stack for a hung package instead of an opaque cancellation: with the slowest observed start delay of 5.91 minutes, a twenty-five-minute alarm expires 30.91 minutes into the job, about 4.09 minutes before the new cap.
Twenty-five minutes leaves 5.7 minutes over the longest system-test run observed, the incomplete 19.28-minute one, and 6.8 minutes over the longest completed one.
Retaining the twenty-minute alarm alongside a larger job cap would have left 43.3 seconds of measured margin on that profile, which is why the alarm moved as well.
The stream stops while the package is still running, so it does not show whether that package would have finished within the remaining margin; the recorded fact is the margin, not a failure.
The portable component slice added 308 integration results that day, but its own runs finished in 21.0 and 21.8 minutes, so suite growth alone does not explain the cancellation.

No assertion, fixture, race or coverage option, producer-exit check, scenario reconciliation or aggregate refusal changes, and no other job's budget changes.
This correction does not change the valuable-data release hold, and it makes no claim about why a particular hosted runner is slow.
