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
Its Linux test package uses a separate binary with a per-run private daemon, no service activation, a short socket path in a mode-0700 directory, and bounded startup and teardown.
A missing daemon fails this required lane; an ordinary local suite without the requirement flag reports explicit skips when the tool is absent.
These tests exercise the pinned client against a synthetic Secret Service and cannot satisfy the native desktop keychain gates.

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
CI does not commit these generated files or use `BADGE_DEPLOY_KEY` anymore.
The existing coverage floors are advisory; hard scenario and coverage gates remain audit work package P34.
Green CI is required evidence, and the wider safety and platform release gates still apply.

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
