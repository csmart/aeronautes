# ADR 0110 — switch accounts without signing in again, and show container settings for the rows on screen

**Status:** Proposed on 12 November 2026, from the operator's reports F26 and F32 during the 0.5.0 test run and the decision of 12 November to bring both into 0.5.0.
**Scope:** `Connections.Connect`; the container listing reply; a new remembered-settings table; the container column's reads of the rows on screen and its badges.
**Amends:** DESIGN §14.2 for container rows only; [ADR 0071](0071-container-settings-indicators.md) decisions 2, 3 and 5; [ADR 0066](0066-named-read-session-snapshot.md) for one labelled display.

## Context

Switching to another bookmark in the rail takes seconds, most of them spent signing in again (F26).
`Connections.Connect` always builds a new session, authenticates it and fetches `/info`, even when a session for that bookmark is cached under the bookmark's current generation and every other reader (`SessionFor`, `pinForJob`) would reuse it.
The window then makes the account portrait HEAD, lists the containers and reads the recent audit trail, as it should for a fresh view.
A session renews its own token: a request answered 401 authenticates again and retries, up to the wire library's retry count, as every request already does.

Container settings badges (public, versioned and the rest) appear only after a container is selected, edited or checked with "Check settings" (F32).
ADR 0071 kept listing free of HEADs under DESIGN §14.2 and held facts in memory per bookmark generation, so they also vanish at every restart.
The operator asked on 11 November for two things: read the settings of the containers on screen without a click, and remember the last settings seen, shown muted and never used as authority.

## Decision

1. **Connect reuses a current session.**
   When the bookmark has a cached session whose generation is still the bookmark's current one, checked the way every cached read checks it, `Connect` makes that session active again with the capabilities fetched when it was opened, and neither authenticates nor fetches `/info`.
   With no cached session, or one whose generation is no longer current because the bookmark, its key or its CA was changed, `Connect` opens a new session as now.
   The capabilities are cached with the session they were fetched for and go with it.
   The portrait HEAD, the container listing and the audit read are still made on every connect, so what the window shows is read afresh; only signing in and `/info` are reused.
   A revoked credential still surfaces on the first request, whose 401 re-authentication fails as it does today.
2. **The container column reads the settings of the rows on screen.**
   A container row that stays inside the column's visible area for 300 ms, and has no fact under the current listing generation, is read through the existing `InspectorService.Container` read, the HEAD that selection and "Check settings" already make, so no bridge action is added (ADR 0069).
   At most 4 of these reads are in flight; a row that leaves the visible area before its read starts is dropped from the queue, and a read already sent is allowed to finish and its fact is kept.
   Each container is read this way at most once per listing generation; a failed read leaves the row unchecked with the error in its tooltip and is not repeated automatically, while selection and "Check settings" still read it again.
   While "Check settings" runs, these reads wait, so the two never exceed 4 in flight together.
   Object rows are unchanged: listing objects never HEADs a row.
   This replaces ADR 0071's "listing never HEADs a row" for container rows with a bound that follows what the operator keeps on screen, not the size of the account: rows scrolled past quickly are never read, and a screenful costs one HEAD per row once per connection.
3. **The last settings seen are remembered.**
   Every fact, from selection, an edit, a check or an on-screen read, is also written to a `container_settings_seen` table in the database, keyed by bookmark, the account identity of the storage URL it was read under, and container name, with the derived settings and the observation time.
   A container HEAD answered 404 removes that container's row; deleting a bookmark removes its rows.
   A failed write is logged and does not fail the read that produced the fact, because the remembered value is a convenience.
   The container listing reply carries, for a row with no fact under the current generation, the remembered one as `lastSeen`, and only when it was recorded under the same account identity as the listing's session, so a bookmark edited to point at another account never shows that account's settings.
4. **Remembered settings are shown as remembered, and nothing relies on them.**
   The column shows a `lastSeen` value as the same badges in a muted style, whose tooltip says when they were last seen and that they have not been checked under this connection.
   A current fact replaces it as soon as one arrives.
   The danger banner over a world-readable open container and the inspector's read ACL sentence come only from a current fact, which selecting the container always reads.
   No acknowledgement, edit, gate or decision reads a remembered value: the public-read acknowledgement still checks the value being typed, and the editor still opens from a fresh read.
   This is a deliberate, labelled exception to ADR 0066's rule that a fact read under one connection is not shown under another: it is displayed as history and used for nothing.
5. **Not in this record.**
   Showing the last container list while a switch refreshes it was suggested with F26; with sign-in reused, a switch costs one HEAD and one listing, so it is left until switching is measured again.

## Consequences

- Switching back to a bookmark already used in this session no longer signs in again, unless that bookmark changed in between.
- A container's settings appear soon after it is scrolled into view, and on the next start its last-seen settings appear at once, muted, until they are read again.
- An operator who scrolls through a whole account at reading pace causes one HEAD per container they looked at; that is the cost of the feature and is bounded by attention, not by the namespace.
- A remembered `public` badge can outlive a change made by someone else, which is why it is muted, dated and never acted upon.

## Tests

- Go, switching: connecting twice to one bookmark authenticates once and fetches `/info` once, while the portrait and the listing are fetched both times; after the bookmark or its key is changed, the next connect authenticates again; a reused session whose token has expired re-authenticates on its first 401 and the connect succeeds; a credential revoked in between fails the connect with the existing message.
- Go, remembered settings: each fact source writes a row; a listing under the same account returns it as `lastSeen` for a row with no current fact and not for a row that has one; a bookmark edited to another account gets none; a 404 HEAD removes the row; deleting the bookmark removes its rows; a failed write leaves the HEAD's own result intact.
- Frontend: a row visible for the dwell time is read and one scrolled past quickly is not; never more than 4 reads in flight, together with a running check; a row is read at most once per generation and a failed read is not repeated; remembered badges are muted with the dated tooltip and are replaced by a current fact; a remembered world-read fact shows no banner.
- System, against the owned Swift: a reconnect to the same bookmark makes no auth request (from the fixture's request log), and after a restart over the same database the listing shows the last-seen settings recorded before it.
