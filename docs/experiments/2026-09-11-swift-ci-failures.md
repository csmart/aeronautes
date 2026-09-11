# Swift integration failure investigation

## Observations

PR93 remains unmerged at source `bca217c50be57a5807aeec3048f30b2f957590d5`, incorporating master `1b520ba2a80f281d5945250548137be7498a1f02`.
The [first integration attempt](https://github.com/csmart/aeronautes/actions/runs/34567370623/job/103162139411) and [second attempt](https://github.com/csmart/aeronautes/actions/runs/34567370623/job/103166908781) both failed.
Go, frontend and smoke succeeded; the aggregate correctly refused integration's failure.
The second attempt reused those three successful jobs and does not represent five newly executed jobs.

Each retained event stream contains 3,746 distinct terminal test names, with 3,472 passing leaves, eight failing leaves and two skipped leaves.
A leaf excludes every executed slash-separated ancestor, including ancestors implicit in a slash-containing subtest name.
Only the system-test package failed, after 1,056.941 seconds on attempt one and 1,189.390 seconds on attempt two; neither ended at the package timeout.

| Attempt | Failing leaf names, all in `internal/systemtest` |
| --- | --- |
| 1 | `TestCopySegmentChangeDetectionRealSwift`; `TestCopyRefusesOverwritesRealSwift`; `TestCopyItemRecordsRealSwift/failed_201` and `/failed_401`; `TestCopyInterruptionRealSwift/cancel_after_both`, `/cancel_after_refusal_failure` and `/cancel_refusal_control`; `TestCopyCompletionRealSwift/refused` |
| 2 | `TestCopySegmentChangeDetectionRealSwift`; `TestCopyItemRecordsRealSwift/failed_201` and `/failed_401`; `TestCopyCompletionRealSwift/mixed`, `/terminal`, `/audit`, `/planning` and `/verifying_failed` |

All eight first-attempt failing leaves report HTTP 503, including independent fixture reads and the new publication assertion.
On attempt two, six failing leaves explicitly report HTTP 503.
The `/terminal` and `/audit` cases instead report one failed and unresolved item where the fixture expected two successful items; their output does not retain the item's underlying diagnostic.
The database refusal messages in those two cases are expected injected faults, so they do not establish the cause of the unexpected item outcome.
Those two failures remain separately unexplained.

The first raw integration log is 6,756,669 bytes with SHA-256 `8c2a7f68a73d782deb50c67a092c1bd73fad6c9c13ec2d4e2dada22c654c38d5`.
The second is 6,758,274 bytes with SHA-256 `e7db83a074e47cb67c1a313807c1eab53f642891bc3fd291345941911b5181a5`.
Both logs, API job metadata and reconstructed JSON event streams are retained with the investigation.
The workflow saves test events and scenario reports, but it collects container logs only when Swift fails its initial readiness check.
No post-test server logs, container state, filesystem availability or memory observation survives either failure.
HTTP 503 establishes service unavailability at those requests, not its cause, and the available evidence does not establish an external outage, resource exhaustion or an application defect.

## Oracle and sequence

The [CI merge procedure](../CI.md#merge-procedure) requires investigated failures and all five current-head checks to succeed before merge.
The next change adds evidence collection, preserving the complete suite, race and coverage settings, producer exit, scenario reconciliation, aggregate refusal, pinned Swift image, concurrency and existing time budgets.
It changes no application behaviour and grants no retry or destructive authority.

After independent review of this sequence, add a small collector for the owned CI container's timestamped server output, limited state and resource observations, and the runner's filesystem and memory availability.
Bound each command and the whole collection step, record unsuccessful or timed-out probes explicitly, and still attempt the remaining probes when one fails.
Exclude environment dumps, credential files, application databases and object bodies; redact authentication material if it appears in server output.
Upload the diagnostics after failed as well as successful suites, without converting an original test failure into success.
Exercise normal collection and missing-container, failed-command and timeout controls, and inspect the saved artifact from the next hosted run.

Use the next complete PR run as a measured investigation of the retained failure, then repair only what its evidence supports.
Preserve any recurring failure instead of retrying indefinitely or weakening assertions.
If the next run succeeds, record that result separately from the still-unproved historical causes.
The valuable-data hold and the remaining DS-04 content-verification work stay open.

## Collector implementation and local verification

The collector now runs after the suite with `always()`, followed by a separate artifact upload with the same condition.
Each of ten probes has a ten-second timeout, a UTC timestamp, an actual exit or explicit timeout/error, and at most four MiB of redacted output.
The two log probes request the final 10,000 lines; the step has a two-minute limit within the unchanged thirty-five-minute integration job budget.
Completed probes are saved individually so a later command failure does not erase their evidence.
A collector exit of zero means it wrote the requested observations, not that every diagnostic command succeeded.
Output-directory or artifact-write errors still fail collection, and the existing test-step result remains independent of collection.

The probes read limited container identity/state, container statistics, container output, Swift's internal log, cgroup memory events, and container/runner filesystem and memory observations.
The [upstream log initialization](https://github.com/openstack/swift/blob/fe1ae77c33170927fe5629ca10aa0d1e165b85c2/docker/rootfs/etc/cont-init.d/01_swift_logs) identifies `/var/log/swift/all.log` as Swift's internal log link; the next hosted artifact must confirm availability in the pinned image.
Tempauth tokens and authentication-header lines are removed before any output truncation or file write.
No environment, configuration, database or object-content files are read.
Missing log paths, unsupported cgroup files, truncated tails and commands interrupted by the time limit remain visible gaps.
A runner that disappears or exhausts the whole job budget cannot be assumed to finish collection or upload.

Eight focused Python tests passed using disposable child processes, including nonzero command exits, missing commands, partial output on timeout, continuation after timeout, token/header redaction, UTF-8 truncation and refusal of an unusable output path.
The missing-container response is a simulated command result in those tests, not an observed Docker failure against the pinned image.
The actual local CLI run wrote ten readable JSON records: seven explicit missing-Docker errors and three successful runner probes.
The local Swift HTTP fixture is reachable but its Docker runtime is unavailable to this session, so container log/resource capture remains a hosted verification requirement.
Workflow linting and `git diff --check` passed, and the existing aggregate regression still accepts all-success and refuses its nineteen incomplete-result cases.
The complete local internal race/coverage replay is separate from these collector checks and does not replace the required hosted run.
