# Swift integration failure investigation

## Observations

The first two PR93 attempts used source `bca217c50be57a5807aeec3048f30b2f957590d5`, incorporating master `1b520ba2a80f281d5945250548137be7498a1f02`.
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

## First hosted diagnostic capture

The [next complete run](https://github.com/csmart/aeronautes/actions/runs/34574614183) used source `c4574c49c9fb37cb8c7ef8b78907019d27614cb0` and the same master base.
The application and module files were unchanged from the first two attempts.
Go, frontend and smoke passed; integration failed and the aggregate refused it.
Its 3,746 terminal test names contain 3,479 passing leaves, one failing leaf and two skipped leaves.
The only failing leaf is `TestCopySegmentChangeDetectionRealSwift`, whose publication assertion recorded HTTP 503, a partial job and one failed object with no copied or verified objects.
The assertion appeared at 07:53:12 UTC and the test failed at 07:53:16 UTC, after 151.28 seconds.
The retained integration log has SHA-256 `348dd398585e47d31c285a4892881644aae2c559c79b25735d265474c361ce72` and its reconstructed event stream has SHA-256 `2bb3fc1f0b51baab50f5cc6b9cf5fa82f091bc49a5f93ca5cb0544fed8f095d0`.

All ten diagnostic commands succeeded without timeout or output truncation, and both diagnostic and scenario artifacts were uploaded.
Container state showed no restart or OOM kill, and all cgroup memory-event counters were zero.
The after-suite observations showed about 453 MiB of container memory, 6,522 MiB available runner memory, about 10.5 GiB free filesystem space and 13 percent inode use.
These observations do not measure every resource at the failing request and do not establish the HTTP 503 cause.
Docker output was empty; the Swift log contained only 242 lines from 07:54:13–14 UTC, during cleanup after the assertion.
Successful collection therefore did not preserve the failure window.

A local reproduction used the identical image ID, `sha256:2c39297ee7e8ff768d10ad970a982a89a87a44eaae5b9140ea80738e67c92d02`, from the unchanged CI image pin.
The unchanged real-Swift test passed with Go 1.27.1, `-race -count=1 -parallel=3`, in a command lasting 60.884 seconds; its event stream has SHA-256 `65f3dd615750314a60eb918d4018b80424561af94539acabfca9cf3336cd1644`.
This was an isolated run, not a full-suite or hosted acceptance result.
The local runtime and host resources differ from CI; its proxy listener was moved to a separate loopback port while the backend listeners remained on loopback.
The pinned image's log directories contained the current file and rapid rotations, and its logging rules also route errors to a separate `/var/log/socklog/errors` directory.
The isolated passing run left that error log empty.

The next bounded collector change will read that separate current error log and its retained rotations, preserving the existing access-log observation and resource probes.
It must retain an earlier error even when subsequent access traffic has rotated away, redact before truncation or artifact output, expose missing paths and read failures, and continue later probes after failure.
Use disposable log files to prove current and rotated error retention, missing-current refusal and authentication redaction before testing the pinned container command.
Keep the same ten-second command limits, two-minute collector step, image configuration, suite, concurrency and test/merge gates.
Any rotations already discarded by the image remain an explicit evidence limit.
PR93 remains draft and unmerged, the HTTP 503 causes remain unproved, and the valuable-data hold remains in force.
