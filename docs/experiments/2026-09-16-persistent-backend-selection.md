# Verified persistent-backend selection — 16 September 2026

The Linux keychain adapter still follows the current Secret Service selector on every call.
This record confirms that, records the correction in [ADR 0057](../adr/0057-persistent-backend-selection.md), and states what it leaves open.

## What was already shown

The [keychain write record](2026-09-06-keychain-writes.md) exercised the real pinned `zalando/go-keyring` v0.2.8 client on a private D-Bus service.
Changing the default collection caused Get and Delete to report `ErrNotFound` while the original collection retained the value.
That is protocol fault-injection evidence, not a native GNOME Keyring or KWallet test.

The current tree still has that selector.
`github.com/zalando/go-keyring@v0.2.8/keyring_unix.go` calls `GetLoginCollection` in `Set`, `findItem` and `findServiceItems`.
`secret_service.GetLoginCollection` uses `/org/freedesktop/secrets/collection/login` when that path is in `Collections`, otherwise `/org/freedesktop/secrets/aliases/default`.
`Origin` has only `Kind` and `Instance`.
`VerifiedBy` accepts only a live session store reading its own instance.

The L01 read boundary already maps unverified keychain absence to `ErrAuthorityUnreachable`.
The remaining gap is that the adapter can still look in a different collection than the one that accepted the write, so a changed default makes the value unreadable through the adapter while it still exists, and the journal has no collection path to hold unresolved cleanup against.

`KeychainStore` calls `keyring.Get`, `keyring.Set` and `keyring.Delete`.
Those three library entry points each call `GetLoginCollection` inside `keyring_unix.go`, so the pin cannot be honoured through the `keyring` API.
The Linux path therefore leaves that API and drives `secret_service` directly.
The pinned helper has no `ReadAlias` and no method that takes a collection path.
`CreateItem` and `SearchItems` take a `dbus.BusObject`.
The contract holds that object and, only when `GetLoginCollection` selected `/org/freedesktop/secrets/aliases/default`, resolves it with a direct D-Bus `ReadAlias("default")` on the service object from the library connection.
A failed alias read leaves `Persistent` empty and does not persist the alias path.
`awaitKeychain` must wrap the new calls; the ADR 0055 bound does not survive the move by itself.

## Blast radius, counted where the values are read

`bookmarks.Store.Save` writes through `SecretStore.Put`.
`bookmarks.Store.ConnectionConfig` reads through `SecretStore.Get`.
No production caller invokes `SecretStore.Delete`; bookmark deletion records retired work and leaves the key.

`Origin` is persisted as `origin_kind` and `origin_instance` on `bookmarks` and `credential_work`.
Readers that would also carry `Persistent` are `scanRecord`, `recordWork`, `publishRecord`, `unverifiedWork`, the publication protocol test, `publication_test.go`, `status_test.go` and `credential_authority_test.go`.

`Unverified` readers are `CredentialStatus`, `CredentialStatusDTO`, `CredentialStatusDialog.svelte` and the tests named in ADR 0057.
The dialog already says that a zero unverified count does not mean keys were removed.

## What the fixture must distinguish

`GetLoginCollection` prefers the login collection when that path is listed.
A test that only retargets the default alias while `/collection/login` remains in `Collections` does not exercise the changed-default miss.
The existing protocol fixture currently exports only the login collection.
The implementation extends that fixture with a second collection, `ReadAlias`, and alias retargeting, including a case where login is not listed so the default alias is the selector, and a case where `ReadAlias` fails.

## Correction delivered

Linux `KeychainStore` drives `secret_service` with a held collection object and records the resolved path as `Origin.Persistent`.
Protocol tests cover same-store retarget, a new store after a changed default, a hidden original collection, a failed `ReadAlias`, pinned absence as erasure, and cancelled waits.
Physical Delete and the collector stay unstarted.

## What this does not claim

No key is removed from any store by the proposed correction, and the collector stays unstarted.

Native keychain behaviour on the six targets is not established; this evidence is the Linux Secret Service fixture alone.

L01 does not close.
Physical cleanup and retry, locked or cancelled access beyond ADR 0055, and the valuable-data hold remain open.
