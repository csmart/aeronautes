# Aeronautes

![CI](https://github.com/csmart/aeronautes/actions/workflows/ci.yml/badge.svg)
![Go coverage](.github/badges/coverage-go.svg)
![UI coverage](.github/badges/coverage-frontend.svg)

> **Status: implementation in progress — Phases 0–6 complete, Phase 7 (release hardening)
> under way.** The design is locked; code is built one component per commit, following the
> sequences in [`docs/DESIGN.md`](docs/DESIGN.md) §65. Live today, all proven against real
> containerized Swift: the four-pane browser and inspector with evidence-driven backstage
> classification; safe metadata/ACL/container mutation; cross-account Copy through the pinned
> rclone with stage-and-commit overwrites and independent verification; executable Mirror;
> upload/download with collision preflight; recursive/empty/delete-container jobs and gated
> SLO/DLO segment cleanup over ownership proofs; the live job drawer with budgets, profiles,
> and a consent-gated benchmark; and per-bookmark TLS trust (system / custom CA / insecure)
> exercised against HTTPS Swift.

**Aeronautes** (a living genus of swifts; “air sailor”) is a beautiful, performance-first
**desktop operator workbench for OpenStack Swift / SwiftStack**.
It makes Swift's control-plane semantics understandable and safe, and uses rclone and native Swift
features to move bulk data as fast as the target cluster can safely handle.

The mantra: **the inspector is the product; transfers are a first-class superpower; performance and
beauty are features.**

## What it is

- A **Swift-native** operator tool — containers, objects, prefixes, **segments**, DLO/SLO manifests,
  storage policies, ACLs, versioning, TempURL, quotas, CORS, staticweb — not a file-manager metaphor.
- **Cross-account / cross-cluster** copy and mirror as a first-class workflow.
- **Safety-first**: destructive actions explain scope; heuristics never authorize destruction;
  object-metadata edits never silently drop keys.
- **Cross-platform** desktop app — Linux, macOS, and Windows on both **x86_64 and aarch64** (six
  targets, all required); Linux is the primary/reference platform.

## What it is not

- Not a `python-swiftclient` port, not a general S3 browser, not a FUSE mount, not a Keystone UI (v1).

## Stack

Go core · `github.com/ncw/swift/v2` (wrapped) · **Wails v3** desktop shell · Svelte 5 + TypeScript UI ·
pinned rclone subprocess for bulk movement · SQLite (non-secret state) · OS keychain / opt-in encrypted
vault for secrets.

## Documents

- [`docs/DESIGN.md`](docs/DESIGN.md) — authoritative product + engineering design (66 sections).
- [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) — compact normative requirement IDs for phase gates.
- [`docs/adr/`](docs/adr/) — architecture decision records (0001–0012; 0006 still proposed).
- [`docs/assets/REGENERATION.md`](docs/assets/REGENERATION.md) — Swift-correct specs for regenerating the mockups.
- `docs/assets/draft/*.png` — visual direction only. ⚠ The current AI-generated PNGs contain S3-flavoured
  terminology errors; `DESIGN.md` §4.5 and the regeneration specs are authoritative until they are redone.
- [`docs/rclone.md`](docs/rclone.md) — the exact pinned-rclone flag surface and measured behaviours.
- [`docs/RELEASING.md`](docs/RELEASING.md) — the six-target release pipeline (unsigned for now, on purpose).
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
   (destructive: typed confirmation, delete half runs only after the copy half verifies).
   Follow everything in the jobs drawer — progress, throughput, cancel, audit trail.
6. Destructive cleanup: **Delete…** for prefix/empty/delete-container (typed confirmation),
   and manifest deletion from the inspector — segments die only under recorded ownership
   proof or an explicit Backstage override.

### Before trusting a release against production

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
