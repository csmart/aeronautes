# ADR 0076: physical credential cleanup on Linux

**Status:** Proposed on 23 September 2026 for the operator's decision; nothing here is implemented, and no key is removed from any store by this record.
**Scope:** The physical removal of retired credential references on the Linux Secret Service backend, which finish-line item L01 requires and which [ADR 0015](0015-credential-bindings.md), [ADR 0017](0017-keychain-write-verification.md), [ADR 0055](0055-bounded-credential-operations.md) and [ADR 0057](0057-persistent-backend-selection.md) each left unstarted. Retry, the six-target native acceptance and the vault question of ADR 0006 are outside it.

## Problem

Deleting a bookmark, or replacing its credential, retires the previous secret reference in the `credential_work` journal and never touches the keychain: `bookmarks.Store` calls only `Origin`, `Put` and `Get` on the secret store, and two maintained tests assert that nothing runs physical cleanup (`internal/bookmarks/publication_test.go:208`, `internal/app/bookmarkpublication_test.go:140-160`).
So every deleted or re-keyed bookmark leaves its old API key in the operator's keychain, counted under `Retired` in the credential status but never removed.
ADR 0015 gated cleanup on two proofs: "Enable physical cleanup and retry only after both native authority and the complete live-reference coordination above are proven."
Both now exist on Linux: ADR 0057 pins the persistent collection and verifies its identity (`Origin.Persistent`, `VerifiedBy`), and the live-reference set (published references, active prepared writes, acquisition leases and admitted-job pins) is held under one lock in `internal/bookmarks` (`bookmarks.go:196`, `publication.go:176`, `jobpin.go:17`).
The adapter's `Delete` exists, is bounded by the caller's context under ADR 0055, and reports an abandoned native call as unresolved rather than as not done.

## Decision, proposed

1. Cleanup runs only when asked: after a successful bookmark delete or credential replacement the service attempts one cleanup pass for that bookmark's retired references, and the credential status dialog offers "Remove retired keys" for the rest. No timer, no background collector, as ADR 0015 and ADR 0017 require.
2. A pass claims one retired reference at a time under the store lock, only when the active backend's verified identity matches the reference's origin (`origin.VerifiedBy`), and the reference is not published by any bookmark, not in an active prepared write, not leased and not pinned by an admitted job; a claimed reference cannot be acquired by a new user. The lock is released before the external delete, as ADR 0015 requires.
3. The journal row is deleted only on a confirmed removal (`nil` or `ErrNotFound` from a verified backend). `ErrUnresolved` and `ErrAuthorityUnreachable` keep the row and record the attempt with its reason, so a lost deletion reply or an unavailable original store never counts as cleaned; the pass stops at the first unresolved reference.
4. The credential status distinguishes complete deletion from logical deletion with pending cleanup, as ADR 0015 promises: `Retired` splits into `Pending` (never attempted or unresolved, with the last reason) and nothing else, because a confirmed removal leaves no row.
5. Proof, tier 1 under DESIGN §65.12: the private Secret Service fixture's `dismiss-delete`, `read-after-delete-error`, hidden-collection and alias-retargeting modes cover ADR 0015's named cases (failed external deletion, lost deletion reply, changed defaults, unavailable original store); maintained cases assert the journal row survives each of them, that a live reference is never claimed, that a confirmed removal deletes exactly its row, and that a bookmark's logical deletion completes before and independently of the physical pass. The two tests that assert cleanup never runs are rewritten to assert it runs only under these conditions.

## Consequences

The keychain no longer accumulates keys the application retired, on Linux, for the verified persistent backend only; session and legacy fallbacks are never cleaned because absence there is not proof.
This touches credentials and authority, so it is tier 1 with independent reproduction and mutants.

## What this does not establish

Native six-target acceptance stays separate; retry beyond the operator's next explicit pass, locked or cancelled access beyond the ADR 0055 bound, the vault decision of ADR 0006 and the valuable-data hold stay open; L01 completes for v1 only when the operator accepts this record and its slice lands.
