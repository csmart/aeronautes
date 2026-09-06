# Keychain acknowledgement and error boundaries

## Scope

[ADR 0017](../adr/0017-keychain-write-verification.md) records the write-verification contract and its staged implementation.
The first implementation establishes the isolated Linux protocol fixture and removes the credential reference from the existing decode error.
Write and startup acknowledgement verification remain the next implementation step.
Native original-store selection, bounded native prompts and physical credential cleanup remain open.

## Protocol finding

A disposable private D-Bus service exercised the real pinned `zalando/go-keyring` v0.2.8 client with synthetic values.
Dismissed creation returned nil from library Set, followed by `ErrNotFound`; dismissed deletion returned nil while the value remained readable.
Changing the default collection caused Get and Delete to report `ErrNotFound` while the original collection retained the value.
Unchanged-default write/read and accepted deletion controls passed on that same fixture.
Independent local review repeated all ten assertions on its own private daemon.
These are protocol fault-injection observations, not a test of native GNOME Keyring, KWallet, Windows Credential Manager or macOS Keychain.

## First implementation: isolated fixture and decode error

The permanent [protocol test package](../../internal/secrets/protocoltest/) runs in a separate binary from the existing native keychain tests because godbus caches its session-bus connection process-wide.
Its TestMain creates a short socket path in a per-run mode-0700 directory, starts a daemon without service activation, sets the address before client use and checks that address at each scenario.
Startup is limited to five seconds, daemon teardown to three seconds after kill, and the complete fixture run to 45 seconds.
The required Go CI job explicitly installs `dbus` and invokes `make test-secret-service`, which enables the required-fixture flag.
Missing tooling cannot satisfy that lane by skipping.

`TestKeychainDecodeErrorHidesReference` inserts a malformed synthetic value through the real keyring client, then reads it through the Aeronautes adapter.
It failed on the original adapter because the returned decode error included the internal reference name.
Removing the name makes the regression pass while preserving the safe `base64.CorruptInputError` classification.
Raw and encoded synthetic values must also remain absent from the error.
Restoring the original adapter with a Go overlay still fails this assertion on the final fixture.
The binary round-trip control covers non-UTF-8 bytes, replacement and deletion through the same protocol.

Additional fixture controls used a compiled test binary and temporary daemon commands:

| Condition | Observation |
| --- | --- |
| Daemon missing, required flag set | Exit 1 before client use |
| Daemon missing, optional local mode | Two explicit test skips, exit 0 |
| Unexpected socket address | Exit 1 before client use |
| Daemon exits without an address | Exit 1 |
| Daemon never announces readiness | Exit 1 after approximately 5.01 seconds |

Each control left no new private fixture directory.
These controls exercise fixture startup and refusal; they do not prove native prompt cancellation or a physical-store deletion acknowledgement.

The full internal suite passed 443 top-level tests in 20 packages, with zero failures or skips, under race detection with both owned SAIO accounts, official rclone 1.74.3 and the compatibility matrix enabled.
Statement coverage was 82.9%; scenario reconciliation remained valid at 10/104 cells, with 47/48 actions carrying gaps.
Application build, vet, formatting, workflow validation and the internal build without CGO passed.
The secrets package also cross-compiled for Windows amd64 and macOS arm64; that is compilation evidence, not native execution.
The unchanged frontend tree reused its previously built embedding assets for the local application build; this slice changed no frontend source or bridge signature.

Independent implementation review and the protected PR/master pipeline gates follow before delivery.
The valuable-data release hold remains.
