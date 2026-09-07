# ADR 0007 — The enumerated rclone flag surface

**Status:** Accepted (2026-08-29) — resolved from seam-spike evidence against the pinned rclone
**Detail:** `docs/rclone.md` (the verified table); `DESIGN.md` §44, §44.1, §26.2; requirements
K-XFER-006, K-SEC-007, K-SEC-008

## Decision

The exact surface lives in **`docs/rclone.md`**: named-remote env config with tokens
environment-only, the job-scoped empty config, `--swift-chunk-size` as §25's single knob, the
selected-set contract as `--files-from-raw` + `--ignore-times`, `--use-json-log -vv` for
progress and generated-segment-prefix capture, bounded retries, and the four concurrency/pacing
flags. TLS `override.*` is config-file-only and non-isolating, so it is allowed only when both
remotes share identical trust; otherwise two single-remote children (§12.3).

Notable corrections the spike forced: `--files-from0` does not exist (newline-bearing names can
never ride rclone lists → native route); `override.*` env forms are silently ignored; rclone
exits 0 while silently skipping unsafe names, making listing-diff verification (§28.4) the only
trustworthy completeness signal.

## Consequences

- No free-form argument box exists; a needed behaviour without an entry in `docs/rclone.md` is
  a deliberate gap to resolve (narrow adapter, upstream contribution, or compatibility
  decision), recorded there.
- The table is re-verified by re-running the seam suite on any pin upgrade; entries marked
  pin-specific (dot-segment safety) are the first suspects.

## Selected-set amendment — 7 September 2026

[ADR 0022](0022-complete-rclone-selection.md) adds the source traversal decision to the selected-set contract: `--files-from-raw` plus `--no-traverse` and `--ignore-times`.
The controlled short-page experiment motivates this addition without changing the pin, raw-name policy, staging or independent verification.
Its separate design review precedes implementation and maintained shared-caller validation.
