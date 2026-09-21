# ADR 0067: later listing generations

**Status:** Proposed.
This contract precedes its implementation.

## Context

[ADR 0063](0063-account-portrait-binding.md) binds the initial container page to a portrait's bookmark and positive generation and explicitly leaves later reads for follow-up.
On its approved head `2a91c3ba984b9b07c672ad4e61e30fe50b4f1186`, the four later listing paths check only the reply bookmark.
The [frontend assessment](../experiments/2026-09-21-later-listing-generations.md) reproduces newer same-bookmark rows appearing beneath the retained older portrait after a separate caller re-saves and reconnects that bookmark.
Frozen finish-line L11 requires account-specific results, paging, retries and errors.
This is a bounded continuation of that requirement, based on the portrait branch rather than a claim about current master.

## Decision

1. `loadContainers`, `loadMoreContainers`, `#loadEntries` and `loadMoreEntries` capture the retained portrait's bookmark and generation when issuing a request.
   `#loadEntries` covers `openContainer`, `goTo` and parent navigation without adding another acceptance path.
   The captured portrait must name the requested bookmark and have a positive safe-integer generation.
   Before publishing rows or a continuation marker, each reply must name that bookmark and exactly that generation.
   Missing scope, missing fields, nonnumeric or invalid generations, another bookmark and another generation refuse the page.
   The comparison is against the retained portrait, not a newly resolved active connection or the latest saved bookmark.

2. Existing request-lifetime checks run before reply validation or publication.
   A superseded navigation, refresh, connection or bookmark edit cannot replace current rows, markers, loading state or errors with its late success or failure.
   Matching replies remain usable after an unchanged-generation reconnect.
   A correctly captured old-generation reply remains usable when it still matches the retained old portrait, even after another caller has re-saved and reconnected the bookmark.
   No backend read is claimed to remain current throughout that interval.

3. A current request with an invalid or mismatched reply takes its existing failed-read path with an explicit message that the listing does not match the connection, was not shown, and needs reconnecting.
   A container refresh retains its prior rows, clears its marker and displays `containersError`; pagination remains unavailable until a successful refresh or reconnect.
   A new object listing clears its rows and marker and displays `entriesError`, so a failed navigation cannot display the previous prefix as the new one.
   A refused continuation retains the prior rows and exact request cursor, displays the corresponding More error, and rejects its promise as existing page failures do.
   Retry uses the same cursor and captured portrait until the browser explicitly reconnects or starts a new listing.
   Ordinary service errors retain their existing text and behaviour.
   Rejected pages never publish their cursor or any subset of their rows; accepted pages retain marker-advancement and duplicate-row checks.

4. Use a listing-specific validation helper rather than changing `#answeredBy` for all readers.
   Its full reader inventory on the basis is: three connection-time checks, these four later listing checks, and six other live-reader checks (object detail, SLO manifest, DLO segments, prefix detail, Find start status and container detail).
   The three connection-time checks retain ADR 0063's contract.
   The adjacent six checks were source-inspected only and are unproved and outside this slice.
   Historical local job and audit reads have separate retained-record contracts and are not bound to a live portrait generation.

5. Maintain ordinary Vitest regressions around the real `BrowserState` with deferred frontend API replies and explicit generation-bearing fixtures.
   Cover every listing path with same-bookmark replacement, matching and correctly captured old replies, superseded successes and errors, missing/invalid scope, row/cursor consistency and retry.
   Assert errors and cursors independently of row rejection so a first failed assertion cannot conceal an untested cursor claim.
   The shared fake's object listing must use its existing generation-aware scope helper, as its container listing already does, with a direct re-save/reconnect regression.
   Preserve before failures and passing controls, then verify the implementation and compiled, assertion-catching omissions of generation, lifetime and cursor publication checks.
   A transform or compilation failure is not a caught behavioural control.

## Limits

No service, DTO, generated binding, mutation authority, credential policy, database or shared-session helper changes.
Ordinary browser stand-ins prove frontend acceptance behaviour; they do not reproduce a backend scheduling race or native desktop acceptance.
Correct attribution is not authorisation to mutate, proof of a current session or proof that a listing is authoritative.
The six adjacent readers, broader live-read atomicity, L11/P28 completion, native acceptance and the valuable-data hold remain open.

## Alternatives considered

A bookmark-only check admits a later generation of that same bookmark.
Rejecting every old-generation reply after an external reconnect would also discard correctly captured replies that still match the displayed portrait.
Changing the generic scope helper would silently extend the contract to unproved reader surfaces with different lifetimes.
