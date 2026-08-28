# ADR 0001 — Go application core with a Wails v3 + Svelte 5 shell

**Status:** Accepted (2026-08-28)
**Detail:** `DESIGN.md` §6 (stack, rationale, rejected alternatives), §55 (bridge boundary)

## Context

Aeronautes is a network/IO-bound desktop operator tool that needs mature Swift wire code, a
first-class custom UI, and native packaging on {Linux, Windows, macOS} × {x86_64, aarch64}.

## Decision

- **Go** for the application core: goroutines/context fit high-concurrency Swift operations, and
  `ncw/swift/v2` plus rclone mean the ecosystem's Swift wire code is already Go.
- **Wails** (system webview) for the desktop shell, **Svelte 5 + TypeScript** for the UI.
- Python is a behavioural reference and fixture generator only, never the runtime (§6.3);
  `python-swiftclient` is not ported (§6.4); `ncw/swift/v2` is wrapped, not forked (§6.5).

The Wails **v3-vs-v2** choice and the webview risk are ADR 0010.

## Consequences

- One language across core, wire library, and transfer engine; native packaging per target.
- The UI renders in the host system webview — design must hold on WebKitGTK (ADR 0010).
- The Go↔frontend bridge must stay a small set of typed services (§55, K-PLAT-001).
