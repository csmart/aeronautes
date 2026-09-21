# Verified persistent-backend selection — 16 September 2026

At parent `e770f8a9`, the Linux keychain adapter followed the current Secret Service selector on every call.
This record describes that earlier behaviour, the delivered correction in [ADR 0057](../adr/0057-persistent-backend-selection.md), and what it leaves open.

## What was already shown

The [keychain write record](2026-09-06-keychain-writes.md) exercised the real pinned `zalando/go-keyring` v0.2.8 client on a private D-Bus service.
Changing the default collection caused Get and Delete to report `ErrNotFound` while the original collection retained the value.
That is protocol fault-injection evidence, not a native GNOME Keyring or KWallet test.

At that parent, the adapter still used that selector.
`github.com/zalando/go-keyring@v0.2.8/keyring_unix.go` calls `GetLoginCollection` in `Set`, `findItem` and `findServiceItems`.
`secret_service.GetLoginCollection` uses `/org/freedesktop/secrets/collection/login` when that path is in `Collections`, otherwise `/org/freedesktop/secrets/aliases/default`.
`Origin` had only `Kind` and `Instance`.
`VerifiedBy` accepted only a live session store reading its own instance.

The L01 read boundary already maps unverified keychain absence to `ErrAuthorityUnreachable`.
The remaining gap was that the adapter could look in a different collection than the one that accepted the write, so a changed default made the value unreadable through the adapter while it still existed, and the journal had no collection path to hold unresolved cleanup against.

The earlier `KeychainStore` called `keyring.Get`, `keyring.Set` and `keyring.Delete`.
Those three library entry points each call `GetLoginCollection` inside `keyring_unix.go`, so the pin cannot be honoured through the `keyring` API.
The Linux path therefore leaves that API and drives `secret_service` directly.
The pinned helper has no `ReadAlias` and no method that takes a collection path.
`CreateItem` and `SearchItems` take a `dbus.BusObject`.
The adapter retains the selected collection path and reconstructs its object for later operations.
Only when `GetLoginCollection` selected `/org/freedesktop/secrets/aliases/default` does construction resolve it with a direct D-Bus `ReadAlias("default")` on the service object from the library connection.
A failed alias read leaves `Persistent` empty and does not persist the alias path.
`awaitKeychain` wraps the new calls, including collection selection; the ADR 0055 bound does not survive the move by itself.

## Blast radius, counted where the values are read

`bookmarks.Store.Save` writes through `SecretStore.Put`.
`bookmarks.Store.ConnectionConfig` reads through `SecretStore.Get`.
No production caller invokes `SecretStore.Delete`; bookmark deletion records retired work and leaves the key.

`Origin` is persisted as `origin_kind`, `origin_instance` and `origin_persistent` on `bookmarks` and `credential_work`.
The new field is carried by `scanRecord`, `recordWork`, `publishRecord`, `unverifiedWork`, the publication protocol test and `publication_test.go`; status and authority tests also exercise the predicate.

`Unverified` readers are `CredentialStatus`, `CredentialStatusDTO`, `CredentialStatusDialog.svelte` and the tests named in ADR 0057.
The dialog already says that a zero unverified count does not mean keys were removed.

## What the fixture must distinguish

`GetLoginCollection` prefers the login collection when that path is listed.
A test that only retargets the default alias while `/collection/login` remains in `Collections` does not exercise the changed-default miss.
Before this correction, the protocol fixture exported only the login collection.
The implementation extends that fixture with a second collection, `ReadAlias`, and alias retargeting, including a case where login is not listed so the default alias is the selector, and a case where `ReadAlias` fails.

## Correction delivered

Linux `KeychainStore` drives `secret_service` with a retained collection path and records it as `Origin.Persistent`.
Protocol tests cover same-store retarget, a new store after a changed default, a hidden original collection, a failed `ReadAlias`, pinned absence as erasure, and cancelled waits.
Startup regressions also hold `Collections`, `ReadAlias` and probe read-back; selection and cleanup must share the ten-second fallback deadline.
Physical Delete and the collector stay unstarted.

## Startup deadline verification — 21 September 2026

Against the production code at `c0a68aec`, all three maintained startup cases failed after twelve seconds with their native reply still held.
At `b3ed3d20`, each returned session fallback at the existing ten-second deadline.
The affected secrets, protocoltest, bookmarks and database race suites passed; protocoltest took 37.13 seconds under its unchanged forty-five-second watchdog, with another race suite running and starting load averages of 7.47, 4.98 and 2.52.
Vet passed for the same packages, and secrets test binaries compiled for Windows amd64 and macOS arm64 with CGO disabled; compilation establishes no native acceptance.
Raw logs and source identities are retained in `~/code/.aeronautes-work/20260921-l01/`, whose `SHA256SUMS` digest is `18fb9366185eddcc9e9f4cfc4e9c64ab313ac992affd5a7b3347b143ddb976fc`.

## What this does not claim

No saved credential is removed by the production correction, and the collector stays unstarted.

Native keychain behaviour on the six targets is not established; this evidence is the Linux Secret Service fixture alone.

L01 does not close.
Physical cleanup and retry, locked or cancelled access beyond ADR 0055, and the valuable-data hold remain open.
