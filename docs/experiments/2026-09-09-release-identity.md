# Release identity stamping

This is the bounded implementation and evidence for [SEC-10](../audits/2026-09-05/security-release.md#sec-10--release-identity-is-not-stamped) and [P33](../audits/2026-09-05/remediation-plan.md#phase-f--supported-platforms-and-release-evidence).
It establishes one build identity and checks two disposable fixture releases.
Native six-target acceptance, package installation, signing and the valuable-data release gate remain open.

## Before and after

At source `33503e0e3c35704731dc86d4d5b1a9338846286d`, `main.version` defaulted to `dev`, every supported build disabled automatic VCS stamping, and none supplied the promised version linker flag.
The macOS, Windows and RPM/DEB inputs contained `0.1.0`, while release archive names omitted the release version.
The original source contents and their SHA256 hashes are preserved in the local evidence file `before.json`.
This is a reproduced build-source defect, not a claim that a tagged production executable was run before the fix.

The [identity builder](../../build/identity/main.go) now resolves the version, exact Git revision and source state together before compiling.
The supported Wails tasks run it after frontend and binding generation, so a generated source change cannot silently keep a clean release label.
It renders native resources and package inputs into the ignored `bin/build-identity/` directory and supplies all three linker values in one argument list.
It rechecks source identity after compilation and records the resulting binary SHA256 and Go toolchain.
An attempted rebuild invalidates prior provenance, and packaging refuses a binary whose bytes no longer match that provenance.

The binary's `--version` command returns JSON before opening application state or the webview.
The same identity value enters the Go About service, and the existing About dialog displays its version, complete source revision and source state.
The generated bridge model and the two fake API identity responses carry the new fields explicitly.

## Release input contract

`AERONAUTES_RELEASE_VERSION` is the only release-version input.
It accepts `vMAJOR.MINOR.PATCH`, without leading zeroes, with each component in `0..65535`.
The exact tag must exist and identify the current commit; annotated and lightweight tags both resolve through their commit object.
A release build requires a clean source tree, including untracked files, and refuses `DEV=true`.
Malformed tags, prerelease suffixes and build metadata suffixes fail explicitly; they are never stripped or silently converted into another version.
Supporting prerelease versions across the native package formats requires a separate mapping decision.

The numeric core is used for native and package versions, while the binary and About report the same core without the leading `v`.
Windows' fixed fields add the fourth numeric component `0`; the string fields retain the three-component application version.
The component bound preserves the [Windows 16-bit version fields](https://learn.microsoft.com/en-us/windows/win32/menurc/versioninfo-resource).
The three-number representation also follows the current [macOS bundle version shape](https://developer.apple.com/documentation/bundleresources/information-property-list/cfbundleversion?changes=_2); native acceptance is still a separate check.
RPM/DEB retain package release `1`, so a Debian version such as `1.2.3-1` contains application version `1.2.3` and packaging revision `1`.

An empty input, including a branch workflow dispatch, preserves application version `dev`.
Its package version and artifact basename include `0.0.0-dev.<12-character-revision>` and append `.dirty` for modified sources.
Numeric native resource fields use `0.0.0`, with `dev`, the full revision and source state retained in the other resource fields and the bundled identity document.
`build/config.yml` is the development configuration and uses that same numeric development placeholder; release metadata comes from the generated inputs.
A direct Go build without identity or VCS stamps reports `dev`, `unknown`, `unknown`; it cannot imply a clean release.
The observed unstamped production executable returned exactly those values.

## Artifact propagation

RPM/DEB include the binary's identity document under `/usr/share/doc/aeronautes/`.
The macOS bundle includes it under `Contents/Resources/`, and the workflow includes it in Linux and Windows archives.
Artifact basenames contain the resolved version, target OS, architecture and the existing `UNSIGNED` marker.
The AppImage task also uses the resolved basename and requires a matching native Linux build; AppImage creation was not exercised here.
The release workflow records SHA256 checksums for the actual artifact files beside their shared build identity.
Recording again preserves the same provenance instead of checksumming and then overwriting its own file.
The workflow requires the named RPM, DEB and macOS bundle to exist before recording them; a missing package cannot disappear behind the old optional-copy fallback.
No signing, tag publication or release publication is added.

## Two fixture releases

The final producer, `v5`, copied 622 source files, including 329 Go/SQL files, into a disposable repository and used Go 1.27.1 plus Wails 3.0.0-beta.16.
This evidence document is excluded from that copy to avoid a self-referential checksum record.
All 329 Go/SQL files and the copied build/frontend inputs match the implementation source checked before commit.
The release invocation documentation was updated after the fixture copy and is recorded as a documentation-only difference.
Its tags exist only in that disposable repository.
The second fixture adds an explanatory marker file, so the expected source revision changes while application inputs remain identical.

| Fixture | Source revision | Binary/About version | Source state |
| --- | --- | --- | --- |
| `v1.2.3` | `59072d6a19c375b56ede192e99a08da910580dff` | `1.2.3` | `clean` |
| `v2.3.4` | `78651fa3753b6cd90422c37211176a3250a2f9cf` | `2.3.4` | `clean` |

| Check | Observation and limit |
| --- | --- |
| Linux amd64 production executable | Built through `wails3 task build`; running `--version` returned the expected three fields for both fixtures. |
| About | Root/Go service tests preserve the complete identity; four Svelte cases display both fixture versions, dirty development and unknown source state. This is not a native About-window capture. |
| RPM and DEB | Queried actual package metadata, extracted both packages, checked embedded identity documents and matched extracted binary SHA256 to the executed binary. No package installation or upgrade is claimed. |
| Linux filenames and provenance | Actual RPM, DEB and tar files have distinct versioned names; every recorded artifact checksum was recomputed and matched. |
| Windows amd64 executable | Cross-built both actual application executables, parsed their PE version resources and checked fixed versions, string versions and source comments. No Windows execution or archive installation is claimed. |
| macOS plist | Parsed the rendered plist and checked both native version fields plus source revision/state. No macOS compilation, bundle execution or installer test is claimed. |

The final application binary SHA256 values are:

| Target | `v1.2.3` | `v2.3.4` |
| --- | --- | --- |
| Linux amd64 | `3f9c2ca52258de303d60c0735437035435d15e6e6b9e3caa882137b95ef4b869` | `2eb80f268b48f9f057af58f388f46244bf2e48bb5e3d123555425b7b132c2f3f` |
| Windows amd64 | `99c26a670c856d081f4e74b09ebdb67d00758c1a5b172aaa6f8d5437b873fe01` | `8944b61e55883dbb96180586821bd03a6b59c6e570977e4ea0ce0f903128415d` |

The evidence archive retains `verify_fixtures.py`, all command logs, `fixture-inputs.json`, `fixture-results.json`, generated inputs, extracted packages, PE resource observations, artifact provenance and `validate_evidence.py`.
The first fixture attempt correctly failed the clean-source guard because its setup tracked a `node_modules` symlink that npm replaced.
The second failed because copying npm command links as ordinary files broke Vite resolution.
Both failures are preserved and excluded from passing evidence.
The third passed; the fourth reran the same checks after the provenance repeatability fix.
The fifth also preserves the existing `macos` artifact label and adds six target-name cases; it is the final evidence above.

## Maintained checks and reproduction

The focused race run passes 23 named tests across the root, app and build-identity packages, without failures or skips.
It exercises two temporary Git tags, real compiler/linker output from a small fixture, typed About identity, structured native resources, clean/tag/input refusals, replaced binaries, stale provenance and artifact checksums.
All 884 frontend tests pass, including the four new About identity cases, and Svelte reports no errors or warnings.
Focused Go vet passes, and binding regeneration changes only the intended Info/About fields.
Workflow lint passes after excluding the pre-existing `windows-11-arm` runner-label warning from the local actionlint version.
The runner definition itself is unchanged.

Ten deliberately broken Go overlays are rejected by the maintained tests: wrong linked version, wrong linked source, hidden dirty state, a tag for another commit, acceptance of a replaced binary, retained stale provenance, fixed native versions, fixed package versions, an artifact with the wrong name, and a provenance self-checksum.
The archived overlays and their expected failing test names are in `mutations/results.json`.
These controls check stamping failures; they add no transfer-scenario or native-platform coverage credit.

Run the maintained tests from the repository root:

```sh
go test -race -count=1 . ./build/identity ./internal/app -run 'Test(BuildIdentityReachesAbout|ReleaseVersion|SourceIdentity|TargetIdentityNames|FixtureReleaseMetadata|BinaryAndArtifactProvenance|FailedBuildInvalidatesProvenance|AppServiceInfo)$'
```

Run `npm run check` and `npm test` from `frontend/`.
For a full artifact replay, copy or clone the reviewed source into a disposable repository, create fixture-only tags there, and keep its working tree clean.
For each tag, run `AERONAUTES_RELEASE_VERSION=v1.2.3 wails3 task build`, generate the desktop file with `wails3 task linux:generate:dotdesktop`, then run `go run ./build/identity package -format rpm` and the corresponding `deb` command.
Read the basename with `go run ./build/identity name`, create the archive, and record its checksums with `go run ./build/identity record -dir <artifact-directory>`.
Use a second fixture tag and revision, compare the observed metadata and extracted binary hashes, and retain the producer inputs with the results.
These fixture tags must stay inside the disposable repository.

P33 still needs the native About/package/resource observations across the supported platform matrix before its full acceptance can be closed.
P34's exact-commit release eligibility and every wider valuable-data gate remain separate.

## Windows checkout line endings

The first [unsigned branch build](https://github.com/csmart/aeronautes/actions/runs/34344406506) at `2a869e41f385714e54cb82b64b96d6f38744a1fb` passed its test job and all six build targets.
Artifact inspection found matching development identities and checksums throughout, but both Windows artifacts reported `dirty`; the Linux and macOS artifacts reported `clean`.
The hosted toolchains recorded Go 1.27.0, distinct from the local Go 1.27.1 checks above.
Actual RPM and DEB development headers both use `0.0.0~dev.<revision>` before their package release, while the recorded canonical package version and filenames retain `0.0.0-dev.<revision>`.
The downloaded Linux amd64 binary returned `dev`, that full source revision and `clean` from `--version`, with no state files created in its fresh disposable state directory.
This is executable identity evidence, not native About or package installation evidence.

A disposable checkout of that exact source with `core.autocrlf=true` reproduced the dirty state locally.
It started clean; Windows-target `go mod tidy` rewrote `go.mod` with LF, and Wails 3.0.0-beta.16 binding generation rewrote all 22 tracked binding files with LF.
Git reported those files as modified even though their normalised contents matched the committed inputs and `git diff` contained no content hunks.
A clean fixture tag would consequently be refused by the existing release guard.
The hosted logs show those generators running before identity resolution, but do not contain the old checkout's Git configuration or changed-file list, so attribution of the hosted failure to line endings remains an inference.
The original downloaded artifacts, logs, failing checkout and reader corrections are preserved.

The scoped `.gitattributes` rules keep only the observed build-rewritten inputs, `go.mod` and `frontend/bindings/`, as LF on checkout.
No tracked source contents are renormalised and the source-state guard is unchanged.
A local control with those attributes stays clean through the same module and binding generators.
The maintained regression uses a real autocrlf checkout and module tidying, plus the LF write boundary of binding generation; it also requires changed binding content and untracked inputs to remain refused.
The final correction producer, `windows-eol-fix-v2`, records 625 matching source inputs and runs the Windows-target generators followed by the actual `windows:build` task from a fresh autocrlf checkout.
Its disposable `v1.2.3` tag identifies fixture source `c358632a97769d33426ae8d58250284ab242228c`; the resulting Windows amd64 binary has SHA256 `d34500ff356852030d1aa4719842ed4df2dec0ddc93cdce7ef204c574f94e9bd`.
Its identity document and PE resources agree on `1.2.3`, that source revision and `clean`, and the checkout remains clean after compilation.
This is a Linux-hosted Windows cross-build, not native Windows execution.
Removing either LF rule makes the unchanged-generation regression fail; ignoring source dirtiness makes both real-change refusal cases fail.
The first correction producer is preserved but excluded because diff inspection found that its candidate replaced an existing unrelated attribute rule; the rule was restored before the final replay.
Windows workflow steps now record checkout configuration and line endings, require clean source before and after the actual Wails build, and check the recorded source state.
Fresh hosted evidence for this correction remains a delivery requirement.

## Protected delivery and hosted artifacts

[PR71](https://github.com/csmart/aeronautes/pull/71) delivered independently reviewed source `0f9bb47412808a9d2650786b170064782ed24ad3` as master `2ad4ea2d05cf479cca9ee720a30a129f98a07ccb` at 14:15:26 UTC on 9 September 2026.
The source incorporates the delivered keyboard changes, and independent range review checked both the About focus attributes and identity display together.
The protected merge preserves the two logical release/checkout patches and exact tree `6af151d613c0b6814a0fb6c59682836628c71051`.
All 628 tracked files, including 331 Go/SQL files, match between reviewed source and merged master.
All five [PR CI jobs](https://github.com/csmart/aeronautes/actions/runs/34358393188) and all five [master CI jobs](https://github.com/csmart/aeronautes/actions/runs/34362414008) passed on their first attempts.
Both complete integration streams contain 2,484 named passes, 718 top-level passes, twenty passing packages and the same two optional keychain/rclone-matrix skips.
Each artifact event stream exactly matches its complete job log; the only time-dependent test name is checked against its producer's one-hour expiry calculation before comparison.
The PR/master system-package times were 831.835/829.539 seconds and integration-job times were 1,061/1,049 seconds, within the unchanged limits.
Both report 86.9% Go statement coverage and valid but incomplete scenario coverage at 40/116 required cells.

All seven jobs in the [unsigned six-target workflow](https://github.com/csmart/aeronautes/actions/runs/34358370897) passed for source `0f9bb474`.
The six downloaded archives match their GitHub SHA-256 digests, inner checksums/provenance, binary architectures, native resources and exact source identity.
Every artifact reports `dev`, the full reviewed revision and `clean`, with canonical package version `0.0.0-dev.0f9bb4741280` and `UNSIGNED` filenames.
Actual RPM headers use `0.0.0~dev.0f9bb4741280` with release `1`; DEB headers use `0.0.0~dev.0f9bb4741280-1`.
The four Linux package payloads match their corresponding archive's binary and identity document.
Both Windows resources and macOS plists retain their platform-specific numeric development version while agreeing on the development string and clean source identity.
Hosted provenance records Go 1.27.0; the separate local verification used Go 1.27.1.

Both actual Windows runners report `core.autocrlf=true`, with `go.mod` and all twenty-two bindings remaining LF and clean before and after the real module, binding and architecture-specific Wails build steps.
This supplies the previously pending hosted witness for the narrow LF correction, while preserving the earlier dirty artifacts and local reproduction.
The downloaded Linux amd64 binary, SHA-256 `87ae6909c94b510922d44ddb7c3e88d037ca501486105f0d4eda159404f5b70d`, returned the exact development/source/clean identity through `--version` and left its fresh state directory empty.
These artifacts remain attributed to source `0f9bb474`; identical merge-tree evidence does not relabel their embedded source as master `2ad4ea2d`.
No additional dispatch, tag or published GitHub release was needed.

The initial whole-scenario-report comparison failed on generated identifiers; the original failure and all raw differences are retained.
A separate comparison verifies a one-to-one mapping of 32 job IDs, six scan IDs and nine fixture namespaces while preserving their relationships and all remaining report fields and evidence text.
Five negative controls detect changed coverage, outcomes, scan relationships, object names and missing witnesses; no source or producer was changed for the comparison.

SEC-10's stamping implementation and bounded build evidence are delivered.
Native About windows, package installation/upgrades, Windows/macOS runtime observations, signing, full P33/platform acceptance and valuable-data release remain open.
