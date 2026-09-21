# ADR 0063: account portrait binding

**Status:** Proposed.
This record requires independent contract review before implementation.

## Context

[ADR 0059](0059-account-scoped-remaining-reads.md) explicitly leaves `BrowseService.Account` and `Capabilities` unbound.
On `5f71a163`, an account HEAD captures one session, but its display name is read from the active session after the HEAD completes.
The [portrait record](../experiments/2026-09-21-account-portrait-binding.md) reproduces alpha's counts with beta's name, and a browser publishing beta's capabilities under alpha even after the initial bound listing refuses the changed account.
The same methods and caller are unchanged from master `74cfc728`.
Frozen finish-line L11 already requires account-specific results, errors and retries; this extends the existing binding contract to these two reads.

## Decision

1. `Account` and `Capabilities` require the bookmark identifier the caller intends to read.
   `Connect` passes its requested bookmark into the portrait read.
   Each reply includes the existing `ListingScopeDTO` as `scope`, containing the bookmark and the positive generation resolved by the backend.
   The Go service signatures and DTOs are the bridge contract; generated TypeScript bindings, the frontend API and its fake move together.

2. Each read uses one captured session, bookmark, generation and capability snapshot, validated against the stored bookmark generation before use.
   Empty bookmarks, a different active bookmark, and connections invalidated by deletion or a changed stored generation refuse without returning another account's data.
   Account mismatch uses `ErrAccountScopeChanged`; missing connections and invalidated credentials retain their existing errors.
   The account name comes from the same captured session as the HEAD.
   Capability conversion uses the captured snapshot without resolving the active connection again.
   An unavailable or disabled `/info` remains a successfully bound `source: unknown` reply under DESIGN section 10.

3. The browser captures its connecting bookmark and requires that bookmark on the portrait reply, with a usable positive generation.
   It passes that bookmark to `Capabilities` and requires the same bookmark and generation before publishing either payload.
   Missing scope, a different bookmark, a different generation or a refused capability read fails the attempt without publishing an account or capabilities.
   A refusal must not become `null` capabilities; unknown capability support is represented by the bound unknown reply.
   The existing navigation lifetime guard and single pending connection slot remain.
   Initial listing errors keep their existing separate display behaviour and cannot substitute for successful portrait and capability binding checks.

4. The shared fake enforces the required bookmark and produces the same reply scope as the real API.
   Maintained regressions cover matching reads, stale and empty bookmarks, deletion and re-save, a held alpha HEAD while beta connects, capability replacement, missing and mismatched reply scope, and a same-bookmark generation mismatch between replies.
   The relevant public-service scenarios and real Swift account controls move with the bridge contract.

## Limits

The captured tuple establishes who produced a reply, not which account remains active when the browser displays it.
A later `Connect` may leave a correctly labelled alpha read in flight while beta becomes active.
This does not authenticate the operator's intended account or grant authority for a later mutation.
The shared `scopedSession` helper, the broader connection/read atomicity race described by ADR 0049/0059, `AuditService.Search` and mutation writers are unchanged.
No database schema or operation policy changes.
L11, P28, native acceptance and the valuable-data hold remain open.

## Alternatives considered

Adding only a reply scope would still allow a request issued for alpha to read beta.
Adding only a bookmark check would still mix alpha's HEAD with a later active account name unless all reply data came from the captured tuple.
Combining portrait and capabilities into a new connect response would leave the two independently callable bridge reads unbound and need a larger API change.
Locking the global connection across remote I/O would change connection and mutation authority beyond this slice.
