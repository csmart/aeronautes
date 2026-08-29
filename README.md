# Aeronautes

> **Status: implementation in progress.** The design is locked; code is built one component per
> commit, following the sequence in [`docs/DESIGN.md`](docs/DESIGN.md) §65. The v0.1 vertical
> proof — bookmark → auth → containers → objects → inspector HEAD in the four-pane UI — runs
> end to end (against the bundled mock cluster; SAIO integration lands with Phase 1).

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
- [`docs/adr/`](docs/adr/) — architecture decision records (0001–0012; 0006 and 0007 still proposed).
- [`docs/assets/REGENERATION.md`](docs/assets/REGENERATION.md) — Swift-correct specs for regenerating the mockups.
- `docs/assets/draft/*.png` — visual direction only. ⚠ The current AI-generated PNGs contain S3-flavoured
  terminology errors; `DESIGN.md` §4.5 and the regeneration specs are authoritative until they are redone.
- [`AGENTS.md`](AGENTS.md) — start-here guide for coding agents.

## Who it's for

OpenStack Swift / SwiftStack operators managing large namespaces: browsing, inspecting, safely mutating
metadata/ACLs, and running cross-account replication and bulk deletes.

## Licence

[Apache License 2.0](LICENSE) © 2026 Chris Smart.

Compatible with the dependencies: rclone and `ncw/swift/v2` are MIT, which combines freely into an
Apache-2.0 project. rclone is bundled as a separate binary (subprocess), so its licence does not
constrain Aeronautes' — when distributing the bundled rclone binary, include rclone's MIT licence/notice in
the third-party notices (a `NOTICE` / `THIRD_PARTY_NOTICES` file, added when code lands).
