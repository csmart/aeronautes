# Dependency refresh observations — 7 September 2026

The owner requested attention to the open Dependabot updates during audit remediation.
This preparation has not changed either PR source head.

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
The application currently pins its CLI in `Makefile`, the CI binding-generation step and the Windows release step, all still beta.15.
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

The commit sequence and delivery gates are recorded in DESIGN §65.11.
Implementation, independent local review and new PR/master checks remain pending.
