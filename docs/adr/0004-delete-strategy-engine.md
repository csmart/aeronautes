# ADR 0004 — All destruction is native: the absolute rclone boundary

**Status:** Accepted (2026-08-28) — supersedes the draft design's rclone destruction carve-outs
**Detail:** `DESIGN.md` §28.4, §28.6, §31–§36, §48; requirements K-XFER-008, K-XFER-014,
K-MIRROR-001, K-MIRROR-004 (retired), K-DEL-001

## Context

rclone's Swift backend bulk-deletes a large object's referenced segments on `Update` (overwrite)
and `Remove` (delete), and Swift permits segment sharing between manifests — so a delegated
overwrite/delete can corrupt an unrelated logical object. Earlier drafts banned this in general
but kept three carve-outs on scan-proven plain-only scopes (`PlainOnlyProven`): raw `rclone sync`
for a Mirror's copy half, `rclone delete` for Empty Container, and `rclone purge` for Delete
Container. Review found the carve-outs contradict the design's own TOCTOU argument — Swift has no
snapshot or lock, so no point-in-time scan makes a later rclone `Update`/`Remove` safe — and the
qualifying scan (HEAD every object in scope) was itself prohibitively expensive at exactly the
scale where the carve-outs would have mattered. The design's own example numbers showed native
bulk-delete outperforming purge regardless.

## Decision

**rclone may write only planned-new or uniquely-staged names. Aeronautes performs every
overwrite, manifest commit, object deletion, bulk deletion, segment reclamation, and container
deletion natively.** Raw `rclone sync`/`purge`/`delete` are not execution engines, on any scope.
Overwrites of changed objects — plain and large — go through stage-and-commit (§28.6): rclone
stages to a unique name; Aeronautes verifies and commits natively (DLO-manifest PUT for large
objects, single-remote server-side COPY for plain ones). Native deletion is manifest-only by
construction and never follows segments except under `ExclusiveOwnershipProof` (ADR 0005). The
`PlainOnlyProven` eligibility concept is removed; K-MIRROR-004 is retired.

## Consequences

- The corruption-shaped race space is gone: the residual races cost orphans (recoverable), never
  shared-segment deletion. "rclone moves bytes; Aeronautes destroys" is literally true.
- Changed-object overwrites cost extra cheap API calls (verify + native commit + staging delete);
  new-object copies (the bulk) are unaffected.
- Mirror's delete half depends on the native delete engine, so executable Mirror ships in
  Phase 6 with it; Phase 5 ships Copy and a read-only Mirror preview (§60).
