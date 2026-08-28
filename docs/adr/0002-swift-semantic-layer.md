# ADR 0002 — An Aeronautes-owned Swift semantic layer over `ncw/swift/v2`

**Status:** Accepted (2026-08-28)
**Detail:** `DESIGN.md` §6.4–§6.5, §9, §13–§24

## Context

Swift's operational semantics (metadata replace-all POST, DLO/SLO manifests, segment sharing,
ACL dialects, capability discovery) are the product. They must live somewhere testable and
UI-independent, and neither `ncw/swift/v2` nor rclone models them.

## Decision

`internal/swiftcontrol` owns all Swift semantics: typed listings, header categorisation, resource
snapshots, mutation planning, DLO/SLO interpretation, segment evidence, destructive-operation
planning, normalized errors with transaction IDs. `ncw/swift/v2` is wrapped as the wire
foundation and never called by the frontend. The normative behaviour hierarchy is §6.4: Swift
protocol → proven target cluster → `python-swiftclient` as reference → `ncw/swift/v2` → this layer.

## Consequences

- Semantics are unit/contract-testable without a GUI (§57.1–57.2) — the test suite's centre.
- Wire-level behaviour (encoding, auth) is shared with rclone's Swift backend, but the layers
  above the wire are implemented twice (here and inside rclone) — §9's asymmetry note governs.
- Do not fork `ncw/swift/v2` pre-emptively; narrow adapters and upstream contributions first.
