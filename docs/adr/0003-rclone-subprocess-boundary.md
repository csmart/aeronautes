# ADR 0003 — rclone as a pinned, isolated, token-only subprocess

**Status:** Accepted (2026-08-28)
**Detail:** `DESIGN.md` §26, §44–§45; requirements K-SEC-001, K-SEC-007, K-SEC-008, K-XFER-006

## Context

rclone provides mature, fast bulk movement, but it must not hold durable credentials, inherit the
operator's configuration, or expand into a semantics engine.

## Decision

- One pinned, checksum-verified rclone binary per target (§26.3, §61.1); one subprocess per
  logical transfer job; rclone's Go packages are not embedded in v1.
- Credentials: pre-authenticated `storage_url` + short-lived token only, via configless
  named-remote **environment** config (`RCLONE_CONFIG_AERONAUTES_SRC_*` / `_DST_*`) — never the
  durable key, never argv, never a persistent `rclone.conf`; every spawn uses a job-scoped empty
  config and a whitelisted environment (§26.2).
- Token expiry is planned for with bounded batches / fresh children (ADR 0009).
- The flag surface is enumerated and curated (§44.1, ADR 0007); no free-form argument box.
- Per-remote TLS uses rclone's remote-scoped `override.*`; if unhonoured, two single-remote
  children or rejection — never process-global trust changes (§12.3, K-SEC-008).

What rclone is *allowed to execute* is ADR 0004.

## Consequences

- The durable key never enters a child process; a read child environment exposes only a
  short-lived token.
- Long jobs must be batch-planned around token lifetime; single objects too large for the
  lifetime envelope fail at preflight (§26.2).
- A provider quirk may need solving twice (native layer and rclone flags) — §9.
