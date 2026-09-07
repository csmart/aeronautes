# Dependency refresh observations — 7 September 2026

The owner requested attention to the open Dependabot updates during audit remediation.
The initial inspection below preceded any branch refresh.

| PR | Proposed update | Original head | Original CI | Commits behind master `e6e01f65` |
| --- | --- | --- | --- | ---: |
| [30](https://github.com/csmart/aeronautes/pull/30) | Wails frontend runtime beta.15 to beta.16 | `efbc022a3f52a8e646e3e5ca38aceeb9320e3226` | [34022163902](https://github.com/csmart/aeronautes/actions/runs/34022163902), all five pass | 46 |
| [29](https://github.com/csmart/aeronautes/pull/29) | Wails Go beta.16, SQLite 1.58.0 and supporting libraries | `193c7817cfe2cc29bbf95f5faeee04419bc84e8c` | [34023862186](https://github.com/csmart/aeronautes/actions/runs/34023862186), all five pass | 42 |

These historical runs cannot authorise a merge against current master.
Neither PR has remote review or comment feedback at this initial inspection.
The cursor-paginated GitHub Dependabot alert query reports zero open alerts; this is narrower than a claim about all vulnerabilities.
Raw observations are `dependabot-pr29-triage.json`, `dependabot-pr30-triage.json` and `dependabot-alerts-20260907.json` under `/tmp/aeronautes-remediation-20260905`.

## Published runtime compatibility

The exact beta.15 and beta.16 npm archives each contain 59 files.
All 58 files besides `package/package.json` are byte-identical, and the manifest differs only in its version.
The beta.16 integrity matches PR30's lockfile.
`dependabot-runtime-compatibility.json` records both archive digests and the comparison; the two archives are retained alongside it.
The [upstream tag comparison](https://github.com/wailsapp/wails/compare/v3.0.0-beta.15...v3.0.0-beta.16) independently shows no frontend runtime source change beyond package version metadata.
This supports delivering the frontend package first without changing its bridge code, while still requiring current application verification and PR/master checks.

## Go module and tool alignment

The [Wails beta.16 release](https://github.com/wailsapp/wails/releases/tag/v3.0.0-beta.16) changes macOS systray click handling and notarisation setup, plus upstream CI.
At initial inspection, the application pinned its CLI in `Makefile`, the CI binding-generation step and the Windows release step, all at beta.15.
The [Wails CLI contract](https://v3.wails.io/guides/cli/#update-cli) calls for matching CLI and Go module versions.
The Go update must align all three pins and revalidate the generated binding surface, including the reserved `ServiceShutdown` exclusion used by Find cleanup.
The downloaded beta.16 runtime and generator source still reserve that method; the generated application bindings must be checked separately.

SQLite 1.58.0 requires libc 1.75.6 and memory 1.12.1, matching PR29's proposed graph.
The [tagged module file](https://gitlab.com/cznic/sqlite/-/raw/v1.58.0/go.mod) and [changelog](https://gitlab.com/cznic/sqlite/-/raw/v1.58.0/CHANGELOG.md) are the source contracts.
The update replaces an existing local journal-recovery patch with upstream's fix and adds opt-in Linux OFD locks, disabled by default.
The application must retain WAL with synchronous FULL; this maintenance does not enable OFD locking or change schema, transaction or recovery policy.
The existing durability, rollback, migration, recovery and full bound-service suites are the application oracles.
The dependency upgrade does not close the audit's P12/P13 work.

## Release test prerequisite

The release test lane runs Go tests without the pin-fetch step added to ordinary CI in PR45.
On unchanged source `198b3042b93d8e6367d348855fc1743823f93be2`, `TestShortListingNativeCopyMock` fails because pinned rclone is absent from PATH, with process exit 1.
Adding the verified pin passes the same test with exit 0.
Both runs execute the owned mock fixture without SAIO, skipped tests or assertion changes.
`release-pin-before.json/.exit`, `release-pin-after.json/.exit` and their summaries preserve this observation in the same temporary directory.
`release-pin-inputs.json` records the source, relevant file hashes and tool-environment difference.

The repair must reuse the existing checksum-verifying fetch script and expose the pin to subsequent test steps.
Retain the failure when the tool is absent; do not convert required coverage into a skip.
A manual release workflow run builds unsigned artifacts without creating a tag or publishing a public release.
Passing that build matrix would establish build evidence, not native functional or valuable-data release approval.

The commit sequence and delivery gates are recorded in DESIGN §65.11 and have independent local approval.
The following sections record implementation separately from that plan approval.

## Frontend runtime refresh

PR30's exact two dependency files have been applied on master `4f0cee2943215511e37d3e0a58329fe8bfd3f645`, with the approved plan as their parent commit.
This base delivered [PR48](https://github.com/csmart/aeronautes/pull/48) through independent local review, all five [PR checks](https://github.com/csmart/aeronautes/actions/runs/34095697995) and all five [master checks](https://github.com/csmart/aeronautes/actions/runs/34096695188).
Its tree matches the reviewed transfer source exactly.

A clean `npm ci` succeeds without changing the dependency files.
Their complete contents match original PR30 head `efbc022a3f52a8e646e3e5ca38aceeb9320e3226`, and all 59 installed runtime files match the beta.16 published archive.
`dep-runtime-installed-v1.json` records the base, plan, original head, dependency hashes and installed-file comparison.
The full frontend suite passes 754 tests in 20 files, with no failures or skips and 77.73% statement coverage.
Svelte checking reports zero errors and warnings, and the production build passes.
All three produced frontend files are byte-identical to the previously reviewed transfer bundle; `dep-runtime-bundle-v1.json` records their names and hashes.
This matches the published package comparison: the selected package version changes while its executable frontend code stays the same.
The unchanged application behaviour also passed the pre-update suite.

The full internal Go race suite passes 577 top-level tests, 1,180 leaf cases and all 20 packages, with no failures or skips and 84.7% combined statement coverage.
It uses both owned SAIO accounts and the verified rclone pin, including the optional matrix.
`dep-runtime-go-full-v1.json/.exit` and `dep-runtime-go-cover-v1.out` retain the actual events, process exit and profile.
`dep-runtime-scenario-coverage-v1.json` reconciles the actual exit, events and expected package set with the producer configuration: valid process, stream and contract, 28/110 covered cells and 39/49 actions still having gaps.
Go vet and build also pass.

The frontend records are `dep-runtime-install-v1.log/.exit`, `dep-runtime-ui-check-v1.log/.exit`, `dep-runtime-ui-full-v1.json/.log/.exit`, `dep-runtime-ui-coverage-v1.json` and `dep-runtime-ui-build-v1.log/.exit` in the same temporary directory.
This update preserves the existing Go module and matching CLI pins; their beta.16 alignment belongs to the next planned slice.
Independent local review approved the complete plan and runtime implementation range.
PR30 then passed all five [PR checks](https://github.com/csmart/aeronautes/actions/runs/34098394386) and merged through the protected rebase procedure.
Master `7e268030af49f39e81d2e2ce85cfee5ad312657f` passed all five [master checks](https://github.com/csmart/aeronautes/actions/runs/34099335002).
The merged tree exactly matches the reviewed source and preserves the separate plan and runtime commits.
The release test prerequisite, SQLite update and wider audit work remain open.

## Wails Go and CLI refresh

This slice starts from that green master and updates the Wails Go module and all three CLI pins to beta.16.
The frontend runtime already matches through PR30.
The existing application behaviour, bound-service inventory and Find lifecycle contract remain the oracle; no application source or test assertions change.
`go mod tidy`, required by the normal Wails binding/build tasks, also records the existing direct Godbus test import as direct and removes unused checksums for the locally replaced Swift module.
Neither module's version changes, and the local Swift replacement and its source are unchanged.
SQLite remains 1.57.0 for its separate reviewed slice.

The isolated beta.16 CLI successfully runs the actual clean binding-generation task: 12 services and 49 methods, with no diff anywhere in the generated bindings.
Find still exposes only `CancelFind`, `FindStatus` and `StartFind`; `ServiceShutdown` is absent from its renderer API.
The production Linux app builds through `wails3 task build`, and its compiled module metadata confirms beta.16 and the production build tag.
`dep-wails-alignment-v1.json` records the CLI and app hashes, generated-method check and changed-file hashes; `dep-wails-base-v1.json` records the preserved base tree and uncommitted input hashes.
The raw CLI/app metadata and actual binding/build logs are retained alongside those records.
Module checksum verification, Go vet and the CGO-disabled internal build all pass.

The full internal race suite passes 577 top-level tests, 1,180 leaf cases and all 20 packages, with no failures or skips and 84.7% combined statement coverage.
It includes both owned SAIO accounts and the verified rclone pin with the optional matrix enabled.
`dep-wails-go-full-v1.json/.exit`, its summary and `dep-wails-go-cover-v1.out` retain the actual process and coverage evidence.
`dep-wails-scenario-coverage-v1.json` reconciles the actual process exit, expected package set and producer environment: valid process, stream and contract, 28/110 cells and 39/49 actions with gaps.
The unchanged frontend passes all 754 tests in 20 files, with no failures or skips and 77.73% statement coverage; checking reports zero errors and warnings, and the production build passes.
`dep-wails-ui-full-v1.json/.log/.exit`, `dep-wails-ui-coverage-v1.json` and `dep-wails-ui-check-v1.log/.exit` preserve that evidence under `/tmp/aeronautes-remediation-20260905`.

Local desktop smoke was not run because this host lacks Xvfb, xdotool and ImageMagick; the required CI smoke remains a delivery gate.
Independent local review approved the Wails implementation after regenerating identical bindings, checking the inventory and running the full internal race suite with SAIO and the rclone matrix.
A fresh `go mod tidy` on the reviewed author head leaves the worktree clean; its log and actual exit are `dep-wails-post-review-tidy-v1.log/.exit`.
Final PR29 checks, the unsigned six-target build matrix and master checks remain pending.
This dependency maintenance does not close native platform validation or the wider valuable-data release hold.

## SQLite refresh

This separate slice updates SQLite from 1.57.0 to 1.58.0, with its required libc 1.75.6 and memory 1.12.1.
The checksums include the updated transitive tooling graph; `dep-sqlite-modules-v2.json/.exit` records all 173 selected modules.
The initial graph listing could not fetch uncached metadata inside the network sandbox; the network-enabled retry succeeds without changing the proposed versions.
The newly built production app's module metadata confirms that its only compiled module changes from the Wails slice are SQLite, libc and memory.
`dep-sqlite-alignment-v1.json` records those exact before/after versions and checksums, module-file hashes and the app hash; the raw metadata is `dep-sqlite-app-version-v1.txt`.

Application source, database settings, schema migrations, generated bindings and test assertions are unchanged.
The new upstream OFD lock opt-in is not enabled by this change, and its environment variable is unset in the validation environment.
WAL and synchronous FULL remain required on every connection.
The existing tests for initial/replacement connections, ineffective durability refusal, each historical schema upgrade, writer serialisation, injected rollback, process reopening and retained recovery status all pass against the new driver.
`dep-sqlite-compatibility-tests-v1.json` indexes those test terminals within the full event stream; it is not a separate test run.
The same existing suite also passed with SQLite 1.57.0 in the preceding Wails validation.

The full internal race run passes 577 top-level tests, 1,180 leaf cases and all 20 packages, with no failures or skips and 84.7% combined statement coverage.
It includes both owned SAIO accounts and the verified rclone matrix.
`dep-sqlite-go-full-v1.json/.exit`, its summary and `dep-sqlite-go-cover-v1.out` preserve the actual evidence.
`dep-sqlite-scenario-coverage-v1.json` reconciles the actual exit, expected packages and producer environment: valid process, stream and contract, 28/110 cells and 39/49 actions still having gaps.
Module verification, Go vet, the production Linux app build and the CGO-disabled internal build pass.
The frontend source, dependencies, bindings and bundle are unchanged from the Wails slice's 754-test run; it was not rerun for this Go-only update.
All records use the same temporary evidence directory.

Independent SQLite review and the separately planned release test prerequisite repair remain pending.
Final PR29 delivery requires the unsigned build matrix, all five current PR checks, a protected merge and all five master checks.
The upgrade does not close P12 durability or P13 outcome/accounting gaps, restore refused transfers, or lift the valuable-data release hold.
