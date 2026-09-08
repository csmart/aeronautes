# Aeronautes

[![CI](https://github.com/csmart/aeronautes/actions/workflows/ci.yml/badge.svg?branch=master&event=push)](https://github.com/csmart/aeronautes/actions/workflows/ci.yml?query=branch%3Amaster+event%3Apush)

**Aeronautes** (a living genus of swifts; “air sailor”) is a beautiful, performance-first
**desktop operator workbench for OpenStack Swift / SwiftStack**.
It makes Swift's control-plane semantics understandable and safe, and uses rclone and native Swift
features to move bulk data as fast as the target cluster can safely handle.

The mantra: **the inspector is the product; transfers are a first-class superpower; performance and
beauty are features.**

![Browser and inspector](docs/assets/screenshots/02-inspector-dlo.png)

*The DLO inspector against real Swift: logical vs listed size, the exact manifest, its segment
table, and the manifest-only delete. More in [`docs/assets/screenshots/`](docs/assets/screenshots/).*

## What it is

- A **Swift-native** operator tool — containers, objects, prefixes, **segments**, DLO/SLO manifests,
  storage policies, ACLs, versioning, quotas — not a file-manager metaphor. TempURL, CORS, and
  staticweb are modeled read-only today (key slots and presence, never secret values); their
  management surfaces are tracked v1.x work.
- **Cross-account / cross-cluster** Copy with conditional creation of new objects and explicit refusal of existing changed destinations; Mirror supports comparison preview while execution is unavailable.
- **Safety-first**: destructive actions explain scope; heuristics never authorize destruction;
  object-metadata edits never silently drop keys.
- **Cross-platform** desktop app — Linux, macOS, and Windows on both **x86_64 and aarch64** (six
  targets, all required); Linux is the primary/reference platform.

## What it is not

- Not a `python-swiftclient` port, not a general S3 browser, not a FUSE mount, not a Keystone UI (v1).

## Stack

Go core · `github.com/ncw/swift/v2` (wrapped) · **Wails v3** desktop shell · Svelte 5 + TypeScript UI ·
pinned rclone subprocess for bulk movement · SQLite (non-secret state) · OS keychain for secrets
(session-only fallback when no keychain is reachable; an encrypted vault is a proposed
future option, ADR 0006).

## Documents

- [`docs/DESIGN.md`](docs/DESIGN.md) — authoritative product + engineering design (66 sections).
- [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) — compact normative requirement IDs for phase gates.
- [`docs/adr/`](docs/adr/) — architecture decision records (0001–0012; 0006 still proposed).
- [`docs/assets/screenshots/`](docs/assets/screenshots/) — real captures of the app against containerized Swift.
- [`docs/rclone.md`](docs/rclone.md) — the exact pinned-rclone flag surface and measured behaviours.
- [`docs/RELEASING.md`](docs/RELEASING.md) — the six-target release pipeline (unsigned for now, on purpose).
- [`docs/CI.md`](docs/CI.md) — required PR checks, merge rules and coverage artifacts.
- [Full project audit, 5 September 2026](docs/audits/2026-09-05/README.md) — evidence-backed findings and an ordered remediation plan, including the fault/barrier harness.
- [`AGENTS.md`](AGENTS.md) — start-here guide for coding agents.

## Building on Fedora

Fedora (current stable) is the first-class Tier-1 platform (§6.6). One-time setup:

```sh
sudo dnf install golang gcc gcc-c++ pkgconf gtk4-devel webkitgtk6.0-devel nodejs npm
go env -w GOTOOLCHAIN=auto   # Fedora defaults to 'local'; go.mod pins a newer toolchain
make tools                   # installs the pinned wails3 CLI into ~/go/bin
```

Then:

```sh
make build   # production binary -> bin/aeronautes
make run     # build + launch
make dev     # hot-reload development mode
make test    # go (race) + frontend suites, no cluster needed
make rpm     # bin/aeronautes.rpm -> sudo dnf install ./bin/aeronautes.rpm
```

The Makefile is a thin directory over the real build system, the Wails v3 Taskfiles
(`Taskfile.yml` + `build/`); `wails3 task --list` shows everything. The RPM (and deb/AppImage via
`make package`) is built by [nfpm](https://nfpm.goreleaser.com) from `build/linux/nfpm/nfpm.yaml`
— a convenience package of the built binary, not a Fedora-guidelines source build; a review-grade
`.spec` (vendored sources through `rpmbuild`) is release-packaging work (§61, Phase 7).

## Quickstart

1. Install the pinned transfer engine: `sudo dnf install rclone` (the app pins **v1.74.3** and
   refuses others — transfers are disabled, browsing still works, if the pin is absent).
2. Launch `bin/aeronautes`, click **Add**, and create a bookmark: display name, TempAuth v1 auth
   URL (`https://cluster/auth/v1.0`), `account:user`, API key. The key goes to the OS keychain;
   if none is reachable the app says so and keeps it for the session only.
3. **TLS**: the default verifies against the system trust store. For a cluster with a private
   CA choose **Custom CA** and paste the PEM bundle — trust is per-bookmark, applies to both
   the native client and the rclone child, and never changes another endpoint's verification.
   **Insecure** exists for labs and is flagged loudly.
4. Browse: containers on the left (with backstage classification), objects in the middle
   (server-side prefix + Find in container), authoritative HEAD detail on the right.
5. Transfers: open a container and use **Copy to…** (preview first, then start), **Upload…**/
   **Download…** (complete collision preflight before any write), or the **Mirror** tab
   (comparison preview only; execution is unavailable while concurrent changes cannot be protected).
   Copy refuses changed existing destinations and reports a partial job with a separate refused count; versioning does not enable overwrites.
   Follow everything in the jobs drawer — progress, throughput, cancel, audit trail.
   Its **Job history** tab pages through older retained jobs; expand a job to inspect every saved item, its exact quoted locations and separate execution and verification observations.
   Missing records, uncaptured history and unavailable reads are shown explicitly; an active job's records are incomplete, and the displayed issue counts can overlap.
   Existing transfers still report that item records were not captured until their callers are migrated; this read-access view does not lift the valuable-data hold.
6. Destructive cleanup: **Delete…** for prefix/empty/delete-container (typed confirmation),
   and manifest deletion from the inspector — segments die only under recorded ownership
   proof or an explicit Backstage override.

### Before trusting a release against production

The [project audit](docs/audits/2026-09-05/README.md) still blocks use of mutation, recovery and transfer paths on valuable data.
Credential replacement now preserves the previously published key if a save fails, but retired and unused keys remain tracked in credential storage until verified cleanup is implemented.
Open **key cleanup** in the status bar to see recorded old keys, unfinished saves and saves in progress.
The view refreshes after bookmark changes and reports unavailable state explicitly; its counts cannot confirm native keychain erasure.
Deleting a bookmark removes its saved configuration and cached connection; it does not yet prove physical key removal or revoke every previously held session.
These remaining [credential lifetime and recovery gates](docs/adr/0015-credential-bindings.md) must close before that hold is lifted.

The §57.4 smoke against real SwiftStack infrastructure is a **manual operator step**:
connect to a real endpoint over TLS, browse a large container, run a small cross-account copy
into a scratch container, verify counts in the drawer, and check `/info`-gated features match
the cluster. `scripts/smoke.sh` covers the automated half against a local mock.

To exercise the app or the integration suite against a real local Swift:

```sh
sudo podman run -d --name saio -p 127.0.0.1:8080:8080 docker.io/openstackswift/saio:latest
make test-integration        # or add a bookmark in the app:
                             # auth URL http://127.0.0.1:8080/auth/v1.0, test:tester / testing
```

Ubuntu current LTS works the same with `libgtk-4-dev libwebkitgtk-6.0-dev`; EL9 has no native
WebKitGTK 6 and is served via Flatpak later (§6.6).

## Who it's for

OpenStack Swift / SwiftStack operators managing large namespaces: browsing, inspecting, safely mutating
metadata/ACLs, and running cross-account replication and bulk deletes.

## Licence

[Apache License 2.0](LICENSE) © 2026 Chris Smart.

Compatible with the dependencies: rclone and `ncw/swift/v2` are MIT, which combines freely into an
Apache-2.0 project. rclone is bundled as a separate binary (subprocess), so its licence does not
constrain Aeronautes' — when distributing the bundled rclone binary, include rclone's MIT licence/notice in
the third-party notices (a `NOTICE` / `THIRD_PARTY_NOTICES` file, added when code lands).
