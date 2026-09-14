# 0049. Account-scoped listing reads

## Status

Proposed. Nothing in it is implemented yet.

## Context

A listing request names no account and its reply states none.
`BrowseService.Containers` and `BrowseService.Objects` each resolve the session through `connections.Session()` at call time, so they answer from whatever connection is active at the moment the call arrives.
`ContainerPageDTO` and `ObjectPageDTO` carry rows and a next marker and nothing else, so no caller can attribute a page to the account that produced it.

Two consequences reproduce through the service against the two-account fixture, recorded in the [account scope record](../experiments/2026-09-14-account-scoped-results.md).

A page continuation crosses accounts.
A first page requested while one account was connected returned its rows and a marker; after the connection moved, the same caller continued with that marker and received the other account's containers.
The caller sees one listing spanning two accounts, and nothing in either reply says so.

Identical requests answered by different accounts are indistinguishable.
The same container name exists in both accounts, which is ordinary because container names are per-account.
Two calls with identical arguments returned different objects, and neither reply states which account answered.

The connection can move without the reading client acting.
`BrowseService.Connect` is reachable from the bridge, which [ADR 0041](0041-scoped-selection-capabilities.md) already records, so a page can connect elsewhere between a client's two requests.
The browser state guards its own transitions with a monotonic scope counter, but that counter tracks what the client intended rather than what the backend did, so a connection change it did not make does not move it.
The counter also does not move when the same bookmark identifier is reconnected after the bookmark was re-saved to a different account, because it compares identifiers.

This is the read half of the hazard ADR 0041 addressed for selections.
The consequence here is misattribution rather than a destructive effect, but misattribution is what the operator then acts on: a download, a metadata edit, and in the wider product a deletion, all aimed at what they believe they are looking at.

## Decision

A listing read names the account it is for, and its reply restates the account that answered.

1. `Containers` and `Objects` take the bookmark identifier the caller believes it is reading.
   The backend resolves that bookmark's current generation itself rather than accepting one from the caller, and requires it to match the active connection.
   A mismatch refuses behind its own sentinel rather than answering from the connection that happens to be active.

2. Each reply states the bookmark identifier and the generation that produced it.
   A caller can then bind what it displays to an account, and discard a page it cannot attribute, without inspecting the rows.

3. A page continuation carries the same binding as the page it continues.
   A marker alone does not authorise continuing under a different account, so a single listing cannot span two accounts.

## Consequences

A read issued under a stale belief about the connection is refused rather than answered from elsewhere.
Existing callers must name the account, so the generated bindings and the browser state move with the signature.

This does not make a read atomic with the connection.
The account can change after the check and before the request reaches the store, so what this establishes is that a read issued under a stale belief is refused, not that the bytes returned came from the named account.
Closing that would need the session captured for the whole read rather than resolved per call, which is separate work.

It does not authenticate which account the operator approved.
The connect action remains reachable from the bridge, so a page can still move the connection; this makes the move visible to a reader instead of silent, which is the same limit ADR 0041 records for selections.

The listing surface is what this claims.
Inspector detail reads, audit queries, job rows, find results and the metadata surface are unchanged and remain open, and P28 does not close with this decision.
