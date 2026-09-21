# ADR 0057: verified persistent-backend selection

**Status:** Accepted for the Linux Secret Service fixture pin.
Native acceptance on the six targets remains separately required. L01 does not close.

## Context

This section describes the parent implementation at `e770f8a9`, before this decision was implemented.

`KeychainStore` still has no pinned native store identity.
The pinned `github.com/zalando/go-keyring` v0.2.8 Linux provider calls `GetLoginCollection` on every Set, Get and Delete: it uses `/org/freedesktop/secrets/collection/login` when that path is in `Collections`, otherwise `/org/freedesktop/secrets/aliases/default`.
Those selectors are not immutable.
`Origin` records only `Kind` and a random adapter `Instance`, and `Origin.VerifiedBy` is true only for a live session store reading its own instance.

The [keychain write record](../experiments/2026-09-06-keychain-writes.md) already showed the protocol consequence: changing the default collection made Get and Delete report `ErrNotFound` while the original collection kept the value.
The L01 read boundary maps unverified keychain absence to `ErrAuthorityUnreachable`, so the bookmark layer does not currently call that miss erasure.
It also cannot keep the value readable after the selector moves, and the journal has no collection identity to hold unresolved cleanup against.

`internal/secrets/keychain.go` imports only the top-level `keyring` package and calls `keyring.Get`, `keyring.Set` and `keyring.Delete`.
It never imports `secret_service` and never holds a `dbus.BusObject`.
Those three library entry points each call `GetLoginCollection` inside `keyring_unix.go`, so the pin cannot be honoured through the `keyring` API.

The pinned `secret_service` package has no `ReadAlias` and no helper that takes a pinned collection path.
Its exported methods are `OpenSession`, `CheckCollectionPath`, `GetCollection`, `GetLoginCollection`, `Unlock`, `Close`, `CreateCollection`, `CreateItem`, `SearchItems`, `GetSecret` and `Delete`.
`CreateItem` and `SearchItems` take a `dbus.BusObject`.
The string "Alias" appears only as `loginCollectionAlias`.
`Unlock` skips its "did I unlock the collection I asked for" check when the path is the alias, which is independent evidence that the alias object is not an identity.

ADR 0015 step 5 requires verified persistent-backend selection before absence can prove completed cleanup.
Physical cleanup is still forbidden until that identity exists.
Production never calls `SecretStore.Delete` outside tests; this decision does not start a collector.

## Decision

The Linux adapter stops using `keyring.Get`, `keyring.Set` and `keyring.Delete`.
It drives `secret_service` directly: open and close a session, unlock the collection before SearchItems and the item before GetSecret, search by the existing service and username attributes, create items on the selected collection, map empty search results to `ErrNotFound`, and inherit the helper's prompt path.
Non-Linux platforms keep the current `keyring` API.
The platform split uses `keychain_linux.go` and `keychain_other.go`.

The pin is a collection object path retained by `KeychainStore`.
Each operation opens a `SecretService` and reconstructs the `dbus.BusObject` for that exact path before passing it into `CreateItem` or `SearchItems`.
`GetSecret` and `Delete` receive item paths returned by `SearchItems`, preserving the library's unix provider behaviour.
`ReadAlias` is a direct D-Bus call on the Secret Service interface, used only when `GetLoginCollection` selected the alias object.

1. At Linux construction, call `GetLoginCollection` once and retain the selected collection path.
   Never call `GetLoginCollection` again on that store.

2. If the selected object's path is listed in `Collections` and is not `/org/freedesktop/secrets/aliases/default`, that path is `Persistent`.
   `GetLoginCollection` only returns the login collection path after `CheckCollectionPath` has listed it.

3. If the selected object's path is the default alias object, resolve it with a direct D-Bus `ReadAlias("default")` on the service object taken from the library connection (`SecretService` embeds `*dbus.Conn`; the helper has no `ReadAlias`).
   Holding the alias object is not a pin: Secret Service forwards collection methods on that path to whichever collection the alias currently names.
   If `ReadAlias` returns a collection path that `CheckCollectionPath` accepts and that is not the alias object path, that path is retained as the pin and `Persistent`.
   Later operations reconstruct it with `SecretService.Object("org.freedesktop.secrets", path)`.
   Do not reconstruct through `GetCollection` by stripping and reappending `/org/freedesktop/secrets/collection/`.

4. If `ReadAlias` fails, returns empty, returns the alias path, or `CheckCollectionPath` rejects the result, `Persistent` stays empty.
   Do not persist the alias object path as authority.
   The adapter does not claim original-store identity.
   Get, Put and Delete on that instance return `ErrAuthorityUnreachable` for a miss, never `ErrNotFound`.
   Construction may still succeed through the unresolved alias; that does not mint a `Persistent` id.

5. Store the resolved path on `Origin` as a new `Persistent` field.
   Do not overload `Instance`.
   `Instance` remains the adapter lifetime and never proves a native store.
   Persist `Persistent` as `origin_persistent` on `bookmarks` and `credential_work`.
   Existing empty values stay unverified legacy or non-Linux provenance.

6. Before an operation on a verified pin, open a `SecretService`, call `CheckCollectionPath` on the retained path, and if it is listed reconstruct the `BusObject` with `Object` as in step 3.
   If it is absent, Get, Put and Delete return `ErrAuthorityUnreachable`, never `ErrNotFound`.
   Do not recover a recorded pin by calling `GetLoginCollection`.

7. A second adapter whose `Persistent` differs is not `ReadableBy` for a recorded origin with a different path, so `ConnectionConfig` does not search the new default.
   After an application restart, construction selects once again and this same predicate checks the saved origin; this implementation does not reopen a different saved collection automatically.

8. `VerifiedBy` for a keychain origin becomes true only when `Persistent` is non-empty and equals the active store's `Persistent`.
   Session verification is unchanged (`Kind` and `Instance`).
   A keychain origin with empty `Persistent` stays unverified.
   Absence may be reported as erasure only under that predicate.
   That is original-store absence for the read path, not cleanup authority.

9. Wrap every new native call in the existing `awaitKeychain` bound from ADR 0055.
   Those bounds do not survive the move off `keyring.Get/Set/Delete` by themselves; they must be re-established around the `secret_service` calls.
   Initial connection and collection selection use the same startup context as the probe; failed-probe cleanup must not wait beyond that context either.
   This adds no backstop deadline on Get, Put or Delete.
   The helper's `handlePrompt` remains unbounded except by that wait, which is the same surface the protocol-test abort and ADR 0055 already name.
   ADR 0012 and the pinned module version stay.

## Readers of the changed predicate

Before this decision, `Unverified` counted every keychain and legacy row because no keychain origin could be verified.
It now counts rows this session still cannot confirm, including empty-`Persistent` keychain and legacy rows and any recorded path that does not match the active store.
Zero still does not mean keys were removed.

Readers: `bookmarks.CredentialStatus` and `unverifiedWork`, `app.CredentialStatusDTO`, `frontend/src/features/browser/CredentialStatusDialog.svelte` and its tests, `internal/secrets/protocoltest/publication_linux_test.go`, `internal/systemtest/credential_test.go`, `internal/bookmarks/credential_authority_test.go` and `internal/app/credentialauthority_test.go`.
`Persistent` does not appear in errors, DTOs or audit records.

## What this does not establish

It does not call `SecretStore.Delete` in production, start a collector, retry physical removal or claim original-store erasure for cleanup.

It does not replace ADR 0012, choose the vault in ADR 0006, or establish native keychain behaviour on macOS or Windows; those platforms keep empty `Persistent` and the current `keyring` API.

It does not close L01.
Locked or cancelled access, physical cleanup and retry, native six-target acceptance and the valuable-data hold remain open.

## Tests

Prove this on the existing Linux Secret Service protocol fixture, extended with a second collection and default-alias retargeting.
The fixture must implement `ReadAlias` for the alias-selector case.
`GetLoginCollection` prefers `/collection/login` when that path is listed, so a changed-default case must exercise the alias path rather than only `SetAlias` while login remains listed.

Required cases:

- Same store still reads the original value after the default alias is retargeted, when `Persistent` was recorded.
- A new store that follows the new selector reports unreachable, not not-found; the original collection still holds the value; journal `Persistent` is unchanged.
- Hiding or removing the original collection returns `ErrAuthorityUnreachable`, not `ErrNotFound`.
- A failed `ReadAlias` leaves `Persistent` empty, persists no alias path, and reports unreachable rather than not-found.
- Absence from the pinned collection, while that collection is still listed, may be erasure under `VerifiedBy`.
- Empty-`Persistent` keychain and legacy origins still cannot prove erasure.
- Session `VerifiedBy` is unchanged.
- Failed Put still makes no compensating Delete.
- A cancelled Get, Put or Delete on the Linux `secret_service` path still returns through `awaitKeychain` as ADR 0055 requires.
- Holding `Collections`, `ReadAlias` or probe read-back cannot postpone session fallback beyond the startup deadline, including through a later cleanup attempt.
- Returned errors contain no collection path, reference or credential value.
