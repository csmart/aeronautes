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
3. [`docs/assets/REGENERATION.md`](docs/assets/REGENERATION.md) — specs to regenerate the mockups.
   ⚠ The PNGs in `docs/assets/draft/` are AI-generated and **wrong for Swift** (S3-flavoured terms).
   Do **not** copy labels/fields from them; §4.5 is authoritative. They are visual direction only.

## Non-negotiable invariants (see the sections for the full rules)

- **The rclone boundary is absolute.** rclone writes only planned-new or uniquely-staged names; it
  never overwrites or deletes an existing object — no `sync`, `purge`, or `delete`, on any scope.
  All destruction is **Aeronautes-native** and manifest-only, with an authoritative HEAD before any
  mutation whose semantics depend on the object's current identity. (§28.6, §36, K-XFER-014)
- **Segment deletion needs `ExclusiveOwnershipProof`** — a unique Aeronautes-recorded prefix.
  `Confirmed` evidence proves a segment is *referenced*, never *unshared*. (§28.6, K-SEG-004)
- **Overwriting any changed object uses the stage-and-commit protocol** (rclone stages to a unique
  temp name; Aeronautes commits natively — a DLO-manifest PUT for large objects, a server-side COPY
  for plain ones). (§28.6)
- **Listings are advisory (eventually consistent); HEAD is authoritative.** Verify with bounded
  retries; namespace comparison is a streaming merge-join over the sorted listings. (§15.2, §15.3, K-CON-001)
- **Secrets never reach the frontend or SQLite**; rclone gets a token, never the durable key, via
  named-remote env config, never argv. (§55, §26.2, K-SEC-*)
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

- **CI runs on every push/PR** (`.github/workflows/ci.yml`): `gofmt`, `go vet`, `go build`,
  `go test -race ./...`, plus frontend lint/build. It activates automatically once `go.mod` /
  `frontend/` exist. Don't merge red.
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
  its tests. No transfers, no destructive jobs yet.
- **CI is already wired** (`.github/workflows/ci.yml`); it activates the moment `go.mod` / `frontend/`
  land — keep it green from the first code commit. The six-target build/sign matrix (§61.0) is added in
  Phase 0/7; a containerized-Swift integration job is added when Phase 1 starts (§57.3).
- **Highest-value early task: the disposable Phase-0 rclone-seam experiment** (§60) — it runs in
  parallel with Phases 1–4 (start with token-lifetime and `override.*` TLS, the cheapest assumptions
  to falsify) and must finish before the Phase-5 transfer abstraction freezes. It will invalidate
  something.
- **The Tier-1 Linux baseline is named** (§6.6, K-PLAT-002): Fedora current stable (first-class) +
  Ubuntu current LTS native; EL-family via Flatpak; EL8 unsupported. The Phase-0 webview spike
  confirms it.
- **Pin these owner decisions before the relevant phase**: the target cluster's concrete versioning
  model (§21); the exact rclone version for all six targets (§26.3); and resolve the Proposed ADRs
  (0006 vault before bookmarks persist secrets to a vault; 0007 flag surface before Phase 5).

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
