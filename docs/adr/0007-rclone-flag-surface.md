# ADR 0007 — The enumerated rclone flag surface

**Status:** Proposed — must be resolved before Phase 5 freezes the transfer abstraction
**Detail:** `DESIGN.md` §44, §44.1, §26.2; requirements K-XFER-006, K-SEC-007, K-SEC-008

## Question to resolve

Produce `docs/rclone.md`: the exact list of rclone flags/config keys Aeronautes sets, each mapped
to the product setting that drives it, verified against the pinned rclone by the Phase-0 seam
spike (§60). No free-form argument box exists, so every needed behaviour must be reachable
through this enumerated surface — a behaviour with no supported flag is a gap to resolve
deliberately (narrow adapter, upstream contribution, or an explicit compatibility decision).

## Minimum coverage

Named-remote env config (`RCLONE_CONFIG_AERONAUTES_SRC_*`/`_DST_*`; never
`--swift-storage-url`/`--swift-auth-token` — argv leaks); `--swift-chunk-size`; segment placement
(`use_segments_container` behaviour); storage policy; per-remote `override.no_check_certificate` /
`override.ca_cert`; `--transfers`, `--checkers`, `--tpslimit`, `--bwlimit`; the forced-set
contract (`--files-from0`, `--ignore-times`); structured/JSON output for progress and generated
segment prefixes; the per-bookmark typed compatibility allowlist (§44.1).

## Decision

_Unresolved. Record the verified flag table (or its `docs/rclone.md` location) and the spike
observations that validated each entry, then flip Status to Accepted._
