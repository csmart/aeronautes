# Mockup regeneration specs

These specs regenerate the four `assets/*.png` mockups with **Swift-native terminology**.
The current PNGs were AI-generated and contain S3-flavoured errors; see `DESIGN.md` §4.5.
Feed one section at a time to an image generator (or hand it to a designer). The **House
style** section applies to all four screens; each screen section then lists the exact,
corrected on-screen content.

> Rule for every screen: Swift vocabulary only — containers, objects, prefixes, **segments**,
> **DLO/SLO manifests**, storage policies, ACLs, `X-*-Meta-*` headers, **ETag (MD5)**.
> Never `x-amz-*`, "multipart", "part", "bucket", "S3-compatible", "Stacked", `EC:4+2`, or `CRC32C`.

---

## House style (applies to all four screens)

**Product:** Aeronautes — a desktop operator workbench for OpenStack Swift / SwiftStack.
macOS-style desktop application window.

**Visual system** (DESIGN.md §4.1):
- warm cream / stone backgrounds (approx `#FBF9F5` app, `#F5F1EA` panels);
- graphite / ink text (approx `#1E1B18` headings, `#4A4640` body, `#8A857C` secondary);
- a restrained champagne / brass accent (approx `#B8925A`), used only for selection,
  active state, links, and the primary button;
- colour reserved for state: danger red, success green, semantic badges — everything else neutral;
- subtle depth (soft 1px borders + faint shadow), carefully rounded cards (~10–14px radius);
- dense information, breathing room around major panes, tabular figures for all numbers;
- warm light theme shown here; a warm dark theme also exists.

**Avoid** (DESIGN.md §4.1): Horizon-style blue admin tables; Cyberduck utility chrome;
Electron/VS Code visual language; purple gradients; stock shadcn look; mascots; excessive
toolbar icons; giant whitespace that reduces scan speed.

**Window chrome (all screens):**
- top-left: three macOS traffic-light dots (red/amber/green);
- top-centre: a small swift-in-flight silhouette + wordmark **Aeronautes**;
- top-right: a "Quick actions…" search field (with `⌘K` hint) and a notification bell.
- **Left bookmark rail** (narrow, icon + label, vertical): `Prod AU` (selected, sun icon),
  `DR EU` (globe), `Archive` (crate), `Dev SAIO` (mountain). At the rail's foot: `+`, a gear,
  and a moon (theme) icon.
- **Bottom status bar** (full width): left a **real app/connection status** (e.g. `● Connected · Prod AU`
  or `● 2 jobs running`) — ⚠ **not** an unsubstantiated `All systems nominal`; Aeronautes has no cluster-health
  probe (K-UI-004/005); centre **account-level** usage
  from the account HEAD, e.g. `Account: 1.82 PB · 12.4M objects` (with a thin brass meter only if a
  quota is set) — ⚠ **not** cluster capacity like "1.82 PB / 4.00 PB": a tenant cannot see cluster
  totals, only its own account usage/quota; right `Go backend v1.24.2 · Svelte frontend v1.12.1`.

> ⚠ **Linux frame variant.** These specs describe macOS chrome (traffic lights, `⌘K`). Because Linux
> is the release-quality target, also produce a Linux variant of the primary screenshots with a
> GTK/client-side-decoration frame, so the first Linux screenshot doesn't read as a macOS port.

**Typography:** legible modern UI face for dense rows; a slightly more characterful face for
headings and the selected resource name; narrow monospace for ETags, tokens, object paths,
request IDs, raw headers. Tabular figures for sizes, rates, counts, timestamps.

---

## 1. `browser-inspector.png` — Browser + inspector

Four columns under the window chrome, plus a transfer drawer docked at the bottom.

**Column A — Containers** (header "Containers" + `+`): a segmented control `Logical | All | Backstage`
(Logical selected). Container rows (icon, name, size · object count):
- `photos` — 12.4 TB · 1.2M objects (selected, brass left-edge highlight, a small dot)
- `logs` — 3.6 TB · 18.7M objects
- `analytics` — 1.1 TB · 4.3M objects
- `backups` — 8.7 TB · 522K objects
- a subdued group header `POSSIBLE BACKSTAGE · 2` with a classify affordance, then:
  - `photos_segments` — — · 3.4M objects
  - `logs_index` — — · 912K objects
- footer: a `Show hidden containers` toggle (off).

**Column B — Working list**:
- breadcrumb: `⌂ / photos / 2026 / wedding /` (last crumb brass).
- a `Prefix` field showing `wedding/`; a `Filter this prefix…` field with a funnel icon;
  a distinct **`Find in container`** button (search icon).
- column headers: `Name ▲ | Size | Last modified` with a small columns/settings icon.
- rows (folder/prefix rows show a folder icon and `—` for size):
  - `ceremony/` — May 12, 2026, 10:21
  - `reception/` — May 12, 2026, 11:05
  - `portraits/` — May 12, 2026, 09:14
  - `details/` — May 11, 2026, 18:42
  - `wedding-video.mp4`  badge **`SLO`** + a small stacked-layers glyph — 42.7 GB — May 12, 2026, 12:31  **(selected row, checkmark, brass tint)**
  - `teaser.mov` — 1.2 GB — May 12, 2026, 12:10
  - `highlight-reel.mp4`  badge **`DLO`** — 6.3 GB — May 12, 2026, 12:05
  - `first-look.jpg` — 5.8 MB — May 12, 2026, 08:23
  - `drone-aerial.mp4` — 3.9 GB — May 12, 2026, 11:42
- footer: `1 selected` (left) · `8 items` + refresh icon (right).

**Column C — Inspector** (always visible). Header: a play glyph, the object name
**`wedding-video.mp4`**, a `SLO` badge, and a pill reading **`STATIC LARGE OBJECT`**
(⚠ NOT "Stacked"), plus a close `✕`.
- tabs: `Overview | Large Object | Metadata | Headers | Raw` (Overview active).
  ⚠ **No "ACL" tab on an object** — Swift ACLs are container-level, not per-object; object inspectors
  show headers, not ACLs.
- **Overview** key/value rows:
  - Type — **`Static Large Object (SLO)`**
  - Logical size — `42.7 GB`
  - Last modified — `May 12, 2026, 12:31:48 AEST`
  - Content type — `video/mp4`
  - ETag — mono `9f2b8c6e4d2a1f2b7e9d3c8a4b1f0e21`
  - Backstage — `Backstage · photos_segments / wedding-video/` (link with external-link icon)
- **Large Object** card (collapsible):
  - summary fields — `Total segments 43` · `Segment size 1.00 GB` · `Manifest ETag`
    (mono, small) — **remove the "Redundancy EC:4+2" and "Checksum CRC32C" fields entirely.**
  - "Segment preview" table, columns: `# | Segment path | Size | ETag (MD5) | ↗`
    (⚠ the hash column header is **ETag (MD5)**, not "Checksum CRC32C"):
    - `1 | photos_segments/wedding-video/00000000 | 1.00 GB | a94f2c1e… | ↗`
    - `2 | photos_segments/wedding-video/00000001 | 1.00 GB | b8de91a4… | ↗`
    - `3 | photos_segments/wedding-video/00000002 | 1.00 GB | c13a7b6e… | ↗`
    - `…`
    - `43 | photos_segments/wedding-video/0000002A | 700.0 MB | f0e1d2c3… | ↗`
      (42 × 1.00 GB + 700 MB = the 42.7 GB logical size — keep the arithmetic consistent)
- **Metadata** card — **`X-Object-Meta-*` headers** (⚠ NOT `x-amz-meta-*`):
  - `X-Object-Meta-Shoot` — `wedding`
  - `X-Object-Meta-Location` — `sydney`
  - `X-Object-Meta-Camera` — `sony-fx6`

**Transfer drawer (bottom):** database-arrow icon, `Production AU → DR EU`, a `Copy` chip,
`● Active · Started 2m 14s ago`; centre `62%` with progress bar and `1.28 TB / 2.05 TB`;
`312 MB/s · Average throughput`; `24 / 64 · Transfers / Checkers` with a sparkline; pause + `…`.

---

## 2. `transfer-planner.png` — Transfer planner

Four regions: Source tree, Destination tree, Transfer plan, Impact — plus a "Recent transfers"
drawer at the bottom.

**Source** (`● Production AU`): path `/ photos / 2026 / wedding /` with a `Browse` button;
a file tree with `photos ▸ 2026 ▸ wedding` (selected), `engagement`, `family`, plus `2025`,
`2024`, `videos`, `raw`, `README.md`. A "Source summary" card: `Total objects 8,192` ·
`Total size 42.7 GB` · `Last modified May 12, 2026, 12:31`.

**Destination** (`● DR EU`): path `/ photos-archive / 2026 / wedding /` + `Browse`; mirror-image
tree under `photos-archive`. "Destination summary": `Total objects 7,146` · `Total size 38.2 GB`
· `Last modified May 11, 2026, 18:42`.

**Transfer plan** (centre):
- a large two-way toggle: **`Copy`** (selected, checkmark) vs **`Mirror`** (⚠ with a warning
  glyph and subtitle "Deletes destination-only objects").
- **Large object handling** card:
  - `Segment placement → [ Sidecar ▾ ]` — Sidecar / In-container / rclone-default. ⚠ **Not** an
    editable container/prefix name: rclone chooses the per-upload prefix (§26.4). Show the resolved
    sidecar container **read-only**: `photos-archive_segments`.
  - `Chunk size → 5 GiB` (editable, pencil; one control, ≤ `max_file_size` — §25)
  - **⚠ remove the "Redundancy EC:4+2" and "Checksum CRC32C" rows** — they are not manifest
    fields (DESIGN.md §4.5).
- **Analysis** (`8,192 source objects`, "Compared just now", refresh):
  - `● New (will be copied) — 1,248 — 12.4 GB`
  - `● Changed (will be updated) — 368 — 6.1 GB`
  - `● Unchanged (will be skipped) — 6,386 — 23.7 GB`
  - `● Destination-only (kept) — 1,114 — 4.3 GB`
  - totals: `Total source size 42.7 GB` · `Total destination size 38.2 GB` ·
    `Estimated data to transfer 18.5 GB (43%)`.
- `Performance [ Auto ▾ ]  ·  24 transfers / 64 checkers`  ·  `Advanced settings` (gear).
- buttons: `Review changes` (secondary) and **`▶ Start copy`** (primary, brass).
- caption: `Copy preserves destination-only objects. Same-name changed objects will be overwritten.`
  (⚠ **not** "Copy is non-destructive" — DESIGN.md §27.1; keep the 368-changed row and the Impact overwrite counts.)

**Impact** (right rail): heading `Copy will…`:
- `Copy new objects — 1,248 — 12.4 GB`
- `Update changed objects — 368 — 6.1 GB`
- `Skip unchanged objects — 6,386 — 23.7 GB`
- `Keep destination-only objects — 1,114 — 4.3 GB`
- a warning card: **`⚠ Mirror would delete destination-only`** — "Mirror will make destination
  identical to source by deleting 1,114 objects (4.3 GB) that exist only on the destination." +
  a `Switch to Mirror ⚠` button.
- "Transfer details": Mode `Copy` · Source `Production AU` · Destination `DR EU` ·
  Source path `/photos/2026/wedding/` · Destination path `/photos-archive/2026/wedding/` ·
  **Data path `Client-side — bytes transit this machine`** (⚠ cross-cluster rclone hairpins through
  the workstation, DESIGN.md §28.5; show `Server-side COPY` only when capability-gated) ·
  Started by `acme:ops` (⚠ TempAuth `account:user`, not an email address).

**Recent transfers drawer:** row 1 `Copy · photos/2026/wedding/ → photos-archive/2026/wedding/ ·
● Running · 62% · 11.6 GB / 18.5 GB · 312 MB/s · 24/64`. Row 2 `Mirror · videos/2025/ →
videos-archive/2025/ · ● Completed · 100% · 8.3 GB / 8.3 GB · 298 MB/s · 24/64`.

---

## 3. `performance-settings.png` — Performance settings

Left settings nav + a Performance page. This screen has the **most corrections** — the original
used S3 "multipart/part" language and a forbidden 64 MB default.

**Settings nav** (search field on top): `General`, `Performance` (selected),
`Swift API`, `Transfers`, `Large Objects`, `Safety`, `Diagnostics`, `Experimental`,
each with a short subtitle.

**Page header:** `Performance` — "Tune Aeronautes for maximum throughput and stability." Right side:
`↻ Reset to defaults` and `…`.

**Profile** segmented control: **`Auto`** (selected) `| Balanced | Aggressive | Custom ⚙`.

**Application limits** card ("Global ceilings applied across all operations", info icon) —
each row is a `[ Auto ▾ ]` control with a resolved value at the right:
- `Maximum active jobs` — **`Auto (4)`**  (⚠ NOT 256 — DESIGN.md §38.1)
- `Total transfer workers` — `Auto (24)`
- `Total checker workers` — `Auto (64)`
- `Total native Swift API workers` — `Auto (32)`
- `Memory budget` — `Auto (8 GiB)`

**Adaptive concurrency** card (`Recommended` chip), four toggles (all on):
- `Automatically tune API concurrency`
- `Back off on HTTP 429 (Too Many Requests)`
- `Back off on rising latency`
- `Back off on transport failures`

**Swift API** card ("Native Swift API behavior and tuning"):
- `Listing page size` — `Auto (10000)`
- `Delete engine` — `Auto`
- `Bulk-delete batch size` — `Auto`
- `Parallel bulk-delete batches` — `Auto`

**Transfers** card ("Data transfer and connection tuning"):
- `Transfers` — `Auto (24)`
- `Checkers` — `Auto (64)`
- `Max connections` — `Auto`
- `Buffer memory` — `Auto (512 MB)`

**Large Objects** card ("DLO / SLO handling and thresholds") — ⚠ **fully de-S3'd**; replace the
original's Multipart/Part rows and 64 MB defaults:
- `Large-object chunk size` — **`Auto (5 GiB)`** (capped at the cluster's advertised `max_file_size`) —
  ⚠ **one** control, not two: stock rclone has a single `--swift-chunk-size` governing both the
  segmentation threshold and the segment size (§25). Do not show separate "threshold" and "segment size".
  5 GiB is safe against Swift's default `max_file_size` of `5368709122`; 64 MiB is forbidden (§64).
- `Segment placement` — `Auto (<container>_segments)`
- `Failed-segment cleanup` — `On` — "rclone cleans its own segments on graceful failure."

**Expert settings** card (collapsible, warning banner "Expert settings can impact performance and
compatibility."), ⚠ **only ONE checkbox** (unchecked):
- `Enable custom pagination compatibility mode` — "Typed compatibility for non-standard backends."
  (⚠ a typed setting, not a raw rclone flag.)
- ⚠ **Do NOT include** "Disable large-object detection" (defeats segment safety; K-XFER-012) or
  "Enable server-side copy across configurations" (`--server-side-across-configs` is unsafe across two
  accounts; K-XFER-015). These retracted toggles must not reappear in the regenerated screen.

**Diagnostics / Benchmark** card ("Last run: May 12, 2026, 12:04", `Run benchmark` button),
three "Observed optimums" tiles — `Listing throughput 182,400 objects/s`,
`Delete throughput 21,440 objects/s`, `Transfer throughput 312 MB/s`. "Recommended settings"
chips: `Transfers 24` · `Checkers 64` · `API workers 48` · `Page size 10000`.
`View full benchmark results ↗`.

**Effective configuration** card ("Based on selected bookmark"): `● Production AU · Sydney,
Australia · Active`, then `Profile Auto` · `Transfers 24` · `Checkers 64` · `API workers 32` ·
`Memory budget 8 GiB`, and an `Export configuration` button + `…`.

---

## 4. `architecture-overview.png` — Architecture overview

An in-app "overview" screen: a layered stack diagram (left/centre), a "Core product pillars"
rail (right), then "Execution strategy", "Roadmap", "Design principles", and an "Included
screens" strip.

**Architecture overview** stack (top to bottom, each a labelled band with a short right-aligned
note; downward arrows between):
- `S`  **Svelte 5 + TypeScript frontend** — "Modern, reactive UI · Inspector-first"
- `W`  **Wails desktop shell** — "Native window, menus, filesystem dialogs" (⚠ **no** keychain/secret
  access on this band — secrets live behind the Go SecretStore, §55 / K-SEC-002)
- `Go` **Go application core** — "Orchestration, workers, caching, policies, **SecretStore** (all secret access)"
- `SW` **Swift control plane service** — chips: `Listings · Metadata · ACLs · Large-object logic ·
  Deletion planning`; side-tag "Owns semantics & safety"
- `rC` **rclone transfer engine** — chips: `Uploads · Downloads · Swift-to-Swift copy ·
  Mirror execution`; side-tag "Owns movement & speed"
- bottom row of three stores:
  - `SQLite state store` — "Local state & indexes"
  - `OS keychain / secrets` — "Tokens, credentials, session"
  - **`Swift / SwiftStack clusters`** — ⚠ subtitle **"OpenStack Swift · native API (TempAuth v1)"**
    (NOT "S3-compatible object stores").

**Core product pillars** (right rail, five cards): `Inspector-first UX`; `Cross-account copy +
mirror`; `Swift semantic safety` ("Never hide semantics. Surface them, respect them, protect
them."); `Performance as a feature` ("Leverage rclone's speed and Swift's scale. Measure
everything."); `Beautiful operator UI`.

**Execution strategy** table (`Operation | Engine | Strategy / Notes`) — engine chips coloured
brass (Swift) / blue-grey (rclone) / green (fastest-safe):
- `Browse / List | Swift API | Read-only listings via Swift`
- `Metadata edit | Swift API | Native metadata updates (system, user)`
- `ACL / versioning | Swift API | Preserve semantics & response headers`
- `Upload / download | rclone | Fastest path with retries, resume`
- `Cross-account copy | rclone | Client-side (bytes transit this machine); server-side COPY only when capability-gated and proven (§30, §28.5)`
- `Recursive delete | Fastest safe engine | rclone when safe, else Swift API`
- `SLO delete | Aeronautes-native | Manifest-only by default; +segments only if exclusively owned`
  (⚠ DESIGN.md §34: `multipart-manifest=delete` deletes shared segments, so it is not the default.)

**Roadmap** (numbered 0–7, matching DESIGN.md §60):
- `0 shell + semantics` — Project scaffolding, Wails v3 shell, semantics library
- `1 auth + browse` — Auth, endpoint discovery, container & object browse
- `2 inspector` — Rich inspector, metadata, ACLs, versions, headers
- `3 safe mutation` — Metadata edit, ACLs, container config
- `4 large-object read` — DLO/SLO inspectors, exact segments, ownership (read/classify)
- `5 transfers + mirror` — Uploads, downloads, cross-account Copy, Mirror
- `6 destructive jobs + performance` — Deletes, SLO+segment / DLO cleanup, strategy engine, benchmark
- `7 hardening` — Accessibility, virtualisation, packaging/signing, audit viewer

**Design principles** (five rows, from §3): `Pretty is a feature`; `Never hide Swift semantics`;
`Fastest safe path wins`; `Explain before deleting` ("Preview, simulate, confirm, then execute.");
`Advanced, not intimidating`.

**Included screens** strip: three thumbnails — `Browser + Inspector`, `Transfer Planner`,
`Performance Settings` — each with a "View details →" link.

---

## After regenerating

The current (stale) PNGs live in `assets/draft/`, and `DESIGN.md` currently embeds those `assets/draft/…`
paths. To regenerate: write each corrected PNG (from its spec above) to the **`assets/` root**, keep the
filenames, then retarget the four `DESIGN.md` `![](…)` embeds and the §4.5 wording from `assets/draft/…`
to `assets/…`, update the `README.md` mockup line, and delete `assets/draft/`. Confirm none of the forbidden terms
survive before committing.
