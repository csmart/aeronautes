# ADR 0057: verified persistent-backend selection

**Status:** Proposed. Nothing in it is implemented yet.
Native acceptance on the six targets remains separately required. L01 does not close.

## Context

`KeychainStore` still has no pinned native store identity.
The pinned `github.com/zalando/go-keyring` v0.2.8 Linux provider calls `GetLoginCollection` on every Set, Get and Delete: it uses `/org/freedesktop/secrets/collection/login` when that path is in `Collections`, otherwise `/org/freedesktop/secrets/aliases/default`.
Those selectors are not immutable.
`Origin` records only `Kind` and a random adapter `Instance`, and `Origin.VerifiedBy` is true only for a live session store reading its own instance.

The [keychain write record](../experiments/2026-09-06-keychain-writes.md) already showed the protocol consequence: changing the default collection made Get and Delete report `ErrNotFound` while the original collection kept the value.
The L01 read boundary maps unverified keychain absence to `ErrAuthorityUnreachable`, so the bookmark layer does not currently call that miss erasure.
It also cannot keep the value readable after the selector moves, and the journal has no collection identity to hold unresolved cleanup against.

ADR 0015 step 5 requires verified persistent-backend selection before absence can prove completed cleanup.
Physical cleanup is still forbidden until that identity exists.
Production never calls `SecretStore.Delete` outside tests; this decision does not start a collector.

## Decision

Record the original Linux Secret Service collection and use that identity for later selection.

1. After a successful Linux probe or credential write, resolve the collection that accepted it to a collection object path.
   Resolve the default alias with `ReadAlias("default")` when `GetLoginCollection` selected `/org/freedesktop/secrets/aliases/default`.
   Never persist that alias object path as authority: it follows a later default change.

2. Store the resolved path on `Origin` as a new `Persistent` field.
   Do not overload `Instance`.
   `Instance` remains the adapter lifetime and never proves a native store.
   Persist `Persistent` as `origin_persistent` on `bookmarks` and `credential_work`.
   Existing empty values stay unverified legacy or non-Linux provenance.

3. A Linux `KeychainStore` pins that path at construction and uses it for every later Get, Put and Delete, through the library's `secret_service` helper, not by calling `GetLoginCollection` again.
   ADR 0012 and the pinned module version stay.
   ADR 0055 wait bounds stay; this adds no backstop deadline on Get, Put or Delete.

4. If the pinned collection is absent from `Collections`, Get, Put and Delete return `ErrAuthorityUnreachable`, never `ErrNotFound`.
   A second adapter whose `Persistent` differs is not `ReadableBy` for a recorded origin with a different path, so `ConnectionConfig` does not search the new default.

5. `VerifiedBy` for a keychain origin becomes true only when `Persistent` is non-empty and equals the active store's `Persistent`.
   Session verification is unchanged (`Kind` and `Instance`).
   A keychain origin with empty `Persistent` stays unverified.
   Absence may be reported as erasure only under that predicate.
   That is original-store absence for the read path, not cleanup authority.

## Readers of the changed predicate

`Unverified` currently counts every keychain and legacy row because no keychain origin can be verified.
After this decision it counts rows this session still cannot confirm, including empty-`Persistent` keychain and legacy rows and any recorded path that does not match the active store.
Zero still does not mean keys were removed.

Readers: `bookmarks.CredentialStatus` and `unverifiedWork`, `app.CredentialStatusDTO`, `frontend/src/features/browser/CredentialStatusDialog.svelte` and its tests, `internal/secrets/protocoltest/publication_linux_test.go`, `internal/systemtest/credential_test.go`, `internal/bookmarks/credential_authority_test.go` and `internal/app/credentialauthority_test.go`.
`Persistent` does not appear in errors, DTOs or audit records.

## What this does not establish

It does not call `SecretStore.Delete` in production, start a collector, retry physical removal or claim original-store erasure for cleanup.

It does not replace ADR 0012, choose the vault in ADR 0006, or establish native keychain behaviour on macOS or Windows; those platforms keep empty `Persistent`.

It does not close L01.
Locked or cancelled access, physical cleanup and retry, native six-target acceptance and the valuable-data hold remain open.

## Tests

Prove this on the existing Linux Secret Service protocol fixture, extended with a second collection and default-alias retargeting.
`GetLoginCollection` prefers `/collection/login` when that path is listed, so a changed-default case must exercise the alias path rather than only `SetAlias` while login remains listed.

Required cases:

- Same store still reads the original value after the default alias is retargeted.
- A new store that follows the new selector reports unreachable, not not-found; the original collection still holds the value; journal `Persistent` is unchanged.
- Hiding or removing the original collection returns `ErrAuthorityUnreachable`, not `ErrNotFound`.
- Absence from the pinned collection, while that collection is still listed, may be erasure under `VerifiedBy`.
- Empty-`Persistent` keychain and legacy origins still cannot prove erasure.
- Session `VerifiedBy` is unchanged.
- Failed Put still makes no compensating Delete.
- Returned errors contain no collection path, reference or credential value.
