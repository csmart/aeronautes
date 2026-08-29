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
- **MutateService** — the ONLY mutating surface (Phase 3). Every method takes a complete
  intended outcome (a working copy, never a raw header grab-bag), runs preflight → execute →
  verify → audit, returns typed outcomes (applied / conflict / verify-mismatch), and gates on
  connect-time capabilities only where the cluster is KNOWN to reject the request — Unknown never
  gates (§10). Methods: object metadata update, container update, container create, marker
  create/delete.
- **AuditService** — read-only §50 trail (recent events).
- **TransferService** — the transfer surface (Phase 5): `PlanCopy` (read-only §15.2/§28.4
  preview with versioned-destination and DLO-commit disclosures), `StartCopy` (creates the §46
  job, runs plan→execute→verify→reconcile in a background goroutine; start and terminal states
  audited), `CancelJob` (graceful §26.4 cancellation; audited). Engine construction is
  internal — remotes, storage URLs, and tokens never appear in any DTO.
- **JobService** — read-only §46/§47 drawer surface: `List` (bookmark filter matching either
  end, active-only toggle, bounded) and `Get`; DTOs carry counters and display locators only.

Rules for every service: no secret, token, or keychain handle in any return type (K-SEC-002/003);
inputs validated in Go as if hostile; errors cross as normalized `SwiftError`-derived shapes;
long results stream via events/pagination, never unbounded arrays. New services or methods that
widen this surface need an ADR update in the same commit.

## Consequences

- The bindings bridge stays reviewable in one sitting, and the §55 audit ("what can the webview
  drive?") is the union of these files.
- The decoupled surface keeps a Wails v2 fallback cheap (ADR 0010) and gives the §57 tests a
  seam: services are plain Go, tested without the webview.
