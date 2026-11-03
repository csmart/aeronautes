# Register clause resolutions

The [register reconciliation](2026-10-18/register-reconciliation.md) left three clauses in the SEC family "not established", and the [road to 1.0.0](2026-10-27/road-to-1.0.0.md) places their resolution in 0.4.0.
This record resolves them.
The reconciliation remains the careful judgement snapshot it was; this record is the forward disposition of its three open clauses.
None of the three names a known way for the build to lose data in ordinary operation; each is a boundary or a wording question, not a defect.

## 1. Shutdown ownership of credential publication and cleanup (SEC-02)

Resolved by [ADR 0097](../adr/0097-shutdown-ownership-of-credential-operations.md).
`ShutdownWork` can interrupt an unfinished `BookmarkService.Save`/`Delete`, but the valuable bookmark row changes only in a single all-or-nothing publication under `synchronous=FULL`, so an interrupted operation leaves an intact committed state — the prior one or the fully committed new one, with the missing acknowledgement not saying which — and, at most, a durable journal record and a re-enterable keychain item. [ADR 0055](../adr/0055-bounded-credential-operations.md) bounds the caller's wait, not the native call; a failed `Put` leaves a `prepared` reference that the retired-only cleanup pass does not reconcile, a known limitation rather than valuable-data loss.

## 2. Source-version binding — the wider wording (SEC-01)

Delivered within a recorded exception on the routes that exist.
Download refuses an item whose plan captured no source identity, and compares a changed SLO tag only where one was captured; [ADR 0060](../adr/0060-native-download-content-binding.md) states the one exception plainly — "an SLO listed with bytes and no `SLOETag` has only size as captured identity; a same-size substitution of that object is not claimed from the listing" — and the code matches that exception rather than exceeding it.
Upload's preflight captures a path, a size and a route rather than a source identity ([ADR 0080](../adr/0080-upload-content-binding.md)).
The clause's wording may have reached wider than these routes; where it did, the gap is a *finer* source identity (pinning a size-only SLO), which is documented, is not a known substitution-loss in ordinary operation, and belongs with the version and hidden-version work scheduled for 0.5.0 — not the valuable-data hold.

## 3. The synchronous bridge and native-request half (DS-07/SEC-06)

This clause is lifecycle ownership, not input shape: it asks when admitted background work and its final records finish at shutdown.
[ADR 0011](../adr/0011-typed-service-surface.md)'s typed surface removes the free-form native request but does not answer that — its own transfer jobs run in background goroutines.
The answer is the job lifecycle: `ShutdownWork` (`internal/app/shutdown.go`) joins the job runner, the Find service and the synchronous metadata writer (`MutateService`) before the shell closes the database and releases the lock, which is [ADR 0053](../adr/0053-synchronous-writer-shutdown-ownership.md)'s accepted ownership for the synchronous writes v1 keeps, with its live-window boundary accepted and the native-platform limits retained.
The credential half of the original concern is clause 1 (ADR 0097); this half is the register's DS-07/SEC-06 cancellation-and-shutdown row.

## For the valuable-data hold

With clause 1 recorded (ADR 0097), clause 2 bounded by ADR 0060 and scoped to 0.5.0, and clause 3 mapped to the job lifecycle and ADR 0053, the three "not established" clauses are resolved as owner mappings and boundaries rather than data-loss gaps.
With the [durability-limits record](../experiments/2026-11-02-durability-limits.md), this narrows the durability residual — the power-loss contract and the recovery components are evidenced — but does not discharge it: the end-to-end desktop-start-after-crash measurement remains.
So the durability-residual holder stays, alongside the artefact and deployment holder (remediation P31 to P35); the hold stays in force and is lifted at 1.0.0.
