# Transfer test scheduling

Status: proposed use of the existing three-slot schedule, pending independent review and implementation.
This changes test scheduling only and cannot establish valuable-data or native-platform acceptance.

The [last master CI run](https://github.com/csmart/aeronautes/actions/runs/34689630364) passed all five jobs at `2f576f77`.
Its system-test package took 1,478.204 seconds of the existing 1,500-second limit; the integration job took 1,768 seconds of its 2,100-second limit.
The package had only 21.796 seconds remaining.

The combined implementation at `836ad0d2` adds eight outer groups that currently execute sequentially before Go resumes parallel tests.
Their completed events in a still-running local full race suite total 103.900 seconds and 35 executed passing leaves, with no skipped leaf in those groups.
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
