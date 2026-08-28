# ADR 0008 — Cross-bookmark copy is client-side, and the planner says so

**Status:** Accepted (2026-08-28)
**Detail:** `DESIGN.md` §28.5, §30; requirements K-XFER-005, K-XFER-015

## Context

A desktop-spawned rclone copy between two different Swift bookmarks streams every byte through
the workstation; rclone performs server-side COPY only within one remote configuration. A "Prod →
DR" copy is therefore bounded by the operator's link, and pretending otherwise misleads.

## Decision

- v1 cross-bookmark transfer is **client-side** and the planner's Impact rail labels it as such,
  with duration/egress estimates from the measured link — never cluster throughput (K-XFER-005).
- Server-side COPY across bookmarks is a future fast path, enabled only from a typed §30
  capability proof; `--server-side-across-configs` is never enabled for two independently
  authenticated bookmarks (K-XFER-015). Same-remote server-side COPY (e.g. the §28.6 plain-object
  commit) is ordinary and allowed.
- Cluster-side rclone (jump host/sidecar) is the stated future answer for real bulk replication;
  out of v1 scope.

## Consequences

- Honest, sometimes unwelcome numbers in the planner; no accidental multi-TB laptop hairpins.
- The §26.2 token-lifetime envelope interacts with client-side throughput and is preflighted.
