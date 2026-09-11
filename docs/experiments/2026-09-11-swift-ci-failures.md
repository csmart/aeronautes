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

## Retained error-log collection

The collector now adds an eleventh probe for `/var/log/socklog/errors`.
It reads the retained `.s` and `.u` rotations in filename order and the required current file last, taking at most 10,000 lines per file before redaction and retaining the first four MiB of that result.
Later retained errors can be omitted when that limit is reached; this probe prefers the earliest retained window while the other probes continue keeping their final window.
The reader replaces its launcher process and has an eight-second deadline inside the container, followed by a forced stop after one further second if needed, within the existing ten-second client deadline.
This bounds the reader independently of whether stopping a Docker client stops its container process.
A missing current file remains a nonzero probe even when earlier rotations were read successfully; later independent probes still run.
There is no new shell interpolation, Swift configuration change, suite retry or change to the workflow's two-minute collection budget.

All thirteen collector tests pass.
New disposable-file controls prove an earlier rotated HTTP 503 survives more than 10,000 later cleanup lines, both rotation suffixes are retained, unrelated state files are excluded, authentication material is redacted, an unrotated current file works, and a missing current file is reported before continuing the next probe.
The three new tests first failed because the probe was absent.
A blocked-file read also failed its control before the internal deadline was added; it now returns a nonzero command result before the outer probe times out.
Separate sensitivity controls against the final twelve-test suite removed rotations, the current file or the internal deadline: they produced two, four and one assertion failures respectively, with no test execution errors.
The actual probe command also succeeded against the pinned local image's empty error log, using the owned Podman fixture instead of Docker.
An isolated blocked-file control inside that same image exited 143 after 8.108 seconds without triggering the outer timeout; the normal probe then exited zero after 0.132 seconds.
That empty-log observation verifies command and path availability; it does not establish a server-failure diagnosis or replace the next hosted artifact inspection.

Independent review found that the initial implementation's final four-MiB window could still discard an earlier rotated error.
A new control with more than six MiB in the current file reproduced that loss and now requires the earlier rotated error to survive collection without exposing its token.
The first-window selection applies only to this error-log probe and each record reports its selected window alongside truncation status.

A subsequent marked synthetic `local2.err` message reached the pinned image's Swift and general logs but not its separate error log.
An isolated invocation of the image's `s6-log` reproduced its filter mismatch: the original `+\..err:` rule missed that severity, while `+\.err:` retained it.
Neither invocation changed the running logger or Swift configuration.
This is a newly reproduced diagnostic-routing gap, not a cause of the hosted HTTP 503 responses; reliable error capture still requires its own reviewed correction before another hosted investigation.

The complete pinned local internal suite subsequently exited zero after 838.888 seconds with Go 1.27.1, `-race -count=1 -parallel=3 -timeout=25m` and internal-package coverage enabled.
Its 3,746 terminal test names contain 3,481 passing leaves and one explicit `TestRcloneMatrix` skip; all twenty packages passed.
The event stream has SHA-256 `254bbe478546613dd420a1628354dbb800340fbc1d0ea3637358fb743d350f34`.
The run began at `c4574c49`; documentation and collector files changed while it ran, but Go and module files remained identical throughout.
The same local runtime and listener differences described above apply, and this pass does not establish the causes of the three hosted failures.

## Logging-rule correction plan

The new logger control requires a narrow exception to the earlier unchanged-image-configuration constraint: correct only the separate error-selection rule in the disposable CI fixture.
Keep every Swift request/storage setting, authentication setting, worker setting and image digest unchanged.
This changes which messages reach an existing diagnostic file, not the response or data path under test.

Add a small startup helper which accepts one exact original `+\..err:` line or one exact already-corrected `+\.err:` line in `/etc/socklog.rules/~-errors`.
For the original form, replace only that line while preserving every other byte; for the corrected form, perform no write.
Missing, duplicate or ambiguous forms must fail before executing the image's original `/init`.
Create the pinned container, copy the reviewed helper into it and start it through that helper, so no running service needs its logger restarted or rules replaced.
Keep the existing readiness check and five-job gate; startup failure must remain a failed integration setup.

Tests must cover the one-line correction, the already-corrected control, preservation of unrelated rules, rejection without writes and refusal to start the image after a configuration error.
Verify workflow syntax and the unchanged suite/gates.
Against a fresh owned container from the exact pin, emit a clearly marked synthetic error through its real syslog socket, then require that marker in the redacted error-log observation.
Keep the synthetic control separate from actual test failures, verify the ordinary Swift readiness endpoint, and retain actual commands and exits.
Obtain independent local review of the helper, workflow and evidence before the next complete hosted run; then inspect the hosted error-log artifact before claiming the diagnostic path works there.
This plan neither diagnoses the historical HTTP 503 responses nor permits a merge or removal of the valuable-data hold.

## Startup correction and local routing evidence

The startup helper now validates whole byte lines, corrects the single original selector, and preserves every other byte before executing `/init`.
It leaves the one known corrected form untouched and raises an error for missing, duplicate, ambiguous, commented, embedded or differently terminated selector lines.
CI creates the pinned container, copies the helper, then starts the helper as its entrypoint; the existing readiness check, suite, limits and required job names are unchanged.
Six helper tests pass, including seven refused rule forms and initialization refusal after configuration errors.
Separate omission controls catch a missing correction with one assertion failure and ignored configuration errors with two; neither control fails to execute.
Workflow linting, the thirteen collector tests, whitespace checks and the nineteen aggregate refusals pass.

A fresh pinned local container with unexpected rules exited one before image initialization.
A second fresh container started normally, answered `/info`, and routed a clearly marked synthetic `local2.err` message into the redacted error-log observation.
The helper's SHA-256 was `86daf43665f78d6be1b03a37ca0fd2ac87891fe79889d7614d205661bab8a46b`; the captured error observation has SHA-256 `2bea766a9b3bb5e439f8ea4a7aa3f74bdcd4ec132217055a2c3657261c356ea9`.
The initial local launcher's quoting error and failed readiness are preserved separately; that failed setup is not counted as a passing container check.
The corrected launcher used the same rootful Podman and loopback-listener adaptations already disclosed above, without mounting host directories or credentials.

The unchanged `TestCopySegmentChangeDetectionRealSwift` then passed against this fixture with Go 1.27.1 and `-race -count=1 -parallel=3`, in a command lasting 65.224 seconds.
Its event stream has SHA-256 `9a11822ff377857973ca280a8f2c1117d53414bea50872821d7d483fba189edd`.
This is a focused control after the logging correction, separate from the earlier full-suite pass.
The next hosted run must still demonstrate the corrected startup and artifact capture in Docker, and every required PR/master check remains mandatory.
The logging correction explains a loss of diagnostic evidence, not the earlier HTTP 503 responses.

## Fourth hosted run: extended-attribute failure

[Run 34583178040](https://github.com/csmart/aeronautes/actions/runs/34583178040), attempt one on source `08a3454bdb8c16795168118ee531e49a5e7c3dc8`, completed with failed integration and aggregate checks.
Go, frontend and smoke passed.
The integration test step ran from 09:15:32 to 09:31:49 UTC on 11 September 2026.
Its 3,746 terminal test names contain 3,476 passing leaves, two skips and four failed leaves: `TestCopySegmentChangeDetectionRealSwift`, `TestCopyRefusesOverwritesRealSwift`, and `TestCopyItemRecordsRealSwift/failed_1` and `/failed_401`.
The raw job log has SHA-256 `7314a80d8d5c0aa9f5616ac6cf44182a304ad8c809ab9e97f1cf7e2031a5b901`; the uploaded event stream has SHA-256 `c8ab725bc669c490796f6095b5302a308e529a2dcf4e4c6eda2afda9024a2f02`.
The scenario report correctly refuses the failed producer and names its workflow checkout `2349a3510e5e4ff533eb575c1fb0aa758c562a90`, which is GitHub's temporary merge commit rather than the source head.

The corrected image startup succeeded, and all eleven diagnostic probes exited zero without a timeout or output truncation.
The separate error-log observation contains 1,857 output bytes and has SHA-256 `bb0d102bdba1b5ef9fd9a8b91fae3834065f0e123009ca24171496fc83c6d538`.
It records this sequence:

1. At 09:30:38.532, `swift.obj.diskfile.write_metadata` fails in `xattr.setxattr` with `OSError: [Errno 28] No space left on device`.
2. The same transaction, `tx8d819e1e57054e1aa6c3e-006aa3ca3e`, produces backend 507, a sixty-second error exclusion of `127.0.0.1:6200/swift-d5`, and a proxy PUT 503.
3. The Copy publication assertion reports its partial outcome at 09:30:38.743.
4. Proxy GET 503s at 09:30:38.936, 09:30:47.294 and 09:31:31.551 coincide with the three other failing leaves, while that node remains excluded.

The after-suite snapshot reports about 10.5 GiB of available disk space, thirteen percent inode use, 6,468 MiB available memory, no container restart and zero cgroup out-of-memory events.
Those resource observations are after the failed requests, not measurements of every request's resource state.
The captured stack identifies an extended-attribute write failure; it does not identify which filesystem limit was reached, the backing filesystem type, or the exact metadata-boundary case executing then.
The access-log window again contains later cleanup only.
The preceding three runs have no equivalent error record, so their causes remain unproved.

The [Linux extended-attribute documentation](https://www.man7.org/linux/man-pages/man7/xattr.7.html) describes filesystem-specific storage limits, and the [setxattr error contract](https://www.man7.org/linux/man-pages/man2/fsetxattr.2.html) includes insufficient extended-attribute space as `ENOSPC`.
The [upstream SAIO guide](https://docs.openstack.org/swift/latest/development_saio) specifies XFS storage for tests.
The hosted fixture currently stores its data in Docker's writable overlay.
The local pinned fixture uses a different overlay implementation; the local workspace is XFS, and temporary files in both that workspace and local `/tmp` successfully round-trip attribute values from 2,048 through 60,000 bytes.
These differences support investigating metadata-storage capacity; the local control does not reproduce the hosted failure.

## Dedicated fixture storage plan

Use a fresh four-GiB sparse backing file and a dedicated mount directory beneath the job's temporary directory.
Format only that new file as XFS, using the formatter already present in the exact pinned image, and mount it through a loop device on the disposable runner.
Do not format a pre-existing block device, edit host filesystem configuration or change another container's storage.
Bind the mounted filesystem to the fixture's existing `/srv/node` path and preserve the pinned image's Swift user ownership and device initialization.
Keep the image digest, original startup, reviewed logger correction, authentication, rings, request/storage settings and worker configuration unchanged.

Before any suite starts, check the actual mounted filesystem and require a temporary file on the Swift data path to round-trip a 65,536-byte metadata attribute alongside its checksum-sized attribute.
The pinned `write_metadata` implementation writes chunks up to that size; successful file creation alone cannot establish this capability.
Use the fixture's service user for this check, remove the probe file, and refuse startup on a failed write, readback, ownership check or unexpected mount.
Record the filesystem type and the storage-check result without credentials or object bodies.
Bound formatter, mount, probe and teardown commands; retain the existing thirty-five-minute job and twenty-five-minute package budgets.

Always collect the existing diagnostics before removing the owned container and unmounting this job's filesystem.
Setup and teardown failures must remain visible, and a failed suite must remain failed.
Keep every metadata-count and metadata-byte boundary scenario, application assertion, producer result, scenario reconciler, concurrency setting and required check unchanged.
This storage correction grants no retry, transfer or cleanup authority to the application.

Validate the real storage check on an owned fixture, including caught write/readback failures, and lint the workflow.
Exercise formatting, mount identity and cleanup where the runtime permits actual loop mounts; record a missing capability as a local evidence gap rather than a pass.
Obtain independent review before pushing, then run the complete hosted suite and inspect the actual mount, probe and diagnostic evidence.
No merge is permitted until all five current-head/current-base PR jobs pass; verify all five resulting master jobs afterwards.
The valuable-data hold remains in place.

## Dedicated storage implementation and local controls

The integration job now formats a fresh four-GiB sparse file with the pinned image's formatter, mounts it as XFS, and checks both the mount type and its loop device's exact backing-file path.
It gives the Swift service user ownership before binding the filesystem over `/srv/node`, so the original image initialization still creates its six device directories.
The storage helper runs as that service user before Swift starts and requires one writable XFS filesystem-root mount, ownership by UID/GID 1000, and simultaneous exact readback of a 65,536-byte metadata attribute and a 32-byte checksum attribute.
It removes its temporary probe on success and on write or readback failure.
Formatting, mounting, probing and teardown have command and step deadlines within the existing job budget.

Teardown runs after diagnostic and scenario artifact collection.
It removes only the three named job containers before attempting a normal unmount, then removes this job's backing file and empty directories.
Failed container observation, container removal, mount observation or unmounting refuses backing-file deletion.
An interrupted setup with no data directory is accepted for cleanup; an unexpected file or symlink at that path is refused.
The first cleanup control exposed a missing-directory failure, which was corrected before review.

The eight storage-helper tests pass.
Six separate omission controls remove the mount or ownership checks, remove their invocation, reduce the metadata payload to 4,096 bytes, or omit the exact readback comparison.
Every control produces assertion failures with no test execution errors.
The maintained workflow-cleanup test exercises nine cases using fake container and mount commands with real files confined to disposable directories.
It checks successful cleanup, partial setup, unexpected path entries, failure preservation and removal of all containers before unmounting.
Substituting the original cleanup body into a private copy reproduces the maintained missing-directory assertion failure.
Two further controls which ignore container-removal or unmount errors each fail the corresponding refusal-exit assertion, with no test execution errors.
The CI workflow passes linting, and the aggregate gate still accepts success and rejects nineteen incomplete result combinations.

The exact pinned image's `/sbin/mkfs.xfs` successfully formatted an owned four-GiB sparse file locally.
The subsequent mount command failed with exit 32 because this runtime could not set up a loop device; the metadata admission helper did not run on that file.
The owned formatter container was removed, and a subsequent mount observation found no mount at the target.
This is an incomplete local fixture validation, not a successful loop-mount test.

A separate owned XFS-directory bind in the pinned image successfully ran the real metadata writes and readbacks as UID/GID 1000, leaving no probe file.
The same control then required the helper's mount check to reject that ordinary subdirectory as unsuitable for the dedicated fixture.
The helper had SHA-256 `d09f96438144ab711511b45cae4cee7a6d2a8c9903359c64af594c9789f52ca6`.
This proves the attribute capacity and refusal on that local bind only; it does not validate the missing loop-mounted path.
The local runtime used rootful Podman with the previously disclosed host-network adaptation, while hosted CI uses Docker with isolated formatter and probe containers.

No Go or module file changed from the previously reviewed application implementation.
The metadata-boundary scenarios, producer result, scenario reconciliation, image pin, Swift settings, concurrency, time budgets and five required checks are preserved.
Independent implementation review and a complete hosted run must still establish actual mount identity, successful admission, the unchanged suite and visible teardown before any merge.
The earlier hosted causes remain unproved, and the valuable-data hold remains in force.
