# Archived audit probe status on current master

## Scope and oracle

The 2026-09-05 audit archived its reproductions as text under `audits/2026-09-05/probes/` with a manifest that maps each one to the source path it replaces.
This record answers one narrow question for two of them: does the archived reproduction still reproduce on current master?
The oracle is the archived probe itself, applied to unchanged production through a Go overlay so that no source file is edited.

It answers nothing about whether either finding's acceptance criteria are met.
A probe that no longer reproduces records that one named scenario is covered, not that the requirement behind it is satisfied.

## Method

Both probes were prepared from the archive exactly as its own preparation script does, substituting the pinned engine path into the placeholder, and applied with a Go overlay against a detached worktree at master.
The preparation script also requires the locked frontend dependencies for its user-interface probe, so the two Go probes were prepared directly rather than through it; nothing else about their content changed.

The application probe needed one adaptation to compile: the cached session type now holds its session in a field rather than embedding it, so two call sites gained that field.
That is an adaptation to a later refactor and not a defect, and no assertion in the probe changed.

## Result

Both probes pass on master `08b0d125ace4944c702720f4c08336253127a36c`.

| Probe | Package | Result |
| --- | --- | --- |
| `TestAuditCancelledDownloadStopsBeforePlacement` | `internal/transfer/local` | pass in 0.005 s |
| `TestAuditCopyCountsOneFailedIdentityOnce` | `internal/app` | pass in 0.26 s |

Maintained tests already cover those two boundaries on this platform.
Cancellation is covered by `TestDownloadNativeCancellationBetweenItems`, `TestDownloadCancelledBodyClosure` and `TestDownloadEngineCancellationBoundaries` in the local transfer package.
Item identity accounting is covered by the copy and upload item-record tests and by `TestInterruptedItemRecordsReopen` in the system-test package, on both the mock and the owned service.

## What this does not establish

DS-07 still requires cancellation at native writes and placements, exact partial outcomes and child quiescence, proved by native parent-kill runs on all six operating system and architecture targets.
This run was Linux only and proves none of that.

DS-08's bulk-delete accounting is unchanged and still derives identities from an aggregate count.
Both deletion paths compute an accounted total from deleted, not-found and error counts, and then use that total as an index into the requested batch, so an out-of-order or duplicated reply can fail one name twice and lose another, even though the reported errors carry the paths.
The finding's own remedy forbids deriving an identity from an aggregate response count, so that half remains exactly as the audit describes it.

The remaining archived probes were not run here, and nothing in this record changes the valuable-data release hold.
