# CI and protected merges

Every PR runs `.github/workflows/ci.yml`, including documentation and workflow changes.
Pushes to master and main run the same checks again.
New revisions of the same PR supersede its previous run; each master or main push has its own concurrency group and runs to completion independently.
The push group uses the run ID because a shared group can replace a pending run even when cancellation of a running job is disabled; see [GitHub's concurrency rules](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency).
The README status badge shows master push runs so that a green PR cannot hide a failing master.

## Required checks

| Check | Evidence |
| --- | --- |
| Go — fmt, vet, build, test (race) | Formatting, vet, application build, binding drift, Go race tests and isolated keychain protocol tests |
| Frontend — lint, build, test | Type checking, frontend build and tests |
| Integration — containerized Swift (SAIO) | Internal race tests against two Swift accounts with the pinned official rclone |
| Smoke — headless app against mock Swift | Built desktop application under Xvfb, including a real engine copy |
| CI — all required jobs passed | Every preceding job completed with `success` |

The aggregate uses `always()` so that a failed dependency cannot silently skip it.
It refuses failed, cancelled, skipped or missing job results.
Its shell regression test exercises each refusal for all four required jobs and runs in the aggregate job.
GitHub otherwise accepts skipped status checks as satisfying a merge requirement; see [GitHub's required-check troubleshooting](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks).

The Go job installs `dbus` and runs `make test-secret-service` with `AERONAUTES_REQUIRE_SECRET_SERVICE_FIXTURE=1`.
It also fetches the official pinned rclone, verifies the archive and embedded binary checksums, and exposes it to the test process through PATH and `AERONAUTES_OFFICIAL_RCLONE_DIR`.
Mock Copy tests exercise the actual transfer runner and require this dependency even without a SAIO endpoint.
Its Linux test package uses a separate binary with a per-run private daemon, no service activation, a short socket path in a mode-0700 directory, and bounded startup and teardown.
A missing daemon fails this required lane; an ordinary local suite without the requirement flag reports explicit skips when the tool is absent.
These tests exercise the pinned client against a synthetic Secret Service and cannot satisfy the native desktop keychain gates.

The release workflow's test job also fetches and verifies the official rclone pin before running Go tests.
It exposes the binary through the same PATH and `AERONAUTES_OFFICIAL_RCLONE_DIR` settings as ordinary CI because the mock Copy tests need the real runner there too.
Fetching sidecars in the later packaging jobs cannot supply this earlier, separate test job.
Manual release dispatch builds unsigned artifacts for all six targets; it does not create a tag or publish a GitHub release.

## Merge procedure

1. Finish the implementation, relevant local verification and independent local review.
2. Push a PR branch and confirm a `pull_request` CI run exists for its latest head.
3. Incorporate current master, then rerun local verification and independent review after any rebase or history rewrite.
4. Wait for the latest PR run to finish successfully and inspect all five required jobs.
   A skipped, cancelled, pending, missing or failed job blocks merging, and an older successful run does not replace a newer failure.
   Check the run's head and base against the PR head and current master.
   Investigate failures before rerunning; repeated attempts are not a fix for a reproduced test race.
5. Resolve review findings, refresh the PR state and merge only the verified head with the normal protected merge operation.
   Preserve separate logical fixes; squash only when the resulting commit remains one coherent change.
6. Check the resulting master push pipeline to completion.
   If it fails, stop feature merges, investigate and deliver the repair through the same reviewed PR process.

## GitHub configuration

[`.github/master-ruleset.json`](../.github/master-ruleset.json) is the reviewed specification for the repository's `master protection` ruleset (ID `21829446`).
It requires a PR, blocks deletion and force pushes, requires the latest master to be incorporated, and pins all five status checks to the GitHub Actions app (ID `15368`).
There are no bypass actors, including administrators, integrations or deploy keys.
GitHub documents the strict latest-base setting and check-provider binding in [available rules for rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets).
Local review remains a separate required workflow; the GitHub review count is zero because these reviews happen outside GitHub.

GitHub does not apply this file automatically.
After local review and a successful PR run containing any new required check, a repository administrator applies the file to the existing ruleset through the [ruleset API](https://docs.github.com/en/rest/repos/rules#update-a-repository-ruleset).
Read the ruleset and effective master rules back to verify the saved checks, provider, strict setting, branch target and empty bypass list before merging.
Changing protection to get a failing or missing check through is forbidden.

Coverage SVGs remain downloadable as `badge-go` and `badge-frontend` artifacts on each CI run; the scenario report and event stream are in `scenario-coverage`.
The `saio-diagnostics` artifact retains container and runner observations after successful and failed integration suites for seven days.
Each probe records its command, UTC start, elapsed time, exit or timeout, and redacted output; unsuccessful probes do not prevent the remaining probes from running.
Before the pinned image starts, a small helper corrects its demonstrated error-filter typo while preserving every other byte and then runs the original `/init`.
An already-corrected rule needs no write; missing, duplicate or ambiguous forms refuse startup.
Swift request, authentication, ring and worker settings keep their pinned values.
The disposable fixture stores its data on a dedicated four-GiB XFS filesystem beneath the runner's temporary directory, formatted only from a newly created sparse file using the pinned image.
Setup checks the loop device's exact backing file and requires a writable filesystem-root mount owned by the Swift service user.
Before starting Swift, that user must round-trip a full 65,536-byte metadata attribute and its checksum-sized attribute on the data mount.
Failed setup refuses the suite; formatting, mounting and admission checks have bounded deadlines.
After diagnostic and scenario artifact collection, checked teardown removes the three owned job containers before unmounting and removing the backing file.
Observation, removal and unmount errors remain failures and prevent backing-file deletion.
The storage-helper and workflow-cleanup tests run in the required Go job.
The collector bounds each of eleven probes to ten seconds, retains at most four MiB of redacted output per probe, and has a two-minute step limit within the existing job budget.
Swift's internal access log and container output each retain at most the last 10,000 lines before redaction and the output limit.
A separate probe reads the last 10,000 lines of each retained error-log rotation and the current error log, oldest first, then retains the first four MiB after redaction so later traffic cannot displace an earlier retained error.
Other probes keep their final four MiB; each record names its selected output window.
Missing files, unsupported cgroup files, discarded rotations and truncated output are explicit limitations.
It reads only the disposable `saio` container and runner resource state, excludes environment/configuration dumps and object bodies, and redacts tempauth tokens and authentication-header lines before saving output.
The [PR93 investigation](experiments/2026-09-11-swift-ci-failures.md) records why these observations are needed and keeps the earlier failure causes open.
CI does not commit these generated files or use `BADGE_DEPLOY_KEY` anymore.
The existing coverage floors are advisory; hard scenario and coverage gates remain audit work package P34.
Green CI is required evidence, and the wider safety and platform release gates still apply.

The real-Swift integration lane gives each Go test package twenty-five minutes and bounds the whole job to thirty-five minutes, including setup and report upload.
The job cap includes setup time and the compilation and other packages that run before the long system-test package, so inspect both limits and the measured start delay when diagnosing a timeout.
The growing sequential fault suite exceeded Go's default ten-minute package limit during PR60 and the fifteen-minute limit during PR62, and the twenty-five-minute job cap cancelled a master run on 2026-09-10; the [diagnosis and validation](experiments/2026-09-08-integration-time-budget.md) distinguish those suite budgets from individual test, request and child-process deadlines.
The change preserves every assertion, fixture, race/coverage option, producer-exit check, scenario reconciliation and aggregate refusal.

The [bounded SAIO schedule](experiments/2026-09-09-saio-scheduling.md) lets fourteen independent real-Swift groups overlap, including the [download destination](experiments/2026-09-09-download-root.md#delivery), [stage lifecycle](experiments/2026-09-09-download-stage.md#delivery) and [native closure regressions](experiments/2026-09-09-download-close.md), with `-parallel=3` on both Go CI commands.
Each group retains its sequential cases and per-harness database, services, local paths and remote namespaces; random namespace text avoids depending on clock resolution for isolation.
The [mock schedule](experiments/2026-09-09-mock-scheduling.md) gives mock fixtures explicit instance settings and adds thirteen existing counterparts to the same three slots, for twenty-seven initial eligible groups.
The [checked download record cases](experiments/2026-09-09-checked-download-records.md) add six groups for complete item histories, SQL refusals and empty/marker selections on both providers, using the same three slots.
The [transfer verification cases](experiments/2026-09-12-transfer-test-schedule.md) add eight independent outer child-lifetime, staged-content and representation groups to that existing schedule, retaining every inner case and deadline.
Their complete local race run preserves all scenarios and outcomes after accounting for two existing generated test names; hosted package and job margins still require measurement.
The remaining non-parallel tests finish before Go resumes those groups; no mock harness changes process-wide environment values.
Use `go test -race -count=1 -parallel=3 ./...` for the matching local schedule, retaining the configured owned Swift and optional fixture environment when exercising those lanes.
No tests or required jobs are removed, and the package/job limits and coverage/reconciliation settings above stay unchanged.
The [protected PR70 delivery record](experiments/2026-09-09-mock-scheduling.md#independent-review-and-protected-delivery) preserves the complete 2,486-test terminal inventory and both successful five-job pipelines.
Observed PR/master system packages take 792.682/826.057 seconds and integration jobs take 1,011/1,064 seconds within the unchanged limits.
Keep measuring the complete inventory and both hosted durations as maintained download cases are added.

PR108's [proposed fixture partition](experiments/2026-09-12-remaining-test-schedule.md#proposed-partition-across-fixtures) separates three heavy groups from the complementary internal suite while preserving all tests and limits.
Its Integration check will require both producers, complete disjoint execution and compatible combined coverage before success; the contract requires independent review before workflow changes.

## September 2026 incident

[PR17](https://github.com/csmart/aeronautes/pull/17) ran all four then-required checks successfully in [run 33974249668](https://github.com/csmart/aeronautes/actions/runs/33974249668), completed at 15:20:54 UTC on 5 September, before merge at 15:24:05 UTC.
The [master run](https://github.com/csmart/aeronautes/actions/runs/33974684916) then exposed a test ordering race: a terminal job row can become visible before its separate audit append.
[PR18](https://github.com/csmart/aeronautes/pull/18) fixes that test wait and a second test that mistook a final cancellation error response for an open gateway socket.
Both repairs passed independent local review and [PR CI](https://github.com/csmart/aeronautes/actions/runs/33993189107) before merging.
The [first repaired master run](https://github.com/csmart/aeronautes/actions/runs/33993862798) passed all four test jobs, then its badge push started another master run and cancelled the finishing workflow through the concurrency rule.
The incident did not result from skipping PR17's pipeline.
The protection review also found that strict latest-base checks were off and the coverage bot could bypass PRs with a deploy key; this slice closes those separate gaps.

[PR42](https://github.com/csmart/aeronautes/pull/42) also passed all five required jobs before its protected merge, but [master run 34071011273](https://github.com/csmart/aeronautes/actions/runs/34071011273) exposed a remaining gateway-test ordering assumption.
An active socket can close while the listener is still shutting down, so an immediate successful dial does not prove the cancellation deadline was missed.
The test now checks socket closure and listener refusal independently against one shared absolute deadline, retaining the existing allowance.
The [controlled reproduction and refusal controls](experiments/2026-09-07-gateway-deadline.md) distinguish an allowed shutdown interval from absent or late revocation.
This is a test synchronisation repair; feature delivery stopped until the repair passes the protected PR and master checks.
