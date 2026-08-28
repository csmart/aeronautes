# ADR 0010 — Wails v3 (beta) on a named modern-Linux baseline, with v2 as fallback

**Status:** Accepted (2026-08-28), confirmation gated on the Phase-0 webview spike
**Detail:** `DESIGN.md` §6.6, §61.0; requirements K-PLAT-002, K-PLAT-003

## Context

Wails renders in the system webview; on Linux that decides the "beautiful" pillar. v3 (beta as of
2026-08; API stable, no fixed 3.0 date) uses GTK4/WebKitGTK 6 — a far better Linux base than
v2's GTK3/WebKitGTK 4.1 — but EL9 has no WebKitGTK 6 and Red Hat has deprecated WebKitGTK, so a
"runs on every distro" native binary is not achievable with any Wails version, and a static
run-anywhere binary is impossible with a system-webview shell.

## Decision

- **Wails v3**, pinned to an exact beta version, upgraded deliberately.
- **Tier-1 Linux baseline (K-PLAT-002): modern distros only** — Fedora current stable
  (first-class; primary development platform) and Ubuntu current LTS native (at design lock:
  Fedora 44, Ubuntu 26.04 LTS; 24.04 LTS as a compatibility target). EL-family via **Flatpak**
  only (the runtime bundles WebKitGTK 6.0), gated on keychain/sidecar/portal proof (§61.1). EL8
  unsupported. Static-binary distribution rejected.
- The Phase-0 spike proves the design system renders and packages acceptably on v3 on this
  baseline (§60) and verifies release hardening (§55: DevTools off, no reachable bindings
  listener). The spike failing is the trigger for the **v2 fallback**, kept cheap by the typed
  service bridge (§55, K-PLAT-001).

## Consequences

- GTK4/WebKitGTK 6 rendering quality on the platforms that matter; a beta-API churn risk we
  absorb by pinning.
- Flatpak investigation moves earlier than "someday": it is the EL answer and the general
  webview-variance escape hatch.
- All six OS×arch targets still verify webview availability in the spike (WebView2 on Windows
  ARM64, WKWebView on Apple Silicon, WebKitGTK on Linux arm64) (K-PLAT-003).
