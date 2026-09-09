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
