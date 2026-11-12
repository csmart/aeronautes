# ADR 0005 — Large-object safety: manifest-only defaults and ExclusiveOwnershipProof

**Status:** Accepted (2026-08-28); its sentence on rclone-executed uploads is amended by [ADR 0111](0111-segmented-uploads-as-static-large-objects.md), which has a segmented upload published as a static large object.
**Detail:** `DESIGN.md` §23–§25, §28.4, §28.6, §34–§35; requirements K-SEG-001..004, K-XFER-007,
K-META-002

## Context

DLO and SLO segments may be shared between manifests; heuristics (`*_segments` names) identify
*candidate* segment stores, and `Confirmed` evidence proves a segment is *referenced* — neither
proves it is *unshared*. Deleting or overwriting through anything that follows segments can
therefore destroy unrelated data.

## Decision

- DLO and SLO are distinct typed domain objects; SLO manifests round-trip exactly (ordered
  entries, ranged and inline-data segments, unknown fields preserved) (§23).
- Large-object equality is a typed `ComparisonResult` with evidence; `Unknown` never authorizes
  deletion; different manifests do not prove different content (§28.4).
- Deletion defaults are **manifest-only**. Segment deletion requires `ExclusiveOwnershipProof` —
  a unique, Aeronautes-recorded segment prefix for *this* job — which `Confirmed` evidence never
  satisfies for externally-created objects; external SLO/DLO segment purges are deliberate
  Backstage flows (§28.6b, §34, §35).
- Naming heuristics never authorize destruction (K-SEG-001/002).
- The engine uploads segments and stages a DLO; for a segmented upload Aeronautes composes the
  static manifest at the commit, and a native static composer is in scope for that and nothing
  else (ADR 0111, K-XFER-007); overwrites commit via stage-and-commit (ADR 0004).

## Consequences

- The safe default leaks recoverable orphans rather than risking irreversible shared-segment
  corruption; reconciliation and Backstage flows surface them.
- Verification must be consistency-aware (§15.3): a fresh DLO commit is verified for
  segment-listing completeness before success.
- The §58 fixture namespace (shared prefixes, nested/ranged SLOs, a legitimate `photos_segments`
  container, the plain→DLO race) is the acceptance bed for every rule above.
