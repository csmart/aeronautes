# ADR 0041 — scoped selection capabilities

**Status:** Proposed on 13 September 2026 for the Linux finish-line item L02.
**Scope:** The picker-issued selection token and the two local transfer boundaries that consume it.
No change to supported operations or to the ADR 0020 refusals.
This is a bounded part of L02, not L02 itself; the open remainder is named below.

## Problem

A picker selection is an opaque token over a session-lifetime map ([ADR 0011](0011-typed-service-surface.md) keeps paths out of the webview).
The token records the selection and nothing else.

It carries no operation, no account, no container, no prefix, no issue time and no credential generation.
The requested bookmark arrives beside the token in the request, so the two are independent values that the backend never relates.
Security review row SEC-07 states the consequence directly: a retained token can be used with a different requested bookmark.

Reproduced against unchanged source at `31c6c3d5`, through `PreflightUpload` and `PreflightDownload` rather than against the store:
a directory picked while one account was connected accepts a download from a second, genuinely separate account;
a file selection picked for one account uploads into another;
a token issued before a bookmark was re-saved is still accepted under the new generation;
and one token is replayed against a different prefix in the same container.
Three healthy controls pass in the same run, so the four refusals to come are the change and not a broken harness.

A widened scope here destroys or discloses the wrong data, which is why L02 leads the data-safety ranking.

## Decision

A selection becomes a capability that names the operation it may perform and the single scope it may perform it in.

**Bind at issue.**
The pick methods take the bookmark, container and prefix the selection is being made for, and the backend resolves that bookmark's current generation itself.
When there is an active connection the pick must name it, so a page cannot pick for an account the user is not looking at.
The check only narrows: re-saving a bookmark drops the active connection, and the selection is still pinned to the one account it names for its whole life.

**Observe the endpoint across the blocking picker.**
The connection endpoint — the connected bookmark and its generation, or the honest absence of one — is read before the native dialog opens and read again when it returns, and the two observations must be identical.
Absence means nothing is connected and nothing else.
The endpoint is read directly rather than through the validating snapshot, which reports the same condition both when nothing is connected and when the connection changed while it was being read — states that mean opposite things here, since the second can leave another account connected.
The named bookmark's generation is re-read at the same point, which is what catches a re-save while nothing is connected.
Together these close the window in which a page swaps, retires or republishes the account underneath a dialog the user is already looking at.

The store records, per token: operation, bookmark identifier, generation, container, prefix and issue time.

**Separate read-only validation from consumption.**
Preflight is read-only and may validate the same token repeatedly.
Start is the effect, and it consumes the token atomically under the store lock, so the token is gone once a job owns its handle.
Two concurrent starts on one token therefore cannot both win, and a page cannot replay a confirmed selection into further jobs.
A user who wants the same transfer twice picks again; that is the intended cost.

**Bounded lifetime as the backstop.**
A token that is never started expires ten minutes after issue.
The interval is sized for a person reading a preflight report and deciding, and short enough that a selection left in a backgrounded page dies.

**Check the whole binding at every use.**
Validation and consumption both refuse unless the token is known, its operation matches the boundary using it, its bookmark identifier equals the requested one, its generation is still current, its container and prefix equal the requested ones, and its lifetime has not elapsed.
Refusals are distinct sentinel errors, so a caller, a test and the dialog can tell an expiry from a scope violation, and a deliberately broken control fails only its own case.

## What this does not establish

Reading the connection registry is **partial** protection against retargeting, not proof of what the user approved.

`BrowseService.Connect` is a bridge method (`frontend/src/lib/api.ts` calls it).
A page can therefore connect to account B before opening the picker while still rendering account A, and every check then passes for B.
Every check above passes, because the selection really was made while B was active.
What the binding defeats is the finding as SEC-07 words it — a **retained** token used with a different requested bookmark — together with replay, expiry, operation confusion, generation drift and mid-dialog account swaps.
What it cannot do is authenticate which account, container and prefix the user believed they were selecting for.

Establishing that needs a trusted acknowledgement of account, container and prefix for selected local resources, presented outside the webview's control.
That is an open L02 requirement and it is not delivered here.
It is a separate requirement from the trusted acknowledgement the forged force and public access-control cases need in `mutateservice.go` and `segmentservice.go`, which is also open.
L02 is not complete when this slice lands.

## Alternatives considered

*Bind on first use, backend only.*
The token would learn its scope from the first request carrying it and refuse any later mismatch.
It needs no contract change and it closes replay.
It does not close the retained-token case at all: a hostile page pairs a fresh pick with another account and the first use then defines that account as legitimate.
Rejected.

*Reading the account only from the connection registry, never from the caller.*
This was the first decision here.
It fails for a reason that has nothing to do with security: republishing a bookmark drops the active connection, and no other local transfer method needs one, so the pick alone would refuse whenever credentials had just been edited.
Superseded by the caller-named bookmark with a connection check layered on top, which pins the token to one account just as firmly.

*Caller-named account with no connection check at all.*
Simplest, and it still defeats the retained-token case, because the token is pinned to the one account it names.
Rejected because it would let a page open a picker for an account the user is not looking at whenever a connection does exist, and that check costs nothing.

*Single use for validation as well as start.*
Rejected because preflight is read-only and a user may reasonably re-run a report; consumption is placed at the effect instead.

## Consequences

The bridge surface changes, so the generated TypeScript bindings and the two Svelte call sites change with it in the same commits.
The change is small at the call sites because the dialog already freezes its scope before invoking the picker.

Navigating elsewhere does not invalidate a selection.
The selection stays valid for its own scope until it is started or expires; a request naming a different scope is what gets refused.
Re-saving a bookmark does invalidate it, because the generation moves.

A start consumes the selection, so the dialog asks for a fresh pick before a second identical transfer.

Selection state grows by six fields per entry and gains an expiry sweep beside the existing oldest-first eviction.
