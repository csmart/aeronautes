# ADR 0098: settings backup and restore

**Status:** Accepted for the configuration backup without credentials; the opt-in credential bundle waits on [ADR 0006](0006-secret-storage-linux.md).
**Detail:** DESIGN §51 (local state), P28 (the finish line's "tested backup and restore"); requirements K-SEC-003, K-SEC-004.

## Context

An operator builds up configuration in Aeronautes — bookmarks (the connection definitions), their per-bookmark performance profiles and custom CA certificates, and the application settings — and has no way to carry it to a new machine or to put it back after a reinstall.
The finish line owes "tested backup and restore of the database and the keychain items" (P28), and the operator asked for an in-app way to save and restore their settings.

Two facts shape the design.
The valuable configuration lives in SQLite; credentials never do — a bookmark carries a `secret_ref`, a lookup name into the OS keychain, not the secret itself (K-SEC-003).
A keychain is per-machine and per-platform, so a credential saved in a Linux keyring cannot simply reappear in a macOS Keychain; carrying credentials across machines is therefore a deliberate, separate, security-sensitive choice, not part of an ordinary settings backup.

## Decision

### What a backup contains

A backup is an operator-initiated export of **configuration**, not operational history:

- the application settings (`SettingsService` `AppSettingsDTO`);
- every bookmark as its non-secret `BookmarkDTO` (`BookmarkService.List` already omits the credential), together with its performance profile and, for a custom CA it trusts, only the certificate — re-encoded as `CERTIFICATE` PEM blocks, so that whatever was originally imported, no private key or other non-certificate PEM material can travel in a backup.

Job and audit history are deliberately **out of scope**: they are machine-local operational records, they can be large, and the audit trail already has its own §51 export.
A settings backup is a point-in-time snapshot, not continuous sync.

### Credentials are excluded by default

By default a backup carries no credentials.
On restore, each bookmark is recreated without one, so it cannot connect until the operator enters its password, and the restore reports plainly which bookmarks still need a credential.
This is the safe, portable default: it moves a Linux configuration to a macOS machine cleanly, because nothing in it depends on a particular keychain.

### Including credentials is opt-in and always encrypted

The operator may choose to include credentials in a backup.
That choice is off by default, and turning it on **requires a passphrase**: the credential section is encrypted with a key derived from that passphrase, so the file on disk never holds a readable secret.
The encryption uses the established, reviewed vault format decided in [ADR 0006](0006-secret-storage-linux.md) — authenticated encryption and a memory-hard password KDF, no Aeronautes-specific cryptographic construction.
A restore without the passphrase still restores everything else and simply leaves those credentials to be re-entered.
Because a credential only means anything bound to its connection, the encrypted bundle must bind each secret to the bookmark identity it belongs to — the endpoint, the username, the storage-URL override and the TLS and CA trust — so the binding cannot be altered in the plaintext part while the secret is reused ([ADR 0015](0015-credential-bindings.md)); the slice that builds this is reviewed against that requirement.
This part waits on ADR 0006 being resolved and is built after the default backup, so the two are not coupled.

### File format and safe writing

A backup is a single versioned JSON document: a schema version, the time it was written, the application settings, and the list of bookmarks with their profiles and CA certificates (and, when opted in, the encrypted credential section).
It is written through the Go-side file picker — paths never originate in the webview (§55) — using the same safe pattern as the audit export: the bytes go to an exclusively-created `.part` file that is flushed and closed before being published to its final name by an atomic, no-replace link, so a failed or interrupted export never leaves a file that looks complete.

### What a restore does

A restore reads a chosen file and validates the whole of it before it changes anything: it refuses a schema version it does not understand, including a newer one, and it parses and checks every setting, every bookmark field and every CA certificate up front, so a malformed or partly-invalid file is refused with nothing applied.

Once validated it applies the configuration:

- the application settings replace the current ones, because restoring settings is the operator asking for exactly that;
- bookmarks are merged by their complete restorable configuration — not the display name, auth URL and username alone, but also the storage-URL override, the TLS mode and CA trust, and the saved per-bookmark settings — because two bookmarks that share a name, URL and user while pointing at different storage or trusting different CAs are distinct connections the backup means to keep. A bookmark whose complete configuration matches no existing one is created fresh, with a new backend-generated id as [ADR 0015](0015-credential-bindings.md) requires rather than the exported one, so no import exception to that decision is needed; a bookmark whose complete configuration already matches an existing one is left untouched and reported, never overwritten. So a restore onto a fresh machine brings every distinct connection back, a restore onto a populated one never overwrites or conflates two different connections, and importing the same backup twice adds nothing the second time.

The settings and each bookmark are separate commits, so a write error, a cancellation or another fault part-way through can leave the settings applied and only some bookmarks created. The restore says so plainly — what it applied and what it did not — and never implies the earlier changes were undone. Because the merge is additive by identity, running the import again simply finishes it: what already landed is skipped and the rest retried, with no duplicates. A restore changes configuration only; it never starts a transfer, touches a remote, or deletes anything.

## Consequences

- A new `BackupService` owns the two bound actions `ExportSettings` and `ImportSettings`; it reads and writes through the existing bookmark and settings stores rather than reaching into SQLite directly.
- The two new actions change the scenario-contract inventory, so each implementing slice takes the [ADR 0069](0069-preserve-covered-scenario-cells.md) drift route and is classified in the v1 capability table (kept: a settings backup gates and lifts nothing).
- The frontend gains an operator-facing way to export and import, with the "include passwords" choice off by default and a passphrase prompt when it is turned on.
- Delivery is sliced so each part is reviewable: first the default configuration export, then restore, then the opt-in encrypted credential bundle once ADR 0006 is resolved.

## Limits

This covers configuration, not the job and audit history, and not continuous synchronisation; the broader P28 acceptance of the whole database and the keychain items is a separate concern it does not close.
Carrying credentials between machines is only possible through the opt-in encrypted bundle, which depends on ADR 0006; without it, passwords are re-entered after a restore.
A custom CA is exported as its certificate only, never the bytes as imported, so a certificate-and-key bundle cannot leak a private key into a backup.
A restore applies the settings and then each bookmark as separate commits, so an interrupted restore can be partial; it reports what it applied and is safe to run again, rather than being rolled back.
A restore trusts the operator who supplies the file to that running instance; it validates and applies only configuration, but a backup file is as sensitive as the configuration it holds and, when credentials are included, as the passphrase that protects them.
