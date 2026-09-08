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

## Bounded correction

The integration command now sets `-timeout=15m` for each Go package, and the integration job sets `timeout-minutes: 20` for setup, execution and report upload together.
The fifteen-minute package budget adds headroom for the measured sequential real-Swift fault and collateral checks while retaining a finite deadline.
The job limit also replaces GitHub's much larger implicit overall job allowance for this lane.
No application code, test assertion, fixture size, request/child deadline, race or coverage flag, package selection, producer-exit check, scenario reconciliation or aggregate refusal changed.
PR concurrency, required check names and protected merge rules remain unchanged.

## Validation and limits

The existing aggregate regression accepted success and rejected all nineteen incomplete-result cases.
The unchanged full internal race suite passed locally with the new package budget, required Secret Service fixture, pinned rclone and owned mock/two-account SAIO fixtures.
It passed 641 top-level tests, 1,864 leaf cases and 2,020 named tests across twenty packages, with zero skips or failures and 85.7% combined statement coverage.
The system-test package completed in 484.689 seconds; local timing does not promise the same performance on a hosted runner.
All 279 Go/SQL source hashes matched the reviewed item-access commit before and after the run.
The scenario reconciler accepted the complete producer at 38/116 cells, with gaps in 38/52 actions; the failed CI stream remains invalid and receives no credit.
All fourteen multiline workflow shell blocks passed Bash syntax checks, the four local documentation links resolve, and inspection confirms that only the two time budgets and their rationale changed in the workflow.
Independent local review, a new successful latest-head PR run and all five resulting master checks remain required before this correction or the item-access slice is delivered.
This workflow correction supplies no new behavioural-matrix credit and does not close the valuable-data release hold.
