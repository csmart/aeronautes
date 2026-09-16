# ADR 0059: account-scoped remaining reads

**Status:** Accepted.
Implemented in the range that carries this record.
This does not close L11, P28 or the valuable-data hold.

## Context

[ADR 0049](0049-account-scoped-reads.md) bound `BrowseService.Containers` and `BrowseService.Objects`.
A listing read names the bookmark it is for, the backend resolves that bookmark's generation itself, a mismatch refuses behind `ErrAccountScopeChanged`, and each page restates the account that answered.
ADR 0049 says the rest of P28 stays open: inspector detail, find results, audit queries, job rows and the metadata surface were unchanged.

Those remaining reads named no account, and most of their replies stated none.
The same two-account fixture and the same bridge-reachable `Connect` that ADR 0049 used still apply.
Container names are per-account, so a collision between two accounts is ordinary rather than contrived.
The [remaining-reads record](../experiments/2026-09-16-account-scoped-remaining-reads.md) is the source survey of those methods on `e770f8a9`.

Inspector detail is a live Swift HEAD.
`InspectorService.Object`, `Prefix`, `Container`, `SLOManifest` and `DLOSegments` each called `connections.Session()` at call time.
Their reply types carried the resource and nothing that named an account.
`Container` classified evidence with `connections.ActiveBookmarkID()`, so a classification could be recorded against whichever bookmark was active rather than the one the caller believed it was inspecting.

Find is a live Swift scan that captures a session once.
`FindService.StartFind` took container, prefix and query, and no bookmark.
`FindStatusDTO` carried matches, counts and error text, and no account.
A scan started under one account could still be polled after `Connect` moved, and nothing in the snapshot said which account produced the matches.

Audit recent is a local trail read, not a Swift session.
`AuditService.Recent` took only a limit and returned newest events across every bookmark.
`AuditEventDTO` omitted `BookmarkID` even though `audit.Event` stores it, so a caller could not attribute a row without inspecting other fields that were never meant to carry an account.
`Search` already accepts an optional `bookmarkId` and the frontend doorway currently passes an empty one, so a Search page can also mix accounts.

Job get is a local row read.
`JobService.List` and `History` already take a bookmark identifier.
`Get` took only a job id, and `Items` / `ItemTotals` paged that job by id alone.
`JobDTO` already names source and destination bookmarks, so the row can be attributed after it arrives, but a Get or an item page issued under a belief about one account still answered for another.

The remaining metadata *reads* are those inspector HEADs.
`MutateService` writes are a different surface and are not this decision.

The browser state's monotonic scope counter still tracks what that client intended rather than what the backend did, and this slice does not edit `frontend/src/features/browser/browserState.svelte.ts`.

## Decision

The remaining P28 read surfaces name the account they are for, and their replies restate the account that answered, using the same check ADR 0049 already uses where the read goes to Swift.

1. Live Swift reads reuse ADR 0049's binding.
   `InspectorService.Object`, `Prefix`, `Container`, `SLOManifest` and `DLOSegments`, and `FindService.StartFind`, take the bookmark identifier the caller believes it is reading.
   The backend resolves that bookmark's current generation itself rather than accepting one from the caller, requires it to match the active connection, and refuses a mismatch behind `ErrAccountScopeChanged`.
   Each inspector reply carries `ListingScopeDTO` as `scope`.
   `Container` classifies evidence against the named bookmark, not `ActiveBookmarkID()` after the fact.

2. A Find scan is bound at start and keeps that binding for its life.
   `StartFind` stores the bookmark and generation that passed the check, and the worker keeps using the session captured then, so a later `Connect` does not retarget an in-flight scan.
   `FindStatus` and `CancelFind` take the bookmark identifier the caller still believes it is polling.
   A mismatch with the scan's stored bookmark refuses behind the same sentinel rather than returning another account's matches.
   `FindStatusDTO` restates that stored scope.
   Polling is the continuation: a scan id alone does not authorise reading the scan under a different account.

3. Local store reads filter by the named bookmark and do not require the live connection.
   `JobService.List` already answers for a bookmark that is not the active session, and these reads stay in that shape.
   The bound recent-events read takes the bookmark identifier and returns only that bookmark's newest events, each restating `bookmarkId` on `AuditEventDTO`.
   Because `Recent` lives in `mutateservice.go`, which this slice does not edit, the bound method is `RecentFor` on `AuditService` in a new file.
   The store refuses the unscoped `Recent` path, so that twin cannot answer across accounts.
   `JobService.Get` takes the bookmark identifier and the job id, and refuses unless that bookmark is the job's source or destination.
   `Items` and `ItemTotals` take the same bookmark identifier as the Get they continue, so a job-item page cannot cross accounts.
   Empty bookmark identifiers refuse rather than meaning "every account".

4. Retry and error on these surfaces carry the same binding as the request they retry.
   A refused live read is `ErrAccountScopeChanged`.
   A local row that is not for the named bookmark is refused rather than answered.
   A caller discards a reply whose restated scope is not the account it asked for, without inspecting the payload.

## Consequences

A stale belief about which account a remaining read is for is refused rather than answered from elsewhere.
Existing callers of the bound methods must name the account, so the generated bindings and `frontend/src/lib/api.ts` move with the signatures.

`browserState.svelte.ts` is not edited here.
That file's inspector, find, audit-recent and job-Get call sites remain a named remainder, and so do any TypeScript fixtures that only exist to serve it.
Proof that a bound method refuses a stale belief is a Go service test against the two-account fixture or the local store, not a change in that file.

This does not make a live read atomic with the connection.
The account can still move after the check and before the request reaches the store, which is the limit ADR 0049 already records.
A Find scan captures its session at start, so that particular hole does not apply to an in-flight scan; it still applies to inspector HEADs and to `StartFind` itself.

It does not authenticate which account the operator approved.
The connect action remains reachable from the bridge, which is the same L02 limit ADR 0041 records for selections and ADR 0049 records for listings.

It does not bind `BrowseService.Account` or `Capabilities`, which still resolve `Session()` with no bookmark.
It does not bind `AuditService.Search`, whose empty `bookmarkId` still means every account, because that method lives in `mutateservice.go`.
It does not bind `MutateService` writes, `JobService.RecoveryStatus`, or any destructive path.

L11, P28 and the valuable-data hold stay open.

## Alternatives considered

*Restate the active account in replies, without requiring the caller to name one.*
A caller could then discard a misattributed payload.
It would still *produce* the other account's HEAD, scan, trail or job row, and a client whose scope counter did not move would display it.
Rejected for the same reason ADR 0049 rejected it.

*Change `AuditService.Recent` in place.*
That is the natural signature change, and it would match listings.
The method lives in `mutateservice.go`, which a concurrent slice owns.
Rejected; `RecentFor` on the same type in a new file is the bound recent-events read.

*Capture the session for the whole inspector HEAD.*
That would close the remaining race ADR 0049 deferred.
It is still separate work, and this slice does not take it.
