# ADR 0064: audit account attribution

**Status:** Proposed.
This record does not implement the correction or close L11, P28 or the valuable-data hold.

## Context

DESIGN section 47 requires the audit view to carry an account/bookmark filter, and section 50 requires each event to record its bookmark.
[ADR 0059](0059-account-scoped-remaining-reads.md) binds the separate recent-events read but explicitly leaves `AuditService.Search` open.
Search already supports an optional bookmark filter in the local store; an empty filter deliberately means all accounts.
The [audit query contract](../experiments/2026-09-12-audit-query-state.md) independently owns Search lifetime, paging, retry and error state.

On source `e4b7730721adc2beea38c0e22257325a352e9132`, the store retains the bookmark but the shared `auditEventDTO` mapper omits it.
`RecentFor` restores the field separately, while Search loses it for both named and all-account queries.
The drawer does not show row attribution even when its input carries the bookmark, and always sends an empty bookmark filter.
The [audit attribution record](../experiments/2026-09-21-audit-account-attribution.md) retains the service and mounted before failures with passing store and all-account controls.

## Decision

1. Search projects each event's retained `BookmarkID` without replacement or inference.
   A named query keeps the existing exact store filter; an empty query remains the explicitly labelled All accounts mode.
   No value is inferred from the live connection, resource name, current bookmark label or another event.
   Local historical reads remain available while disconnected, while another account is active, and when the recorded bookmark no longer exists.

2. The audit tab gains its own bookmark selector alongside operation and result.
   It defaults to All accounts and is independent of the jobs filter and live connection.
   The selector offers current bookmarks and nonempty bookmark IDs in the displayed audit rows; the selected ID remains available if its current bookmark or old rows disappear.
   This permits filtering retained history for an unavailable bookmark without fetching an unbounded catalogue of historical identities.
   Choosing a bookmark supplies that literal ID to first-page, continuation and retry calls.

3. Bookmark, literal operation prefix, result, cursor and query generation belong to the same request.
   Changing any selected filter synchronously retires prior rows, errors and pending completion ownership, including an away-and-back edit before another request.
   Stale success, failure and finalisation cannot update the current observation.
   Preserve the existing single-More admission, 50-row limit, descending unique IDs, exclusive cursor and fixed safe error text.
   A failed same-query refresh or page preserves its prior attributable rows; retry keeps the same selected filters and unadvanced cursor.

4. Before publishing any named-query page, require every row to restate the requested bookmark.
   A foreign, empty or missing bookmark rejects the whole page without exposing its payload or advancing the cursor.
   All-account pages may contain multiple bookmarks and events with no recorded bookmark.
   This validation is separate from the connected-account `RecentFor` observation and does not alter it.

5. Every rendered Search row visibly identifies its recorded bookmark ID and, when available, its current bookmark label.
   The ID remains visible even when labels collide, change or disappear.
   An unavailable current bookmark is labelled as unavailable with its retained ID; absence of a current label does not prove deletion.
   An empty recorded ID is shown as No recorded bookmark, not attributed to the active account.
   The view explains that names come from current bookmarks; it does not represent them as historical connection or generation evidence.

## Verification and boundaries

Maintained Go service tests use the actual local store to prove named and all-account projection, identical resources under two bookmarks, combined literal filters, continuation, unavailable bookmarks and accountless events.
Frontend tests exercise the real bridge adapter with account-aware service stand-ins and the actual browser state and drawer.
They cover the selector, visible IDs with duplicate or unavailable labels, first and later pages, disconnected reads, competing live accounts, retry identity, scope rejection and late success/error/finalisation after filter changes.
Preserve the existing audit lifetime and Recent regression suites.
Deliberately removing the mapper, filter and lifetime protections must produce named assertion failures after successful compilation.
Keep before failures, passing controls and any failed attempts in a sealed private evidence packet.

The change needs no schema migration, new audit endpoint or connection check.
The audit API shape already carries the bookmark filter and row field.
The shared fake's Search method must honour the same bookmark filter; unrelated fake mutation behaviour is outside this slice.
Audit retention, mutation attribution, generation provenance, recovery aggregates, account portraits and mutation authority remain unchanged.
An audit label is display information, never permission to act on a resource.
L11, P28, native acceptance and the valuable-data hold remain open.

## Alternatives considered

*Always filter by the connected account.*
This removes intentional All accounts access and prevents reading retained history for another or unavailable bookmark.
Rejected.

*Show the current active account beside every row.*
That fabricates attribution whenever stored history belongs elsewhere.
Rejected.

*Show only the current bookmark label.*
Labels may collide, change or become unavailable and do not establish the historical connection generation.
Keep the retained ID visible.
