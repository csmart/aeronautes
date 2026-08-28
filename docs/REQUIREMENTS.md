# Aeronautes — normative requirements

A compact, mechanically-checkable list of the **hard** constraints from `DESIGN.md`.
`DESIGN.md` remains the authoritative rationale; this file exists so coding agents and phase
gates can reference stable IDs instead of re-reading ~11k words of prose.

- **MUST / MUST NOT** are binding. Changing one requires updating `DESIGN.md` (and an ADR where noted).
- Each requirement cites the governing `DESIGN.md` section.
- Phase gates (`DESIGN.md` §60) should list the requirement IDs they verify.
- IDs are prefixed `K-` and are **stable**. The prefix is historical (the former working name); do not rename them to match Aeronautes.

## Security & secrets

- **K-SEC-001** — rclone MUST NOT receive the durable Swift auth key; it gets a pre-authenticated
  `storage_url` + short-lived `token` only. (§26.2)
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
- **K-XFER-002** — After a hard interruption, Aeronautes MUST NOT auto-delete destination segment objects
  without Confirmed ownership of the actual generated prefix; otherwise classify as suspected orphan
  and surface to the operator. (§26.4)
- **K-XFER-003** — Graceful cancellation MUST let rclone clean its own segments; Aeronautes MUST NOT set
  `--swift-leave-parts-on-error`. (§26.4)
- **K-XFER-004** — The large-object chunk size is a single control that MUST NOT exceed the cluster's
  advertised `max_file_size` (default 5 GiB, safe against Swift's default `max_file_size` of
  `5368709122`); MUST NOT default to tiny values (e.g. 64 MiB). (§25)
- **K-XFER-005** — The transfer planner MUST show the data path (client-side vs server-side); a
  cross-remote copy is client-side (hairpins through the workstation) and MUST be labelled as such. (§28.5, ADR 0008)
- **K-XFER-006** — Aeronautes MUST NOT expose a free-form arbitrary-rclone-argument textbox in normal or
  expert UI. (§44, §44.1)
- **K-XFER-007** — rclone-executed large-object uploads are DLOs (`X-Object-Manifest`), not SLOs; a
  native SLO composer is out of v1. (§25, §62)
- **K-XFER-008** — rclone `Update` (overwrite) and `Remove` (delete) bulk-delete a large object's old
  referenced segments, so rclone MUST NOT overwrite or delete **any** existing destination object.
  Every overwrite goes through the Aeronautes-owned stage-and-commit path (staged unique name +
  native commit); every deletion is Aeronautes-native. (§28.6)
- **K-XFER-009** — An Aeronautes-selected transfer set MUST be forced onto the wire (e.g. `--ignore-times`)
  and verified before any Mirror deletion; `--files-from` only filters the source and MUST NOT be
  relied on to override rclone's own equality test. `--ignore-existing` MUST NOT be used for a Copy
  selected set. (§28.4)
- **K-SEC-007** — rclone tokens MUST be passed by environment named-remote config only, never in argv;
  the child MUST run with a job-scoped empty config, never inheriting the user's `rclone.conf`. (§26.2)
- **K-SEC-008** — Per-bookmark TLS policy MUST be applied per-remote (`override.no_check_certificate` /
  `override.ca_cert` on `aeronautes_src`/`aeronautes_dst`), never as a process-global flag when two
  remotes are present. If the pinned rclone does not honour the per-remote override, split the job
  into two single-remote children or reject the pairing — never broaden trust process-wide. (§12.3, §44.1)
- **K-XFER-010** — Cross-bookmark Copy MUST preserve object metadata explicitly (rclone's Swift path
  drops it): HEAD source, apply the §17.1 writable-header set; the destination keeps its own
  `X-Object-Manifest`; if data-only, the planner MUST preview every dropped category. (§27.4)
- **K-XFER-011** — Swift→local download MUST run a complete collision preflight and apply a per-target
  materialization policy; it MUST NOT write-then-discover collisions. (§27.5)
- **K-XFER-012** — `no-large-objects` MUST NOT be a user toggle, MUST NOT be an authorization or
  eligibility input for anything, and MUST be refused for any destructive scope; internal
  checksum-path optimisation only, on a scope Aeronautes itself currently classifies fully plain. (§43.4)
- **K-XFER-013** — Token lifecycle MUST be implementable in one-shot rclone: bounded batches / a fresh
  child per batch / sufficient-lifetime, not a non-existent "drain"; on restart rebuild the set from
  unverified objects only. (§26.2)
- **K-XFER-014** — The rclone boundary is absolute: rclone MUST NOT execute any destructive or
  overwriting operation (`sync` delete phase, `purge`, `delete`, or any write to an existing live
  name) on **any** scope. rclone writes only planned-new or uniquely-staged names; all destruction
  is Aeronautes-native and manifest-only, with HEAD-before-mutate where identity matters (§28.6a). (§28.6, §36, ADR 0004)
- **K-XFER-015** — `--server-side-across-configs` MUST NEVER be enabled for two independently
  authenticated bookmarks. (§30)
- **K-SEG-004** — Segment deletion requires `ExclusiveOwnershipProof` (a unique Aeronautes-recorded prefix);
  `Confirmed` evidence MUST NOT be treated as exclusive. (§28.6, §24.1)

## Mirror & comparison

- **K-MIRROR-001** — Mirror MUST be executed as an Aeronautes-planned copy (rclone `copy` of the
  forced selected set) plus Aeronautes-native destination-only deletion; rclone `sync` MUST NOT be
  used, on any scope. (§27.2, §28.4)
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
- **K-SEG-003** — "SLO manifest + segments" delete MUST auto-delete segments only when every referenced
  segment (nested and ranged included) is proven exclusively owned; external SLOs require a Backstage
  flow. `?multipart-manifest=delete` deletes shared segments too. (§34)
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
  Listing-based verification MUST use bounded retries, and stage-and-commit verification MUST
  confirm segment-listing completeness before declaring success. (§15.3, §28.6)

## UI & platform

- **K-UI-001** — SLO MUST be displayed as **Static Large Object**. (§4.5)
- **K-UI-002** — Swift large-object UI MUST use segment/chunk vocabulary, not S3 multipart/part
  vocabulary, and MUST NOT use `x-amz-*` headers or "S3-compatible". (§4.5)
- **K-UI-003** — Object inspectors MUST NOT present an ACL tab (Swift ACLs are container-level). (§19, assets/REGENERATION.md)
- **K-UI-004** — The shell MUST NOT display cluster-capacity telemetry a tenant cannot obtain; show
  account usage/quota only. (assets/REGENERATION.md)
- **K-UI-005** — The shell MUST NOT display an unsubstantiated health status (e.g. "All systems
  nominal") without an actual probe; show real app/connection/job state. (assets/REGENERATION.md)
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

- **K-TEST-001** — The object-name encoding suite MUST *pass* end-to-end through both the native client
  and the rclone transfer path (forced-copy and Mirror-planned paths included), with a native
  byte-preserving fallback for names rclone cannot preserve; names MUST NEVER be silently skipped or
  normalized. (§14.3)

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
| Phase 6 — destructive + performance | K-MIRROR-001..003 (004 retired), K-DEL-001..003, K-SEG-003, K-SEG-004, K-PERF-003, K-TEST-001 (Mirror-planned path re-verified) |
| Phase 7 — hardening | K-PLAT-003 (full six-target build/run/sign), a11y / virtualisation per §60 |

The prose gates in `DESIGN.md` §60 should reference the IDs in this table rather than duplicate them.
