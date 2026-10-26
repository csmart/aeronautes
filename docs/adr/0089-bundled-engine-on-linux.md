# ADR 0089 — ship the verified engine inside the Linux packages

**Status:** Accepted (2026-10-26) — the operator's decision of the same day, recorded as decision 26 in the [Linux finish line](../audits/2026-09-05/linux-finish-line.md); the resolver and packaging changes ship in the same range as this record.
**Scope:** How the Linux rpm, deb and tarball obtain the transfer engine and how the application finds it.
It changes neither the pin itself (DESIGN §26.3), the enumerated flag surface ([ADR 0007](0007-enumerated-rclone-flag-surface.md)), the gateway ([ADR 0014](0014-child-storage-gateway.md)), nor the macOS and Windows bundles, which already work the way this record describes.

## The failure

The Linux packages declared a dependency on the distribution's rclone package, and the runner accepts a `PATH` binary only when its reported version equals the pin, `v1.74.3` (`PinnedVersion` in `internal/transfer/rclone/runner.go`).
The pin was chosen on 2026-08-29 as "the current Fedora stable package", so that development, the seam spike and the primary platform would run one binary (§26.3), and on 2026-09-23 Fedora 44's updates repository did carry `rclone-1.74.3-1.fc44`.
Fedora 44 now ships `rclone-1.75.2-1.fc44`.
On the operator's workstation the installed development package had pulled that update in through its own dependency, and every transfer was refused with `rclone v1.75.2 does not match the pin v1.74.3 (§26.3); refusing`, while browsing kept working.
The operator's report of 2026-10-26: "if I install an update then aeronautes breaks!" and "it's unusable as it is".

The cause is structural rather than one unlucky version.
A distribution dependency cannot hold an exact upstream release, because the distribution updates it on its own schedule, and the pin exists precisely so that the engine does not change underneath the application.
Nor is the distribution's build the measured binary even when the version matches: the sidecar manifest records the official release binaries, and the `PATH` fallback can only compare a version string, never a hash.

## Options considered

1. **Accept a version range** — the same major and minor with any patch, or anything at least the pin.
   Rejected.
   Every engine fact this project relies on is recorded against one binary: the reserved-rune set derived from the frozen v1.74.3 encoder source and re-derived on any upgrade ([docs/rclone.md](../rclone.md)), the absent `--files-from0`, the generated segment prefix shape (§26.4), the retry timing (§26.2) and the data-outcome options measured on 2026-10-02 ([experiment](../experiments/2026-10-02-engine-claims-measured.md)).
   [ADR 0007](0007-enumerated-rclone-flag-surface.md) makes a pin upgrade a re-verification event for exactly that reason.
   A range admits an engine nobody has measured, on the user's machine, in a tool that moves production data; and even a matching version from the distribution is a different build.
2. **Test a matrix of versions in CI.**
   Useful, and already wanted by §26.3 as the pinned version plus the latest upstream; `make rclone-matrix` runs it as a measurement.
   It cannot fix the installation, because the distribution will ship a version that did not exist when the matrix ran.
   It stays the instrument for choosing a pin upgrade and is not the fix.
3. **Bundle the verified official binary inside the Linux packages**, as the macOS and Windows artifacts already do under §61.1.
   Chosen.
   The engine the user runs is byte-identical to the one CI tested, the distribution's rclone becomes irrelevant to the application and is left untouched, and a pin upgrade remains an explicit, reviewed change.

## Decision

1. The Linux rpm, deb and tarball carry the official pinned rclone for their architecture.
   `scripts/fetch-rclone.sh` fetches it at build time and verifies the release archive against rclone's published `SHA256SUMS` and the extracted binary against `internal/transfer/rclone/manifest.txt`, the same manifest the application embeds and checks again before the first spawn.
2. The packages install it at `/usr/local/libexec/aeronautes/rclone`: inside the package's own prefix, never on `PATH`, so it cannot shadow the user's rclone and the user's rclone cannot shadow it.
   The tarball places it beside the executable, as the macOS and Windows bundles do.
3. Resolution order, amending the third step of §61.1 as implemented: an explicit binary; a sidecar beside the executable; on Linux, a sidecar in `../libexec/aeronautes` relative to the executable's directory, which for the packages' `/usr/local/bin/aeronautes` is `/usr/local/libexec/aeronautes`; else `PATH` under the version pin, which remains for development checkouts only.
   A present sidecar that fails verification refuses and never falls back to the next step, unchanged.
4. The packages no longer depend on the distribution's rclone package.
5. The licence notice ships in the packages at `/usr/share/doc/aeronautes/NOTICE`, because the binary is now redistributed on Linux too.
6. `aeronautes --engine` reports which engine the resolver chose, where it found it, its version and whether it was integrity-verified, or the refusal reason, so an installed package can be checked without opening a window or starting a transfer.
7. No version range is introduced anywhere.
   A pin upgrade still regenerates every manifest line together, re-runs the seam suite and re-derives the reserved-rune set.

## Consequences

- Each Linux package grows by the engine: 78,778,530 bytes installed and 19,360,960 bytes compressed with xz at level 6, measured on 2026-10-26 on the pinned linux-amd64 binary.
- Security updates to the engine become the project's to ship through a pin upgrade release, which is the position the macOS and Windows bundles are already in; the [sidecar assessment](../audits/2026-09-05/sidecar-assessment.md) shows what evaluating a candidate looks like.
  Proposed as a follow-up and not decided here: a scheduled workflow that compares the pin with the latest upstream release and opens an issue when they differ.
- A development checkout keeps working as before: without a sidecar, the pinned official binary on `PATH` or an explicit binary is used, and any other version is refused.
- The AppImage produced by `make package` is not in the release matrix and is not changed by this record; it owes the same sidecar beside its executable when it is next built.
- The README's installation step for the engine disappears for package installs, the 0.1.0 test plan's install check moves from the distribution package to the installed sidecar and the `--engine` report, and `NOTICE` no longer says that Linux packages depend on the distribution's rclone.

## Limits

This record does not measure the engine; it changes where the measured one comes from.
It does not decide a pin upgrade: v1.74.3 remains the pin until a re-verified upgrade says otherwise, and Fedora's newer package is not evidence either way.
It does not change what a transfer does when no engine can be resolved: the job is refused with the reason, as today.
