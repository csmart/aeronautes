# ADR 0011 — Typed Wails service surface

**Status:** Accepted (2026-08-29)
**Detail:** `DESIGN.md` §55; requirement K-SEC-002

## Context

The Svelte/webview layer is unprivileged presentation code (§55). Wails v3 binds Go structs and
generates typed TS clients (`wails3 generate bindings`, committed under `frontend/bindings/`);
whatever is bound is drivable by any code in the webview, so the bridge must stay a small set of
purpose-specific services, not a god-object.

## Decision

The frontend reaches Go **only** through these services (all in `internal/app`, bound in
`main.go`; skeletons grow with their phases):

- **AppService** — shell identity/environment (version, linked Wails); no filesystem, no exec.
- **BookmarkService** — bookmark CRUD over `internal/bookmarks`. Credentials cross the bridge
  **one way** (frontend → Go, at save time), are handed straight to the SecretStore, and are
  never readable back; bookmark DTOs carry `HasCredential`, never the secret.
- **BrowseService** — account portrait, container/object listing pages (typed entries, §14).
- **InspectorService** — selection-triggered HEAD detail (redacted views only).
- **TransferService**, **JobService** — Phase 3+; planning, execution, progress events.

Rules for every service: no secret, token, or keychain handle in any return type (K-SEC-002/003);
inputs validated in Go as if hostile; errors cross as normalized `SwiftError`-derived shapes;
long results stream via events/pagination, never unbounded arrays. New services or methods that
widen this surface need an ADR update in the same commit.

## Consequences

- The bindings bridge stays reviewable in one sitting, and the §55 audit ("what can the webview
  drive?") is the union of these files.
- The decoupled surface keeps a Wails v2 fallback cheap (ADR 0010) and gives the §57 tests a
  seam: services are plain Go, tested without the webview.
