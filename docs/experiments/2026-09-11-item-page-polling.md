# Retained item pages and unchanged job polls

## Hosted observation

[PR93 run 34599308939](https://github.com/csmart/aeronautes/actions/runs/34599308939), attempt one on `f466349feb36917c66871569cd1b4df879f903ac`, failed the frontend coverage job with 1,129 passing tests and one failed test.
The failed test, `pages exact download destinations and temporary paths without acquiring rerun authority`, expected its final download marker row but observed the first file row again.
The expected quoted key was `"scope\n<script>/"`; the observed key was `"scope\n<script>/étude-0.bin"`.
The test lasted 2,536 milliseconds and failed at `itemRecords.test.ts:267`.
The retained frontend job log has SHA-256 `b4441d388c44324554f6072540e3322b3f750578fbb274c1b7ad15d1a269b6fe`.
No frontend source or dependency file changed between current master `1b520ba2` and that PR head.

The new Swift storage setup and admission steps passed before the integration suite began.
That suite and teardown were still running during this frontend investigation; their outcome is separate evidence.
The frontend failure cannot be made acceptable by a successful storage check.

## Deterministic control and oracle

The unchanged local item-record file passed all 31 tests in 6.43 seconds.
A private copy then appended the [preserved control](probes/2026-09-11-item-page-polling.test.ts.txt), using two rows, the actual drawer and the existing 1,500-millisecond poll under fake timers.
After navigating to the second item page, an unchanged terminal-job poll loaded cursor `""` and replaced that page with the first row.
Both returning the original fixture array and returning fresh job DTOs produced the same assertion failure as the hosted test, with no test execution errors.
The two controlled failures took 1.60 seconds; the unselected original tests were explicitly skipped by the focused command.
An earlier incomplete private dependency copy failed to import a package; that setup failure is not counted as behavioural evidence.

The reset effect reads `job.id` directly, so replacement of the reactive job value can rerun it even when the ID and active/terminal classification remain unchanged.
A private hypothesis control introduced a derived primitive job ID and made the reset effect depend on that value.
Both timer cases then kept their selected page without another item read, and all 33 tests in that private file passed in 6.58 seconds.
This supports the proposed correction; the repository implementation still requires its own review and validation.

[ADR 0030](../adr/0030-copy-item-recording.md#complete-access-and-interrupted-records) requires complete bounded navigation, first-page refresh when an active job becomes terminal, and rejection of stale job, page and account responses.
[ADR 0033](../adr/0033-download-item-recording.md) reuses that view for exact download destinations, temporary locations and missing observations.
An unchanged status poll must preserve the selected item page and any current page request.
A real completion, explicit refresh, changed identity or recovery after unavailable status retains its existing refresh and invalidation behaviour.

## Bounded correction plan

1. Record this observation, preserved failing control and behaviour oracle, then obtain independent review before changing the component.
2. Depend on the primitive job identity and active/terminal classification in the existing reset effect.
   Preserve the polling cadence, asynchronous request identities, unavailable-status handling, explicit page navigation and all mutation/rerun refusals.
3. Maintain deterministic cases for unchanged polls with both reused and fresh job values, including terminal and active jobs and a page request still pending during the poll.
   Preserve the complete 401-item download assertions and the existing completion, missing/error recovery, stale-page, stale-job and account-switch regressions.
4. Prove that the maintained cases fail on the prior component and pass with the correction; run the full frontend suite with coverage, type checking and a production build.
   Obtain independent implementation review, then require all five jobs on the new PR head and all five resulting master jobs through the existing protected merge procedure.

This fix changes presentation lifetime only and adds no transfer, rerun, overwrite or cleanup authority.
The valuable-data hold remains in force.

## Maintained correction and validation

The component now derives its primitive job ID and reads that value in the existing reset effect.
The active/terminal dependency, poll cadence, request sequence checks and unavailable-status handling are unchanged.
Ordinary status updates still refresh the job counters while preserving the selected item page and any pending page read.

Eight maintained cases cover active and terminal jobs, reused and fresh job values, and a completed or pending second-page read during the existing timer tick.
They require both status polls to execute, updated counters to remain visible in browser state, no replacement item/totals request, the selected page to survive, and a pending read to finish on that page.
The existing 401-row download test now explicitly triggers the poll before checking the last page, preserving every original identity, location, observation, count and refusal assertion.
All nine cases fail their page or identity assertions against the prior component, with no test execution errors.
The corrected item-record file passes all 39 tests.

Four independent omission controls remove completion refresh, missing-status refusal, recovery reload or stale same-job page-response rejection.
Each produces the expected behaviour assertion failure in the existing tests, for five failing cases in total, without a process, setup or test timeout.
These controls keep the distinction between stable-page preservation and the events which must invalidate or refresh retained records.

The full frontend coverage command, with two local workers, passes all 1,138 tests in 29 files.
Type checking reports zero errors and zero warnings, and the production build passes.
Measured whole-source statement coverage is 90.8 percent; `JobItems.svelte` has 100 percent line coverage and 87.71 percent branch coverage.
These local component checks do not establish native platform coverage.
Independent implementation review and successful current-head/current-base PR and resulting master pipelines remain required before delivery.
