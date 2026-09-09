# Releasing Aeronautes

## Targets (§61.0)

Three operating systems × two architectures, all required: Linux, macOS,
and Windows on x86_64 and aarch64. The CI matrix in
`.github/workflows/release.yml` builds all six on matched native runners
(the webview links via CGO, so there is no cross-OS compilation).

## Signing status: UNSIGNED, on purpose

Release signing and notarisation are **deliberately not implemented yet**
(owner decision, 2026-08-29, recorded in DESIGN.md §65.6). Every artifact
name carries the `-UNSIGNED` suffix, macOS Gatekeeper and Windows
SmartScreen will warn, and nothing in the pipeline pretends otherwise.
The workflow marks exactly where `rpm --addsign`/`debsigs`,
`codesign` + `notarytool`, and `signtool` slot in once release keys
exist.

## The transfer engine in bundles (§61.1)

macOS and Windows artifacts carry the official pinned rclone beside the
app executable: CI fetches it per target with
`scripts/fetch-rclone.sh`, which verifies the release zip against
rclone's published SHA256SUMS **and** the extracted binary against
`internal/transfer/rclone/manifest.txt` — the same manifest the app
embeds and re-checks at runtime before the first spawn. A bundle whose
sidecar fails verification refuses to spawn it (no silent fallback).
Linux packages instead **depend on the distribution's rclone** — the
version pin tracks Fedora's build — and PATH binaries are verified by
version pin, not hash. A pin upgrade regenerates every manifest line
together and re-runs the §14.3 suite (docs/rclone.md).

## Local builds (Fedora)

```
sudo dnf install golang gcc gcc-c++ pkgconf gtk4-devel webkitgtk6.0-devel nodejs npm rpm-build
make tools     # pinned wails3 CLI
make build     # bin/aeronautes
make rpm       # bin/*.rpm via nfpm
make package   # rpm + deb + AppImage
```

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

1. `make test` and `make test-integration` (SAIO running) are green.
2. `scripts/smoke.sh` passes headless — CI runs it on every push/PR too.
3. Tag `vX.Y.Z` and push; CI produces the six unsigned artifacts.
4. The §57.4 smoke against real SwiftStack infrastructure is a manual
   owner step before announcing anything.
