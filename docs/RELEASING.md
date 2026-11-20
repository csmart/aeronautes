# Releasing Aeronautes

## Targets (§61.0)

The build matrix covers Linux, macOS and Windows on x86_64 and aarch64.
`.github/workflows/release.yml` builds all six on matched native runners for a tag, or when a manual dispatch requests all platforms; ordinary manual dispatches build Linux only.
Linux is the accepted release scope: publication requires the Linux jobs and attaches their RPM, deb and tarball artifacts for both architectures.
macOS and Windows remain development workflow artifacts until their own native acceptance runs are complete.
The webview links via CGO, so these tasks do not cross-compile between operating systems.

## Signing status: UNSIGNED, on purpose

Release signing and notarisation are **deliberately not implemented yet**
(owner decision, 2026-08-29, recorded in DESIGN.md §65.6). Every artifact
name carries the `-UNSIGNED` suffix, macOS Gatekeeper and Windows
SmartScreen will warn, and nothing in the pipeline pretends otherwise.
The workflow marks exactly where `rpm --addsign`/`debsigs`,
`codesign` + `notarytool`, and `signtool` slot in once release keys
exist.

## The transfer engine in bundles (§61.1)

Every artifact carries the official pinned rclone: CI fetches it per
target with `scripts/fetch-rclone.sh`, which verifies the release zip
against rclone's published SHA256SUMS **and** the extracted binary
against `internal/transfer/rclone/manifest.txt` — the same manifest the
app embeds and re-checks at runtime before the first spawn. A bundle
whose sidecar fails verification refuses to spawn it (no silent
fallback). macOS and Windows carry it beside the app executable. The
Linux rpm and deb install it at `/usr/local/libexec/aeronautes/rclone`,
off `PATH`, and the Linux tarball carries it beside the executable; the
packages depend on no distribution rclone
([ADR 0089](adr/0089-bundled-engine-on-linux.md) records why the earlier
distribution dependency failed). `aeronautes --engine` prints which
engine an installation resolved. A pin upgrade regenerates every
manifest line together and re-runs the §14.3 suite (docs/rclone.md).

## Local builds (Fedora)

```
sudo dnf install golang gcc gcc-c++ pkgconf gtk4-devel webkitgtk6.0-devel nodejs npm rpm-build
make tools     # pinned wails3 CLI
make build     # bin/aeronautes
make rpm       # bin/*.rpm via nfpm
make package   # rpm + deb + AppImage
```

AppImage is an unsupported development convenience: it does not yet carry the release bundles' verified-engine contract or installation acceptance.
Use RPM, deb or the tarball for the Linux release candidate.

The binary is dynamically linked against the system WebKitGTK by design;
a fully static binary is not possible (see README).

## Build identity

Local builds and branch workflow dispatches report `dev`, with the source revision and clean/dirty state.
To stamp a local release build, set `AERONAUTES_RELEASE_VERSION=vX.Y.Z` when running `make build`, `make rpm`, `make deb` or the corresponding Wails task.
The matching tag must identify the current commit, and the source tree must be clean, including untracked files.
The builder accepts only stable three-component tags, with no leading zeroes and each component at most 65535; prerelease and build metadata suffixes fail explicitly.
`DEV=true` cannot carry a release label.
The tag workflow supplies this same input automatically.

Run `bin/aeronautes --version` to inspect the stamped version, full source revision and source state without opening application state.
The About dialog displays the same identity.
Packages and archives carry versioned `UNSIGNED` filenames and a build identity document; the release workflow writes an artifact SHA256 provenance file alongside them.
The [fixture evidence and platform limits](experiments/2026-09-09-release-identity.md) distinguish actual Linux package inspection from Windows cross-build/resource checks and macOS template checks.

## Release steps

The first release is 0.5.0, by the operator's decision of 11 November 2026, which replaces the earlier plan to cut 0.1.0 first (finish line decision 19).
It is cut only after the operator has run the [0.5.0 test plan](audits/2026-11-11/0.5.0-test-plan.md) against the release candidate and recorded its results; the [0.1.0 test plan](audits/2026-09-24/0.1.0-test-plan.md) was never run and is carried into it.

1. `make test` and `make test-integration` (SAIO running) are green.
2. `scripts/smoke.sh` passes headless — CI runs it on every push/PR too.
3. A release-workflow dispatch on the commit to be tagged builds the candidate packages; the operator runs the test plan against them and records the results; a failed step blocks the tag.
4. Write the release notes as `docs/releases/vX.Y.Z.md` on that commit, then tag `vX.Y.Z` and push the tag.
   CI builds the six unsigned artifacts, and its `publish` job drafts a GitHub release titled with the tag, carrying the Linux rpm, deb and tarball for both architectures with their provenance files, and the notes.
   macOS and Windows builds stay workflow artifacts: they are development builds without acceptance (finish line decision 1).
5. Check the draft: every file is present, and each file's SHA256 matches its provenance record; then publish it.
6. Before announcing the release, record the representative real-cloud browser and service journeys from section A of the test plan, run locally with the test accounts.
   The mock and local SAIO runs do not replace that §57.4 release evidence; the remaining host checks are in section C.

Every job of the release workflow builds with the current Go patch release, as CI tests with; `go.mod`'s `go` line is the language floor, not the build toolchain.
