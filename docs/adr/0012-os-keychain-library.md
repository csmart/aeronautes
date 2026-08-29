# ADR 0012 — OS keychain via zalando/go-keyring

**Status:** Accepted (2026-08-29)
**Detail:** `DESIGN.md` §12.1; requirement K-SEC-001; ADR 0006 (vault fallback, still Proposed)

## Context

§12.1 makes the OS keychain the primary credential store on every platform, with visible — never
silent — degradation when it is unavailable. The three platform APIs (Linux DBus Secret Service,
Windows Credential Manager, macOS Keychain) need a wrapper; hand-rolling three native bindings is
not v1 work.

## Decision

- **`github.com/zalando/go-keyring`** (pinned via go.mod): thin, no CGO, per-OS native — Secret
  Service over godbus (already in the module graph), `wincred`, and macOS `/usr/bin/security`.
- Values are **base64-wrapped** by our `KeychainStore` so arbitrary bytes survive the library's
  string-shaped API; entries live under the single service name `aeronautes`.
- **Probe at startup** (write/read/delete round-trip): only a keychain that demonstrably works is
  used. Otherwise `OpenDefault` falls back to the session-only store and the backend name flows
  to the UI (`AppService.Info.secretBackend`), which warns that credentials die with the process.
- Fallback chain today: keychain → session-only. The opt-in encrypted vault slots between them
  once ADR 0006 is resolved.

## Consequences

- Bookmark credentials survive restarts on a normal desktop; keychain-less environments keep
  working with an honest warning instead of a broken or lying state.
- macOS goes through the `security` CLI (a subprocess per operation) — acceptable at bookmark
  cardinality; revisit only if secret traffic ever grows beyond that.
- Tests skip (never fake) without a reachable keychain; a real Secret Service exercises the
  round-trip, including non-UTF-8 bytes, where available.
