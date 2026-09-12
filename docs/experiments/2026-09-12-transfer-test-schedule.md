# Transfer test scheduling

Status: eight outer groups use the existing three-slot schedule and complete local verification has passed; independent implementation/range review and hosted delivery remain pending.
This changes test scheduling only and cannot establish valuable-data or native-platform acceptance.

The [last master CI run](https://github.com/csmart/aeronautes/actions/runs/34689630364) passed all five jobs at `2f576f77`.
Its system-test package took 1,478.204 seconds of the existing 1,500-second limit; the integration job took 1,768 seconds of its 2,100-second limit.
The package had only 21.796 seconds remaining.

The combined implementation at `836ad0d2` adds eight outer groups that currently execute sequentially before Go resumes parallel tests.
At the before observation, their completed events in a still-running local full race suite totalled 103.900 seconds and 35 executed passing leaves, with no skipped leaf in those groups.
The [source-bound before record](probes/2026-09-12-transfer-test-schedule.before.json) names each group, its elapsed time, source hashes and the complete-line partial stream snapshot.
This is a measured local scheduling cost, not a prediction that hosted CI will take the same time or evidence that this full process has succeeded.
The small prior hosted margin warrants checking scheduling before publishing the enlarged suite.

## Oracle and bounded change

The existing [CI schedule](../CI.md) permits independent groups to share three slots while each group retains sequential cases and per-harness resources.
The eight candidates are the mock and real-Swift child-lifetime groups, mock and real-Swift staged-content groups, their two fault groups, and the two real-Swift representation groups.
No unrelated group, fixture, deadline, assertion, coverage option or workflow command is in scope.

`newHarness` gives each instance its own database, services, connection bindings, temporary paths and random remote namespace.
The mock supplies explicit instance configuration and an owned HTTP server instead of changing process environment.
The child-lifetime controller is a separate re-executed process with its own outcome path; its subreaper setting never applies to the parent test suite.
Each outer group keeps its cases sequential, including its stopped-child and parent-kill cases, and cleanup must finish before that group's slot returns.
If verification exposes shared process state or resources, stop and correct the proposed grouping before enabling overlap.

1. Review the measured before evidence and isolation boundary before editing the test entry points.
2. Add only `t.Parallel()` at each approved outer entry point.
   Preserve all scenario bodies, parameters, assertions, process checks, mock/Swift selection and individual deadlines byte-for-byte.
3. Run the complete race suite with the existing `-parallel=3` and package limit, real built frontend assets, both owned Swift accounts and the accepted engine pin.
   Reconcile the full named and leaf inventory, actual producer exit, cleanup results and source stability against the original combined run.
   Check the outer pause/continue events and verify the shared parallel group count never exceeds three.
4. Obtain independent review of the resulting source and complete range, then require all five current-head/current-base PR and resulting-master jobs.
   Record both hosted package and job margins; a local timing improvement alone cannot satisfy those gates.

The original full-run result and any failed or skipped evidence remain separate from later scheduling checks.
No repeated attempt may turn a failed producer into success, and no safety or acceptance finding closes through this scheduling change.

## Complete local verification

The implementation adds exactly eight `t.Parallel()` calls across the three approved test files.
Removing those calls restores every byte of their baseline sources; all other executable inputs and real built frontend assets are unchanged.
The [after record](probes/2026-09-12-transfer-test-schedule.after.json) binds both complete producer results, source snapshots, streams, generated-name mappings and the independent inventory reconciliation.
The candidate ran at plan commit `1a918577` with the recorded eight-line patch; this subsequent evidence update does not change the executed Go sources.

| Complete race run | Baseline `836ad0d2` | Scheduled candidate |
| --- | --- | --- |
| Producer exit / timeout | 0 / no | 0 / no |
| Named passes / skips | 4,342 / 1 | 4,342 / 1 |
| Leaf passes / skips | 4,010 / 1 | 4,010 / 1 |
| System package seconds | 859.570 | 823.778 |
| Whole producer seconds | 1,032.868 | 992.823 |
| Eligible outer parallel groups | 52 | 60 |

All 35 scenario leaves in the newly scheduled groups executed and passed in both runs, including the real-Swift child-lifetime and representation cases.
The only named skip was the optional `TestRcloneMatrix`.
Both streams have one terminal result for every started test and package, no failure, empty stderr and unchanged executed sources.
Formatting, imports and whitespace checks also passed for the eight-line change.

The initial literal inventory comparison exited 1 because two existing tests embed generated values in their names.
`TestForceReauthChangesTheToken` uses the current time plus one hour, and `TestDestinationRootOpenDirectory` uses its owned temporary outside path.
The reconciliation checks the actual timestamp and run-owned path, verifies both test source files are unchanged, and maps only those two exact pairs without collisions or omitted cases.
All 4,343 named scenarios and outcomes then match; the original literal names and failed comparison remain recorded.

Go's output ordering is not an exact count of occupied parallel slots.
The pinned scheduler releases a slot before reporting the completed test, so `CONT` minus terminal events briefly reaches four in both runs; the corresponding leaf counter reaches three/four.
Those counters include completed work awaiting its report and are retained as observations, not relabelled as a measured three-group maximum.
The unchanged `-parallel=3`, pinned Go scheduler and sequential inner cases enforce the existing three-slot policy, while the reviewed per-harness isolation permits the overlap.
The source hash and precise reporting limitation are recorded for independent review.

The local reduction is 35.792 seconds in the system package and does not predict hosted timing.
All five current-head/current-base PR jobs and the resulting-master jobs must still pass, with both hosted package and job margins checked before delivery is complete.
