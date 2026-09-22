# Keychain acknowledgement and error boundaries

## Scope

[ADR 0017](../adr/0017-keychain-write-verification.md) records the write-verification contract and its staged implementation.
The implementation verifies write acknowledgements before credential publication and checks the complete startup probe.
The first reviewed step established the isolated Linux protocol fixture and removed the credential reference from the existing decode error; the second adds the write and startup checks below.
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
Startup is limited to five seconds and daemon teardown to three seconds after kill; the complete fixture run was limited to 45 seconds when this record was written.
That whole-run bound has been three minutes since 22 September 2026, after a hosted runner spent 44 seconds in the package and the watchdog aborted the last of its fourteen tests with the other thirteen passed; the [hosted-runner budget record](2026-09-22-hosted-runner-budget-margins.md) holds the measurements.
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

## Second implementation: write and startup verification

`KeychainStore.Put` now reads the same name back after a successful library Set and requires the exact string supplied to Set.
It compares the encoded string without trimming or decoding, and returns fixed errors for failed native writes or read-back so a backend cannot echo the supplied reference or value.
A verification failure leaves compensation to the existing prepared-work journal.
The startup probe uses the same exact comparison and requires a subsequent `ErrNotFound` after its own deletion; an unavailable check or retained probe selects the existing visible session fallback.
The ordinary credential Delete adapter and native selectors are unchanged, and physical credential cleanup remains disabled at publication level.

The permanent protocol tests exercise eight failure modes: dismissed creation, a write error before or after its effect, missing read-back, a read error containing private material, a different value, a noncanonical encoded value, and a dismissed read-back unlock.
Each mode covers direct Put and both bookmark creation and replacement.
Failed replacement preserves the old bookmark, generation, endpoint and readable key; failed creation publishes no row.
Both retain the prepared candidate and original backend provenance, preserve any partial external value, make no compensating Delete call, and allow a later healthy save.
Ten startup cases add dismissed deletion and a failed lookup after deletion, checking that the returned fallback remains usable.
The successful round-trip control includes empty and non-UTF-8 values.

An acknowledged `GetSecret` barrier holds replacement verification while the old credential, active-save status and SQLite writer remain available.
Releasing the barrier lets the verified replacement publish.
Waits are bounded and cleanup releases the barrier and drains the outstanding operations.

The final test tree with the pre-verification adapter overlaid fails 25 leaf cases, including the read-back barrier and reference-bearing native write errors.
Positive write-error and binary controls distinguish failures that the original adapter already handled.
Six additional negative controls deliberately break the correction or its coordination:

| Disabled or weakened behaviour | Failing cases |
| --- | --- |
| Exact equality check removed | 8 |
| Read-back normalised before comparison, selected direct Put case | 1 |
| Native read error forwarded with private text | 1 |
| Native Set error forwarded with private text | 2 |
| Startup post-delete lookup removed | 2 |
| Bookmark coordinator held across external Put/read-back | 1 |

Every control compiled and failed behavioural assertions.
Independent review repeated the normalisation control across the complete protocol package and observed four failures: direct Put, creation, replacement and startup.
The one-case count above records the narrower author-side selection, not the complete failing set.
Separately, blocking the synthetic OpenSession call triggered the committed fixture watchdog at 45.012 seconds, stopped the daemon and failed the test binary.
This proves the fixture's failure bound, not a bounded production keychain operation.

The final full internal race suite passed 447 top-level tests in 20 packages with zero failures or skips, both owned SAIO accounts, the pinned real rclone and its matrix enabled.
Statement coverage was 83.0%; scenario reconciliation stayed valid at 10/104 cells with 47/48 actions carrying gaps.
The protocol package passed three race-enabled repetitions; application build, vet, formatting and the internal build without CGO passed.
The secrets package cross-compiled without CGO for Linux, Windows and macOS on both amd64 and arm64.
Frontend source and bridge signatures remain unchanged, so native runtime behaviour on the other platforms and desktop prompt latency are still separate gates.

Independent review and green PR/master pipelines are required before delivery.
Read-back cannot establish original-store authority across changed defaults, erase a key from another store, bound a real native prompt or complete P10.
