# AGENTS.md — start here

**Aeronautes** ("air sailor" — a genus of swifts) is a beautiful, performance-first **desktop
operator workbench for OpenStack Swift / SwiftStack**: it makes Swift's control-plane semantics
understandable and safe, and uses rclone + native Swift to move bulk data as fast as the target
cluster can safely handle.

**Status: implementation in progress.** The design is locked. Your job is to build it — commit by
commit, phase by phase, following the design.

## Read these first (authoritative)

1. [`docs/DESIGN.md`](docs/DESIGN.md) — the authoritative product + engineering design (66 sections).
   If implementation experience requires changing a *locked* decision, **update this document (and add
   an ADR) before changing code**.
2. [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) — the hard constraints as stable IDs (`K-*`) with a
   phase→ID gate table. Phase gates verify these.
3. [`docs/assets/screenshots/`](docs/assets/screenshots/) — real captures of the built app
   against containerized Swift (the retired draft mockups' §4.5 corrections remain normative).
   Do **not** copy labels/fields from them; §4.5 is authoritative. They are visual direction only.

## Non-negotiable invariants (see the sections for the full rules)

- **The rclone boundary is absolute.** rclone writes only planned-new or uniquely-staged names; it
  never overwrites or deletes an existing object — no `sync`, `purge`, or `delete`, on any scope.
  All destruction is **Aeronautes-native** and manifest-only, with an authoritative HEAD before any
  mutation whose semantics depend on the object's current identity. (§28.6, §36, K-XFER-014)
- **Automatic segment deletion needs `ExclusiveOwnershipProof`** through the destructive effect.
  Generated prefixes prove creation and `Confirmed` proves a reference; neither excludes sharing or concurrent writers.
  Recovery retains uncertain data and records under ADR 0020; explicit force cleanup is a separate action. (§28.6, K-SEG-004)
- **Overwriting any changed object uses the stage-and-commit protocol** (rclone stages to a unique
  temp name; Aeronautes commits natively — a DLO-manifest PUT for large objects, a server-side COPY
  for plain ones). (§28.6)
  Stage-and-commit alone does not protect a concurrent replacement.
  Mirror starts are refused under ADR 0020.
  P05's unsupported overwrite refusal remains pending its separate implementation.
- **Listings are advisory (eventually consistent); HEAD is authoritative.** Verify with bounded
  retries; namespace comparison is a streaming merge-join over the sorted listings. (§15.2, §15.3, K-CON-001)
- **Secrets never reach the frontend or SQLite**; rclone gets an invocation-local gateway token through named-remote env config, never the durable key or real Swift token, and never through argv. (§55, §26.2, ADR 0014, K-SEC-*)
- **Never HEAD every listing row**; stream large namespaces (never accumulate). (§14.2, §15)
- **Swift semantics live in Go domain packages**, not UI components. Don't port `python-swiftclient`;
  wrap `github.com/ncw/swift/v2`.
- **Test object-name encoding through both engines** with a native fallback (§14.3, K-TEST-001).
- **UI**: SLO = *Static* Large Object; segment/chunk vocabulary, never S3 "multipart"/`x-amz-*`.
- Preserve the Svelte visual direction; surface Swift transaction IDs; make destructive plans explicit.

## Testing is non-negotiable

Aeronautes moves and deletes **people's data**, and a bug can be irreversible. The test suite is a
first-class deliverable — untested destructive, classification, or encoding code does not ship, and CI
must be green.

- **Every PR must run CI and pass before merging**, including documentation and CI changes.
  Require successful Go, frontend, real-Swift integration, smoke and aggregate checks on the latest PR head with the current master incorporated.
  Missing, pending, skipped, cancelled or failing checks are blockers; an older green run does not qualify.
  Refresh the branch and rerun verification and independent local review after a rebase or other history rewrite.
  Use the [protected merge procedure](docs/CI.md), merge only the verified head, and check the resulting master pipeline.
  Stop feature merges and repair a broken master before continuing.
  Never bypass the ruleset or push directly to master, including for generated files.
- **Test the dangerous paths first and hardest** — the four layers in §57 against the fixture namespace
  in §58: object-name encoding on both engines; DLO/SLO classification; segment evidence and
  exclusive-ownership; the mutation planner (metadata replace-all, DLO preservation); the delete planner;
  and the stage-and-commit state machine with a crash at every boundary.
- **Every safety requirement (`K-*`) needs a test that fails when the rule is broken** — especially the
  shared-segment corruption fixtures and the plain→DLO race (§28.6, §28.4).
- **Prefer fast, deterministic tests**: pure unit + mock-HTTP contract tests (§57.1–57.2) for the bulk;
  local real Swift / SAIO for integration (§57.3); a representative real deployment as the release gate
  (§57.4). Use tiny large-object thresholds so segmentation is exercised cheaply.
- **No destructive test runs against real user data** — only Aeronautes-owned test containers; a
  benchmark needs a dedicated bookmark or a typed cluster-name confirmation (§42).

## Stack & targets

Go core · `github.com/ncw/swift/v2` (wrapped) · **Wails v3** (beta, chosen — §6.6) · Svelte 5 + TS ·
pinned rclone subprocess · SQLite (pure-Go driver recommended, §51) · OS keychain / opt-in vault.
Must build and run on **{Linux, Windows, macOS} × {x86_64, aarch64}** — six targets (§61.0). Linux is
the reference/first-polish platform. App id: `io.csmart.aeronautes`.

## Where to start

- **First milestone = the §65 commit sequence** (Go core: SQLite, SecretStore, `ncw/swift/v2`
  adapter, v1 auth, `/info`, account HEAD, listings — then the Wails v3 + Svelte 5 shell, design
  tokens, static browser/inspector, bookmarks, live wiring). One component per commit, each with
  its tests.

  **Current state (2026-08-30): Phases 0–7 are complete and the §65.9 adversarial-review fixes
  are in** — browse/inspect/mutate, cross-account Copy with stage-only engine writes and a durable
  commit protocol, executable Mirror with fresh delete membership, local up/download behind one
  cross-platform namespace policy, the destructive-job family with exact accounting, evidence-gated
  segment cleanup, TLS trust identities, and release hardening. Gate evidence lives in
  `DESIGN.md` §65.x status blocks; items §65.9 defers are named in §65.10, not implied done.
  Requirements not yet met are marked in `docs/REQUIREMENTS.md` context: adaptive concurrency
  (K-PERF-003), plan tokens, the six-platform materialization matrix, vault (ADR 0006). New work
  means a new §65.x commit-sequence plan first, same discipline — and the test rule is phased: until the
  §65.10 behavioral harness lands, no merge without a regression test (unit/SAIO); once it
  exists, every behavior-changing feature adds a system-matrix row and test, and every bug fix
  carries the system-level reproduction that failed before it. No system test, no merge.
  The preceding dated capability list is historical: Mirror now supports comparison preview only and refuses every start until P14 establishes the required concurrency boundary.
- **CI is live** (`.github/workflows/ci.yml`): Go, frontend, containerized-Swift integration, headless smoke and their aggregate gate are required checks on master through PRs.
  The six-target release matrix builds unsigned artifacts on dispatch/tag (§61.0).
- **Highest-value early task: the disposable Phase-0 rclone-seam experiment** (§60) — it runs in
  parallel with Phases 1–4 (start with token-lifetime and `override.*` TLS, the cheapest assumptions
  to falsify) and must finish before the Phase-5 transfer abstraction freezes. It will invalidate
  something.
- **The Tier-1 Linux baseline is named** (§6.6, K-PLAT-002): Fedora current stable (first-class) +
  Ubuntu current LTS native; EL-family via Flatpak; EL8 unsupported. The Phase-0 webview spike
  confirms it.
- **Owner decisions**: the versioning model is pinned (§21 — upstream Swift master;
  `versioned_writes` container versioning is the behavioural target, `object_versioning`
  display-only) and so is rclone (§26.3 — `v1.74.3`, Fedora current). Still open: ADR 0006 (vault, before
  bookmarks persist secrets to a vault); 0007 is Accepted from seam-spike evidence (docs/rclone.md).

## Code style — plain and simple

**Write for the next human who reads this.** Prefer plain and simple over tricky and complex;
concise and clear over verbose and confusing. If a reviewer has to stop and work out *how* the code
works, simplify it.

- **Clarity beats cleverness.** The obvious solution that reads top-to-bottom wins over a clever
  one-liner. Don't show off.
- **Simple first.** Don't add abstraction, generality, config, or indirection until a second real
  caller needs it. Solve the problem in front of you; delete code you don't use (YAGNI).
- **Small, honest functions.** One job each, named for what they do. Prefer early returns over deep
  nesting.
- **Name things clearly.** A longer clear name beats a cryptic short one. Use the domain vocabulary
  from `docs/DESIGN.md` (containers, prefixes, segments, DLO/SLO, manifest, …).
- **Comments explain *why*, not *what*.** The code already says what. A comment earns its place by
  explaining intent, a non-obvious constraint, or a Swift/rclone gotcha — not by narrating obvious lines.
- **Errors are explicit.** Handle them where they happen, wrap with context, never swallow.
- **Boring tech wins.** Prefer the standard library and a few well-chosen deps. Match the style of the
  code around you.
- **Concise, not cryptic.** Short because it's clear, never short because it's dense. A well-named
  helper and some whitespace are free.

Go: `gofmt`/`goimports` + `go vet` clean; idiomatic Go; small consumer-defined interfaces; return
`error` and wrap with `%w`; `context.Context` for anything long-running or I/O-bound. Reach for
generics/reflection/channels only when they make the code *simpler*.

Svelte 5 / TypeScript: small focused components, typed props, no `any`, logic out of the markup. The
UI is presentation — real logic lives in the Go core (§55).

Tests & docs: tests read like documentation (clear names, table-driven where it fits, assert behaviour
not internals); cover the encoding, classification, and destructive-planning paths (§57) first. Write
docs and commit messages the same way as code — plain and clear, no filler.

## Working conventions

- Follow the phase plan in §60; each phase has a Gate. Don't skip semantic foundations for a flashy UI.
- **Commits are clean and atomic — one logical change each**, and every commit builds with its tests
  passing. §65 is the commit-shaped sequence for the first milestone; expand each later phase into a
  commit sequence the same way before starting it. Never batch a phase into one commit.
- **Commit messages are detailed**: a lowercase subject ≤50 chars, then a prose body that explains the
  problem or feature, the fix or implementation approach, and the tests that cover it. No
  `Co-Authored-By` trailer.
- Branch before committing on the default branch. Keep the design and code in sync.
