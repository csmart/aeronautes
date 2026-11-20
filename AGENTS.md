# AGENTS.md — start here

**Aeronautes** ("air sailor" — a genus of swifts) is a desktop operator workbench for the OpenStack Swift API and compatible implementations.
It makes Swift's control-plane semantics explicit and uses a pinned rclone subprocess and native Swift requests to move data.

**Current release line: 0.5.0.**
The application is implemented; new work maintains and extends the current behaviour rather than restarting the original phase plan.
The early release is not recommended for production or valuable data; the [release notes](docs/releases/v0.5.0.md) and [release guide](docs/RELEASING.md) state its limits and outstanding acceptance.

## Read these first (authoritative)

1. [`docs/DESIGN.md`](docs/DESIGN.md) — the authoritative product + engineering design (66 sections).
   If implementation experience requires changing a *locked* decision, **update this document (and add
   an ADR) before changing code**.
2. [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) — the hard constraints as stable IDs (`K-*`) with a
   phase→ID gate table. Phase gates verify these.
3. [`docs/adr/`](docs/adr/) — read the current decisions for the area being changed, especially ADR 0122 for transfer and deletion outcomes.
   Earlier DESIGN phase lists and dated observations can describe superseded behaviour; check the current callers and accepted amendments.
4. [`docs/CI.md`](docs/CI.md) and [`docs/journeys.md`](docs/journeys.md) — required checks, protected merges and the GUI coverage catalogue.
5. [`docs/assets/screenshots/`](docs/assets/screenshots/) — the current app and historical captures, labelled in the gallery.
   Do not copy old controls or labels into new work; DESIGN §4.5 and the current behaviour contract govern the UI.

## Non-negotiable invariants (see the sections for the full rules)

- **The store's answer is the result (ADR 0122).**
  By the operator's decision of 16 November 2026 the application does not guard against another program writing to the same names: it sends a request, and what the store or the transfer engine answers is the outcome.
  ADR 0122 replaces the staging, fixed-point and verifying-read rules for deletions, uploads, copies and downloads; do not restore those retired paths from an earlier design passage.
  A deletion sends each name's DELETE and reads nothing before or after it.
- **The rclone boundary.** rclone is never given `sync`, `purge` or `delete`.
  For an upload and for a copy it writes each object at its name, over what is there once the operator confirmed the replacement, and it removes a replaced large object's old segments only under the destination folder and only where the gateway lets it (ADR 0122 decisions 3 and 4).
  All other destruction is **Aeronautes-native**. (§28.6, §36, K-XFER-014)
- **Automatic segment deletion needs `ExclusiveOwnershipProof`** through the destructive effect.
  Generated prefixes prove creation and `Confirmed` proves a reference; neither excludes sharing or concurrent writers.
  Recovery retains uncertain data and records under ADR 0020; explicit force cleanup is a separate action. (§28.6, K-SEG-004)
  That force is offered for one object from Backstage, and as a bulk deletion's option (ADR 0119).
  Each starts ticked by the operator's decision, is typed by name, and is refused where the container keeps versions (ADR 0120); neither is automatic.
- **An existing Copy destination is replaced only when the operator confirmed it (ADR 0122 decisions 2 and 4)**: the preview names the collisions and the confirmation is asked on a native dialog.
  A name the destination holds differently is refused when the copy was not confirmed; a refused overwrite produces a partial job and is counted separately.
  A confirmed copy writes over what is at the name when its request arrives: nothing is compared again, no request carries a condition, and no name is read after its write.
  A name the two listings show as the same is skipped; a pair they cannot compare is read once on each side.
  A replaced large object's old segments follow the upload's rule.
  Mirror starts are refused under ADR 0020; ADR 0086 is a proposal for it and is not built. (§28.6)
- **Listings are advisory (eventually consistent); HEAD is authoritative.** Namespace comparison
  is a streaming merge-join over the sorted listings. (§15.2, §15.3, K-CON-001)
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
  and each transfer's outcomes from what the engine or the store answered, with a stop at every boundary.
- **Every safety requirement (`K-*`) needs a test that fails when the rule is broken** — especially the
  shared-segment corruption fixtures and the plain→DLO race (§28.6, §28.4).
- **Prefer fast, deterministic tests**: pure unit + mock-HTTP contract tests (§57.1–57.2) for the bulk;
  local real Swift / SAIO for integration (§57.3); a representative real deployment as the release gate
  (§57.4). Use tiny large-object thresholds so segmentation is exercised cheaply.
- **No destructive test runs against real user data** — only Aeronautes-owned test containers; a
  benchmark needs a dedicated bookmark or a typed cluster-name confirmation (§42).

## Four standing preferences

Judge every change, and every review of one, against these four:

- **Maintainable by humans.** The next reader can follow it without its author.
- **Suited to the problem.** The design fits what is being solved, not a wider problem nobody has.
- **The smallest change that meets the contract.** No speculative options or layers, and no refactor of nearby code.
- **Tests proportionate to the behaviour they protect.** Enough to catch the rule being broken, and no more.

Name avoidable complexity, indirection or test bloat as a review finding, and say when the complexity that remains is justified.

### Keeping the tests minimal, targeted and up to date

- **One home for each rule.**
  Swift semantics, planning and safety are tested in the Go package that owns them, against SAIO when the store's behaviour is the question.
  How the window behaves is tested once, in a component test of the lowest component that owns it.
  What an operator can do end to end is one browser journey (`uijourneys/`), on the mock and, run locally, on the real clouds.
  Do not pin the same rule at two layers unless each can fail by itself.
- **Everything the window offers has a row** in the coverage table of [docs/journeys.md](docs/journeys.md).
  A new feature arrives with its browser journey, and a change or removal changes the row in the same pull request.
  A row may say "not yet"; it may not be missing or stale.
- **A test must be able to fail.**
  Before review, break the rule it protects and watch it fail, without committing the break; a test that cannot fail is deleted.
- **Prove the outcome with an independent read where one exists:** bytes compared by digest, a second session's listing, a fresh connection.
  The code under test does not mark its own work.
- **Tests change with the behaviour.**
  Change or delete the tests that pinned the old behaviour in the same commit, and remove a feature's tests with the feature.
  Never skip or weaken a test to get green.
- **A bug found by hand gets the test that would have caught it,** with its fix, at the lowest layer that can catch it.
- **Do not add a test because a line changed,** or one that mirrors the implementation.
  Extend an existing case or table before adding a file, and keep fakes at external boundaries.
- **A test that fails now and then is a finding.**
  Find whether the store lagged or the product is wrong and fix that; never retry until green.
- **Keep the manual test plan for gaps automation has not covered.**
  The [0.5.0 plan](docs/audits/2026-11-11/0.5.0-test-plan.md) accepts native automation for the specific dialog, clipboard, focus and keyring backends it exercises; other desktops, installation and visual acceptance still need their own evidence.

## Stack and targets

Go core · `github.com/ncw/swift/v2` (wrapped) · Wails v3 · Svelte 5 + TypeScript · pinned rclone subprocess · SQLite through `modernc.org/sqlite` · OS keychain, with an explicit session-only fallback.
The encrypted vault remains a proposal (ADR 0006), not an available credential store.
Use the dependency pins in `go.mod`, the frontend lockfile and `internal/transfer/rclone/manifest.txt`; `go.mod` sets the Go language floor, while CI uses the current patch release.

The build matrix is **{Linux, Windows, macOS} × {x86_64, aarch64}** (§61.0).
Linux is the supported reference platform; Windows and macOS downloads are testing bundles without completed native desktop acceptance.
All 0.5.0 downloads are unsigned, and macOS builds are not notarised.
App ID: `io.csmart.aeronautes`.

## Where to start

- **Trace the current operation before changing it.**
  The application browses and edits Swift state, uploads, downloads and copies data, and performs confirmed deletions with per-object job records.
  It supports TempAuth v1 and Keystone v3, bookmark import, configuration backup and restore, and diagnostics.
  Transfer and deletion outcomes follow ADR 0122; Copy replaces an existing destination only after confirmation.
- **Preserve the roadmap without exposing unfinished features.**
  Mirror is planned in ADR 0086; every execution start remains refused, and its backend comparison preview is preserved but not offered in the release dialog.
  Benchmarks and the vault also remain unavailable.
  New work follows the [Linux finish line](docs/audits/2026-09-05/linux-finish-line.md) and DESIGN §65.12: name the behaviour oracle, keep changes atomic and review a changed locked contract before dependent code.
- **Build from a fresh checkout using the [README](README.md#building-on-fedora).**
  `make tools` installs the pinned Wails CLI; `make build` builds the frontend, bindings and application.
  Go builds and tests include the root package, which embeds `frontend/dist`; build that frontend first, including in a new worktree.
  Put the verified official rclone pin on `PATH` for transfers and mock transfer tests; the README's [quickstart](README.md#quickstart) explains the fetch command.
- **Use the existing test entry points.**
  `make test` runs the Go race and frontend suites; `make uijourneys` runs mock browser journeys with no cloud configuration.
  `make test-integration` exercises the adapter against an existing SAIO; the [CI guide](docs/CI.md) describes the wider two-account system suite, required environment and longer package timeouts.
  Never restart or recreate a shared Swift fixture as part of a test run.
  Provider oracles and browser/service journeys run locally with dedicated test accounts; keep their credentials out of GitHub.
- **Use the real release workflow.**
  The five required PR checks cover Go, frontend, real Swift integration, native Linux smoke with browser journeys, and the aggregate gate.
  Verify the resulting master pipeline after the protected merge.
  Follow [RELEASING](docs/RELEASING.md) for the tagged six-target build, provenance checks and publication; a successful build is not native desktop or provider acceptance.
- **Keep platform and protocol limits explicit.**
  Fedora current stable is the Tier-1 reference, Ubuntu current LTS is the native DEB target, and EL-family support is planned through Flatpak; EL8 is unsupported (§6.6, K-PLAT-002).
  Container versioning through `versioned_writes` is the behavioural target; `object_versioning` is display-only (§21).
  The original milestones and seam experiments are recorded in DESIGN §§60 and 65 and `docs/rclone.md`; do not treat completed experiments as new tasks.

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

- Follow the current accepted decisions and release scope; retain the applicable requirement gates when changing behaviour.
- **Commits are clean and atomic — one logical change each**, and every commit builds with its tests
  passing. Use the original §65 sequence as an example of reviewable increments, not a list of work to repeat.
- **Commit messages are detailed**: a lowercase subject ≤50 chars, then a prose body that explains the
  problem or feature, the fix or implementation approach, and the tests that cover it. No
  `Co-Authored-By` trailer.
- Branch before committing on the default branch. Keep the design and code in sync.
- Apply DESIGN §65.12 to new work: short subject records, durable private evidence packets, related documentation in the implementation PR, and operator sign-off before expanding the frozen Linux list.
