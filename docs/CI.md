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
| Smoke — headless app against mock Swift | Built desktop application under Xvfb, connecting, browsing and copying to the native clipboard; full mock browser journeys through the real frontend and Go services |
| CI — all required jobs passed | Every preceding job completed with `success` |

The native smoke proves desktop startup, connection, listing, GTK4/X11 question refusal, X11 clipboard delivery, refresh on returning focus and the GNOME keyring lifecycle; it does not run a transfer.
Escape and a graceful window-close request each dismiss the public-read question, return to a usable editor, send no container POST and leave an independently read empty public ACL.
It reads Copy name, Copy ETag and the Settings log-path action through an independent `xclip` client, comparing them with the known fixture name, payload checksum and active log-file path.
The copied log path must name an opened file; the startup cases separately require their expected diagnostics in the configured log file, since a healthy browsing session may be silent.
It takes X input focus away, creates a container through another client, waits past thirty seconds and returns focus; a new account read and opening the new row prove the refresh reached the window.
This is X11 evidence, not a Wayland or other operating-system run.
The desktop application and secret service use a private HOME, XDG directories, D-Bus and GNOME keyring, all removed after the run.
An independent Secret Service client observes item paths only: saving creates one key, a restarted app reconnects without re-entering it, and deleting the bookmark removes the item and its recorded cleanup work.
This tests GNOME Secret Service; other desktop keyrings retain the release plan's host check.
The same required job then runs all mock [browser journeys](journeys.md), including transfers and independent byte checks, with no cloud credentials.
Browser journeys substitute the native dialogs and secret store; they do not prove native clipboard, keyring or dialog behaviour.
They run serially without retries, and a focused `test.only` fails CI rather than silently reducing coverage.
Failure details stay in the run's private directory; only journey names and outcomes enter the job log, and raw application logs are not uploaded.
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
Manual release dispatch builds the unsigned Linux packages, for amd64 and arm64; it does not create a tag or publish a GitHub release.
macOS and Windows build only for a tag, or when the dispatch ticks "Also build macOS and Windows": on a private repository their runners cost many times the Linux minutes, and a test build is for the packages the operator installs.

## Non-blocking checks

Real-cloud oracles, service journeys and browser journeys run locally with test-account credentials.
Do not store cloud credentials in GitHub or run these real-cloud tests on hosted runners: a compromised credential could incur charges on the test account.
GitHub uses disposable mocks and containerised Swift instead.
The local oracle drives `go test -tags realcloud -run 'TestKeystoneV3Oracle|TestLegacyVersioningOracle' ./internal/swiftcontrol/`.
The required Go job vets that build tag on every run, so the oracle's tests stay compiled although they execute only where credentials are supplied.
The first authenticates through the product's own v3 path and does a full create, upload, read and delete round-trip on each configured cloud.
Its companion, `TestKeystoneV3OracleDiscoversFromTheRoot`, which the same pattern selects, connects through the Identity service's root URL and checks that version discovery reaches the same storage endpoint as the `/v3` URL ([ADR 0113](adr/0113-keystone-discovery-and-ids.md)).
The second sets each legacy versioning mode on a fresh container through the container editor's route, overwrites and deletes an object through the product's delete protocol, and asserts that the name and the archive hold what the [27 October record](experiments/2026-10-27-delete-under-versioning.md) measured on the fixture; a backend whose `/info` does not allow a mode skips it.
Run it locally from the repo root with `make oracle CLOUDS=/path/to/clouds.yaml`, or set `OS_CLIENT_CONFIG_FILE` and run `make oracle`; `make oracle CLOUD=ceph` runs a single backend.
A selected path that does not exist is an error rather than a fallback, so a typo cannot direct the run at another account's clouds.yaml.
Use a local `clouds.yaml` holding the backend-named `aeronautes-ceph` and `aeronautes-swift` entries.
Run `make journeys CLOUDS=/path/to/clouds.yaml` and `make uijourneys CLOUDS=/path/to/clouds.yaml` for the service and browser suites; [the journey guide](journeys.md) describes their fixtures and evidence.
These runs are release evidence, not hosted required checks; an unavailable cloud or expired credential does not block an ordinary merge.
The test keeps passwords, tokens and account identifiers (username, project) out of its output, and prints no auth endpoint, region or storage host either, so a log does not name the provider.
A failure prints the kind of error, its HTTP status and the Go types in its chain, never the error's text, because a transport or certificate error can name a host no configured value predicts.
A run is labelled by its backend, and a clouds.yaml key or path, which can each be named for a provider, is used only to find the entry.
Nothing a server returned as text is printed either: a body that is not the one written, which could be a proxy's sign-in page, is reported by its length and digest, and entity tags, content types and versioning flags are reported only by whether they match.


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
The `saio-diagnostics-main` and `saio-diagnostics-heavy` artifacts retain each fixture's container and runner observations after successful and failed integration suites for seven days.
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
The existing coverage floors are advisory; the bounded covered-cell preservation check below is a hard regression gate, while full scenario coverage and release gates remain audit work package P34.
Green CI is required evidence, and the wider safety and platform release gates still apply.

The real-Swift integration lane gives each Go test package forty minutes and bounds the whole job to sixty minutes, including setup and report upload.
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

The [fixture partition](experiments/2026-09-12-remaining-test-schedule.md#proposed-partition-across-fixtures) separates the heavy groups listed in [the partition script](../scripts/saio-partition.py) from the complementary internal suite while preserving all tests and limits.
The two producers use separate runners and fixtures; `fail-fast: false` lets both retain diagnostics, while newer PR pushes still cancel the superseded workflow run.
The protected Integration check requires both producers to succeed and validates their inventories, source/toolchain/run identity, actual exits and artifact hashes before combining atomic coverage with Go's normal reader.
`saio-tests-main` and `saio-tests-heavy` retain each complete producer; `scenario-coverage` retains the combined profile and both independently validated scenario reports, with main's unchanged credits and the heavy report required to have zero credits.
After all existing checks and output writes, the join requires `--baseline internal/scenariocontract/preservation-baseline.json` and enforces [ADR 0069](adr/0069-preserve-covered-scenario-cells.md) against the independently admitted snapshot.
Lost protected pairs, required-set drift or inconsistent evidence fail the join without changing report validity, gaps or incomplete-coverage semantics.
The workflow records the actual Python status in `saio-combined/join-exit.txt`, then requires zero before coverage summarisation; the unchanged required-job gate refuses a failed Integration result.
The `scenario-coverage` upload runs on failure as well as success, retaining the combined root and `scenario-preservation.json` when comparison was reached.
An earlier failure retains the available numeric exit and producer artifacts without inventing a preservation result; absent upload inputs remain an error.
The [baseline record](experiments/2026-09-21-scenario-preservation-baseline.md#aggregate-activation-proof) distinguishes executed local failure-path controls from the hosted success-artifact and resulting-master evidence required for delivery.
The fixture jobs retain their 60-minute limits; the artifact-only join has a separate ten-minute limit, and the five protected check names remain unchanged.
Its Integration check requires both producers, complete disjoint execution and compatible combined coverage before success; the contract requires independent review before workflow changes.

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
