# ADR 0075: generation acceptance for the six live readers

**Status:** Accepted; implemented on 23 September 2026 after the independent contract review, with the maintained proofs named below.
**Scope:** The frontend acceptance of six live readers (object detail, prefix detail, container detail, SLO manifest, DLO segments and Find status) against the captured account portrait's generation, and the backend's generation check on a retained Find scan. Finish-line item L11. No bridge signature or generated binding changes.

## Problem

[ADR 0067](0067-later-listing-generations.md) bound the four listing readers to the captured portrait's bookmark and generation with `#requireListingScope`, and deliberately left six adjacent live readers on the bookmark-only `#answeredBy` check, naming them "source-inspected only and unproved". Their backend half is now proved: every one goes through the ADR 0066 scoped session, refuses another account, refuses as not connected after a rekey, and answers a reconnected bookmark under a higher generation (`internal/systemtest/reader_generation_test.go`). What remains is the frontend: `#answeredBy` (`frontend/src/features/browser/browserState.svelte.ts:558-560`) compares the bookmark only, so a same-bookmark reply from a newer generation, produced when another caller re-saves the bookmark and reconnects while a detail read is in flight, is accepted and shown as if it belonged to the portrait the browser still holds. For Find, `StartFind` returns only the scan id, `FindStatus` and `CancelFind` compare the bookmark only (`internal/app/findservice.go:228`, `:261`), so a retained scan started under an earlier generation stays pollable after a rekey and reconnect, and the frontend accepts its status on bookmark alone.

The container detail already has half of this: its settings fact is gated by `#factCurrent` on both the scope's and the fact's generation (ADR 0071), while the detail body is admitted on bookmark alone.

## Decision

1. Each of the six reads captures the retained portrait (`this.account.scope`) when it starts, as the listing reads do, and accepts a reply only when the reply's scope names that portrait's bookmark and generation, through a reader-specific helper in the shape of `#requireListingScope`; `#answeredBy` is not widened, keeping ADR 0067 decision 4.
2. A current read with a mismatched or malformed scope takes its existing failed-read path with an explicit message that the detail, manifest, segments or scan does not match this connection and was not shown, and needs reconnecting: the object and prefix detail reads take the existing `detailError` path and publish no body, so the manifest and segment tables that follow a detail are not opened either; the container detail keeps ADR 0071's settings behaviour and additionally refuses the body; a Find scan whose status scope mismatches is shown as failed with that message and is not polled again.
3. On the backend, `FindStatus` and `CancelFind` refuse a scan whose recorded generation is not the connection's current generation for that bookmark, with `ErrAccountScopeChanged`, so a retained scan from an earlier generation cannot be read out after a rekey and reconnect; `StartFind` keeps its signature, because the first status reply carries the scope the frontend validates.
4. Maintained proofs: Vitest cases around the real `BrowserState` with deferred replies and explicit generation-bearing fixtures for each of the six, covering same-bookmark replacement, matching and correctly captured old replies, superseded successes and errors, missing and invalid scope, and the failed-read state asserted independently of the rejection; a Go case that a Find scan started before a rekey and reconnect refuses its poll and cancel afterwards, on the mock and the owned Swift.

## Consequences

Tier 1 under DESIGN §65.12 (account binding): before-and-after regressions through the bridge with the frontend fake and the two-account fixture, and independent reproduction. Frontend-only for five readers; one Go check for Find. Superseded replies are discarded, never merged, so a stale-generation reply can neither be shown nor mixed with current rows.

## What this does not establish

Historical job and audit reads keep their retained-record contracts. Broader live-read atomicity, the caller-named mutation binding named by ADR 0065, native acceptance and the valuable-data hold stay open; L11 does not close by this record alone.
