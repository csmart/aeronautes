# Aeronautes — normative requirements

A compact, mechanically-checkable list of the **hard** constraints from `DESIGN.md`.
`DESIGN.md` remains the authoritative rationale; this file exists so coding agents and phase
gates can reference stable IDs instead of re-reading ~11k words of prose.

- **MUST / MUST NOT** are binding. Changing one requires updating `DESIGN.md` (and an ADR where noted).
- Each requirement cites the governing `DESIGN.md` section.
- Phase gates (`DESIGN.md` §60) should list the requirement IDs they verify.
- IDs are prefixed `K-` and are **stable**. The prefix is historical (the former working name); do not rename them to match Aeronautes.
- [ADR 0122](adr/0122-the-stores-answer-is-the-result.md) (16 November 2026) replaces staging, the comparison before a write and the read after it, step by step.
  K-XFER-008, K-XFER-014, K-SEG-005 and K-CON-001 are rewritten for the steps carried out so far: deletions, uploads, copies and downloads.
  Another entry that still speaks of a stage or a staged object is history until that record's last step rewrites it.

## Security & secrets

- **K-SEC-001** — rclone MUST NOT receive the durable Swift auth key or real Swift token; it gets an invocation-local gateway `storage_url` and random local `token` only. (§26.2, ADR 0014)
- **K-SEC-002** — The frontend/webview MUST NOT receive passwords, keys, tokens, keychain handles,
  or the rclone credential environment. (§55)
- **K-SEC-003** — Secrets MUST NOT be stored in SQLite. Credentials live in the OS Secret Service, an
  opt-in reviewed encrypted vault, or session-only memory. (§12.1, §51)
- **K-SEC-004** — The encrypted vault MUST use an established, reviewed implementation/format — no
  custom cryptographic primitives. (§12.1, ADR 0006)
- **K-SEC-005** — Insecure TLS MUST be explicit, per-bookmark, visually flagged, never default. (§12.3)
- **K-SEC-006** — Logs/audit MUST NOT contain passwords, keys, tokens, TempURL secrets, or the rclone
  credential env; redaction is at the domain layer, not post-hoc regex. (§50)

## Transfer engine & data path

- **K-XFER-001** — A pre-authenticated rclone job whose token expires MUST be handled as
  child-failure → Aeronautes reauth → reconcile → **new** child, never by injecting the durable key. (§26.2, ADR 0009)
- **K-XFER-002** — After a hard interruption, Aeronautes MUST NOT automatically delete destination staging or segment objects.
  Recovery MUST retain the data and records and surface remote residue as unverified; a confirmed generated prefix does not authorise reclamation. (§26.4, ADR 0020)
- **K-XFER-003** — Graceful cancellation MUST let rclone clean its own segments; Aeronautes MUST NOT set
  `--swift-leave-parts-on-error`. (§26.4)
- **K-XFER-004** — The large-object chunk size is a single control that MUST NOT exceed the cluster's
  advertised `max_file_size` (default 5 GiB, safe against Swift's default `max_file_size` of
  `5368709122`); MUST NOT default to tiny values (e.g. 64 MiB). (§25)
- **K-XFER-005** — The transfer planner MUST show the data path (client-side vs server-side); a
  cross-remote copy is client-side (hairpins through the workstation) and MUST be labelled as such. (§28.5, ADR 0008)
- **K-XFER-006** — Aeronautes MUST NOT expose a free-form arbitrary-rclone-argument textbox in normal or
  expert UI. (§44, §44.1)
- **K-XFER-007** — rclone-executed large-object uploads stage DLOs (`X-Object-Manifest`); a native
  SLO composer is in scope only to publish a segmented upload as a static large object over the
  engine's own segments, and is the default, with a dynamic object chosen per bookmark or job.
  (§25, §62, ADR 0111)
- **K-XFER-008** — rclone `Update` (overwrite) and `Remove` (delete) bulk-delete a large object's old
  referenced segments, so the engine MUST be let delete only as K-SEG-005 allows, and its gateway refuses every delete otherwise.
  A copy MUST refuse a name the destination holds differently unless the operator confirmed replacing, including a pair the listings cannot compare that the store describes as different.
  Refused overwrites MUST be counted separately and produce a partial job; every other deletion remains Aeronautes-native. (§28.6, [ADR 0122](adr/0122-the-stores-answer-is-the-result.md))
- **K-XFER-009** — An Aeronautes-selected transfer set MUST be forced onto the wire (e.g. `--ignore-times`)
  and verified before any Mirror deletion; `--files-from` only filters the source and MUST NOT be
  relied on to override rclone's own equality test. `--ignore-existing` MUST NOT be used for a Copy
  selected set. (§28.4)
  An upload or a download the operator confirmed may replace what differs is the one exception: its
  engine is not forced, and compares each name as rclone copy does ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decision 12).
- **K-SEC-007** — rclone tokens MUST be passed by environment named-remote config only, never in argv;
  the child MUST run with a job-scoped empty config, never inheriting the user's `rclone.conf`. (§26.2)
- **K-SEC-008** — Per-bookmark TLS policy MUST be applied per-remote, never process-globally.
  The parent gateway applies captured trust to its fixed upstream; child TLS overrides are unused. (§12.3, §44.1, ADR 0014)
  The historical seam found that rclone 1.74.3 `override.*` leaks trust across remotes in one child.
  Until a separately reviewed compatibility change, endpoints with **different** trust MUST split into two single-remote children or the pairing is rejected.
  Never broaden trust process-wide.
- **K-XFER-010** — Cross-bookmark Copy MUST preserve object metadata explicitly (rclone's Swift path
  drops it): HEAD source, apply the §17.1 writable-header set; the destination keeps its own
  `X-Object-Manifest`; if data-only, the planner MUST preview every dropped category. (§27.4)
- **K-XFER-011** — Swift→local download MUST run a complete collision preflight and apply a per-target
  materialization policy; it MUST NOT write-then-discover collisions. (§27.5)
  Unless the operator confirmed replacing, it MUST NOT replace a local file: the engine is told to leave one that exists, and the application's own write links without replacing ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decision 5).
  Once the operator confirmed it in the backend's native question, it replaces what differs: the engine compares each file as rclone copy does instead of leaving existing files alone, and the application's own write renames its temporary file over the name (decision 12).
  The engine route runs only where the engine can be anchored to the held folder.
- **K-XFER-012** — `no-large-objects` MUST NOT be a user toggle, MUST NOT be an authorization or
  eligibility input for anything, and MUST be refused for any destructive scope; internal
  checksum-path optimisation only, on a scope Aeronautes itself currently classifies fully plain. (§43.4)
- **K-XFER-013** — Token lifecycle MUST be implementable in one-shot rclone: bounded batches / a fresh
  child per batch / sufficient-lifetime, not a non-existent "drain"; on restart rebuild the set from
  unverified objects only. (§26.2)
- **K-XFER-014** — The rclone boundary: rclone MUST NOT be given `sync`, `purge` or `delete` on any scope, and MUST write only where its gateway lets it.
  For an upload and for a copy that is each object's own name under the destination folder, in the container and in its segments container, over what is there once the operator confirmed replacing ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decisions 3 and 4).
  There the engine removes a replaced large object's old segments itself, unless K-SEG-005 has its deletes refused.
  All other destruction is Aeronautes-native, and segments otherwise go only by the operator's explicit force action (K-SEG-003). (§28.6, §36, ADR 0004, ADR 0119, ADR 0122)
- **K-XFER-015** — `--server-side-across-configs` MUST NEVER be enabled for two independently
  authenticated bookmarks. (§30)
- **K-SEG-004** — Automatic segment deletion requires `ExclusiveOwnershipProof` over the exact objects through the destructive effect, excluding other references and relevant concurrent writers.
  Generated-prefix records and `Confirmed` evidence MUST NOT be promoted to exclusive authority: they prove creation and a reference respectively.
  Recovery MUST retain uncertain data and records; explicit force cleanup remains a separate action with sharing risk disclosed. (§28.6, §24.1, ADR 0020)
- **K-SEG-005** — One narrow exception exists, and this entry is the whole of it: an **acknowledged upload or copy** that replaces a large object MAY let the transfer engine remove that predecessor's own segments as it writes the new object, under an operator option that defaults to removing them. ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decisions 3 and 4, which replace ADR 0081 decision 5)
  The engine can delete only where it can write, under the folder the job writes to in the container and in its segments container; old segments kept anywhere else stay.
  Its deletes are refused entirely where the container keeps versions, where the option is off and where the option cannot be read, and the answered confirmation bounds it: a question that stated the segments would be kept MUST NOT be followed by their removal.
  It rests on the operator's accepted exposure and the reference implementation's precedent, NOT on an exclusivity proof, which §65.10 still owes. No other route gains this authority.
  It is an exception to K-SEG-004 and to nothing else.

## Mirror & comparison

- **K-MIRROR-001** — Mirror MUST currently refuse the whole start before transfer or deletion (ADR 0020).
  P14 must establish and test the external-writer boundary before execution is re-enabled.
  Any eventual implementation MUST use an Aeronautes-planned copy (rclone `copy` of the forced selected set) plus Aeronautes-native destination-only deletion; rclone `sync` MUST NOT be used on any scope. (§27.2, §28.4)
- **K-MIRROR-002** — Aeronautes MUST NOT delete a destination object on `Unknown` large-object equality.
  Different manifests do not prove different content. (§28.4)
- **K-MIRROR-003** — Destructive Mirror semantics MUST be a first-class, clearly-labelled mode, never a
  disguised checkbox. (§27.2)
- **K-MIRROR-004** — *Retired* (ADR 0004): raw `rclone sync` is no longer an execution engine, so the
  `PlainOnlyProven` eligibility gate this ID governed no longer exists. The ID is kept, unreused,
  for stability. (§28.4)

## Swift semantics & metadata

- **K-META-001** — Object-metadata mutation MUST preserve every unchanged `X-Object-Meta-*` key
  (assemble the complete remaining set; POST is replace-all). (§17.1)
- **K-META-002** — Metadata edits MUST NOT break a DLO's manifest; the mutation planner owns the
  preserved-header set. (§17.3)
- **K-META-003** — Stale-write protection MUST NOT be presented as atomic compare-and-swap. (§18)

## Segments & destruction

- **K-SEG-001** — Naming heuristics (Suspected) MUST NOT authorize any destructive segment operation. (§24.4)
- **K-SEG-002** — A legitimate container that merely *looks* like a segment store (e.g. `photos_segments`)
  MUST NOT be hidden or destructively classified from its name alone. (§58, §60 Phase 2 gate)
- **K-SEG-003** — Automatic SLO segment deletion requires exclusive authority through deletion for every referenced segment, including nested and ranged segments.
  The current workflow provides no automatic path; manifest plus segments is an explicit force action with the sharing risk disclosed and a typed confirmation.
  It is offered for one object from Backstage (ADR 0095 decision 8), and for the large objects of a prefix or container deletion as that deletion's option (ADR 0119).
  By the operator's decision of 15 November 2026 each starts ticked, except the bulk option in a container that is or may be another container's archive; each is typed by name, and each is refused where the container keeps versions, because a kept version still names its segments (ADR 0120).
  The bulk option deletes only what each manifest names when its turn comes, a static entry compared with the size and entity tag its manifest recorded and a dynamic namespace bounded as ADR 0044 bounds it, and leaves the segments of a large object it cannot bound.
  `?multipart-manifest=delete` deletes shared segments too. (§34, ADR 0020)
- **K-DEL-001** — Empty Container MUST preserve the container and its configuration, and MUST NOT use
  `rclone purge` at a container root (stock rclone deletes the container). (§33)
- **K-DEL-002** — Recursive/large deletes MUST be bounded, streaming, cancellable, and retain exact
  failed object names on partial failure. (§32)
- **K-DEL-003** — Verification MUST be operation-specific and bounded, not always a full re-list. (§32)

## Performance, listing & consistency

- **K-PERF-001** — High-volume namespace jobs MUST stream (never accumulate the whole namespace) and
  operate with bounded memory and bounded concurrency; namespace comparison MUST be a streaming
  merge-join over the name-sorted listings, never an accumulated map. (§15, §15.2, §37)
- **K-PERF-002** — Listing MUST NOT HEAD every row; authoritative HEAD is triggered by selection. (§14.2)
- **K-PERF-003** — Adaptive concurrency MUST back off on 429 / `Retry-After` / rising latency /
  transport failure / memory pressure. (§41)
- **K-CON-001** — Listings are eventually consistent and advisory; object HEAD/GET is authoritative.
  A transfer's outcome MUST NOT be taken from a listing: it is what the engine or the store answered for each object ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decision 7).
  A copy's skip MAY be decided from the two listings, and a pair they cannot compare MUST be read on both sides before it is skipped (decision 4). (§15.3, §28.6)

## UI & platform

- **K-UI-001** — SLO MUST be displayed as **Static Large Object**. (§4.5)
- **K-UI-002** — Swift large-object UI MUST use segment/chunk vocabulary, not S3 multipart/part
  vocabulary, and MUST NOT use `x-amz-*` headers or "S3-compatible". (§4.5)
- **K-UI-003** — Object inspectors MUST NOT present an ACL tab (Swift ACLs are container-level). (§19; the retired mockup-correction specs, now embodied in §4.5)
- **K-UI-004** — The shell MUST NOT display cluster-capacity telemetry a tenant cannot obtain; show
  account usage/quota only. (§4.5)
- **K-UI-005** — The shell MUST NOT display an unsubstantiated health status (e.g. "All systems
  nominal") without an actual probe; show real app/connection/job state. (§4.5)
- **K-PLAT-001** — The Go↔frontend bridge MUST be a set of typed services, not a generic shell/exec
  interface, keeping a Wails v2 fallback cheap. (§55, §6.6)
- **K-PLAT-002** — The Tier-1 Linux baseline MUST be named before the Svelte design system starts.
  **Named (§6.6):** Fedora current stable (first-class) + Ubuntu current LTS native; EL-family via
  Flatpak only; EL8 unsupported; static-binary distribution rejected. The Phase-0 webview spike
  confirms it. (§6.6)
- **K-PLAT-003** — Aeronautes MUST build and run on all six targets: {Linux, Windows, macOS} × {x86_64,
  aarch64}. (§61.0)
- **K-PLAT-004** — The bundled rclone binary MUST match the build's OS and architecture and be
  checksum-verified per target. (§61.1, §26.3)

## Testing

- **K-TEST-001** — The object-name encoding suite MUST *pass* end-to-end through both the native client and the rclone forced-copy path, with a native byte-preserving fallback for names rclone cannot preserve; names MUST NEVER be silently skipped or normalized. (§14.3)
  Mirror currently requires zero-effect refusal tests; its planned execution paths must pass the same encoding suite before P14 re-enables them.

## Phase gates → requirement IDs

Requirements are asserted at the **first phase that implements the behaviour** and remain as regression
checks thereafter; a few are static design/architecture checks (marked *static*) asserted from the phase
shown without a running feature. This replaces the earlier all-in-Phase-0 matrix, which listed behaviour
Phase 0 does not implement.

| First real gate | Requirements |
|---|---|
| Phase 0 (*static* design/architecture) | K-PLAT-001, K-PLAT-002, K-UI-001, K-UI-002; K-SEC-002, K-SEC-003 (bridge shape / schema has no secret columns); a **spike** toward K-PLAT-003 |
| Phase 1 — auth + browse | K-PERF-001, K-PERF-002, K-SEC-004, K-SEC-005 |
| Phase 2 — inspector | K-UI-003, K-UI-004, K-UI-005, K-SEG-001, K-SEG-002 |
| Phase 3 — mutation | K-META-001..003, K-SEC-006 (audit redaction) |
| Phase 4 — large-object read | K-SEG-001, K-SEG-002 (read-only inspect/classify) |
| Phase 5 — transfers + Copy | K-SEC-001, K-SEC-007, K-SEC-008, K-XFER-001..015, K-CON-001, K-TEST-001 (copy paths), K-PLAT-004 |
| Phase 6 — destructive + performance | K-MIRROR-001..003 (004 retired), K-DEL-001..003, K-SEG-003, K-SEG-004, K-PERF-003, K-TEST-001 (Mirror refusal now; planned execution paths gated before P14 re-enablement) |
| Phase 7 — hardening | K-PLAT-003 (full six-target build/run/sign), a11y / virtualisation per §60 |

The prose gates in `DESIGN.md` §60 should reference the IDs in this table rather than duplicate them.
