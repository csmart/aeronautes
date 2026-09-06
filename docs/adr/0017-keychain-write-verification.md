# ADR 0017 — verify keychain write acknowledgements

**Status:** Design accepted after independent local review; implementation is staged below.
**Scope:** P10 credential publication and the startup health probe in ADR 0012.

## Context and oracle

ADR 0015 publishes a new immutable credential reference only after `SecretStore.Put` succeeds.
That assumes success means the supplied value was stored.
The pinned `zalando/go-keyring` v0.2.8 does not always satisfy that assumption on Linux.
Its Secret Service helper returns the prompt's dismissed flag, but `CreateItem` and `Delete` discard it and can return nil after cancellation.
The [Secret Service prompt contract](https://specifications.freedesktop.org/secret-service/latest/prompts.html) says dismissal cancels the requested operation.
The relevant implementation is [the pinned helper](https://github.com/zalando/go-keyring/blob/v0.2.8/secret_service/secret_service.go).

A disposable private D-Bus service reproduces a successful library `Set` followed by `ErrNotFound`, and a successful library `Delete` followed by the unchanged value, after dismissed prompts.
Accepted write/read/delete controls work on the same fixture.
The fixture also demonstrates that changing the default collection produces `ErrNotFound` while the original collection retains the value.
This is protocol fault-injection evidence using the actual pinned client library, not a native keychain or physical-erasure test.

## Decision

Keep the dependency and platform selection in ADR 0012.
Add verification in the Aeronautes keychain adapter before reporting a successful write.

1. Call the existing library Set with the intended encoded value.
2. If Set reports an error, return failure without publishing the new reference.
3. If Set reports success, read that exact reference through the existing adapter and require exact equality with the encoded value supplied to Set.
   Compare the string returned by the library without decoding or normalising it.
4. Missing, unreadable or different read-back is a write failure, with an error that includes neither the credential value nor its reference name.
   Do not delete the candidate as compensation: the existing durable prepared-work journal owns recovery of any partial effect.

Remove the reference name from the existing `KeychainStore.Get` decode error as a separate logical fix under the same ADR 0015 error-content rule.
Regressions must reject reference names and raw or encoded credential values in returned verification and decode errors.

Immutable publication must retain the previous configuration and credential when this verification fails.
The unused candidate stays tracked as unfinished work even when the library did not create it.
Creation fails without publishing a bookmark with an unreadable key.
The coordinator remains released throughout external keychain I/O.

Use the same exact write/read-back check for the random startup probe.
After its Delete reports success, require a subsequent probe lookup to return `ErrNotFound`; a retained value or another lookup error fails the health probe and preserves the existing visible session fallback.
Best-effort probe cleanup is limited to its unique synthetic reference and never authorises cleanup of a credential journal entry.

## Boundaries

Read-back strengthens write acknowledgement; it does not pin the original native store or make a sequence of OS calls transactional.
A later default change or native-store removal can still make a stored value unavailable.
The read-back can itself require an unlock prompt; dismissing that prompt is a failed write verification after the item may already exist, so retain the prepared record for that partial-effect case.
The startup absence check is a health observation through the current selector, not original-store absence proof for the cleanup journal.
Leave ordinary credential Delete and physical cleanup disabled at the publication layer until ADR 0015's native authority and live-reference requirements are met.
The underlying dismissed-delete defect remains a required native-adapter follow-up.
Do not choose the vault, replace the library, add a background collector or claim P10 completion in this slice.
The library's unbounded prompt waits and ignored caller contexts remain separate native-adapter work; a wrapper goroutine that abandons an ongoing write is not a cancellation solution.

## Implementation sequence and proof

1. First establish an isolated Linux D-Bus test package and fix the existing reference-bearing decode error, with its own local review.
   Its separate test binary contains no real-keychain tests because godbus caches `SessionBus` process-wide.
   `TestMain` starts a per-run private `dbus-daemon` and sets `DBUS_SESSION_BUS_ADDRESS` before any client call; each scenario asserts that address before acting.
   Use a short socket path in a per-run mode-0700 temporary directory, respecting the Unix socket path limit, and bound startup, subprocess waits and teardown.
   No test may fall back to the personal bus, and a daemon configuration without service activation prevents a missing fixture from launching a real keyring.
2. Add write/publication regressions using that private service, the real pinned client, synthetic values and acknowledged prompt completion.
   Exercise cancelled creation and replacement, wrong and failed read-back, successful binary values, and failed startup probe deletion.
   Prove the previous bookmark configuration and key remain usable and the replacement stays journalled after every failed replacement.
   Show the relevant assertions fail on the unchanged adapter before implementing verification.
   Then implement the narrow adapter verification and exact startup health check as its own reviewed logical fix.
   Keep the public interface, dependency version, native selectors and credential journal schema unchanged.
3. Run the new package in an explicit protocol-fixture step in the required `Go — fmt, vet, build, test (race)` job, installing `dbus` explicitly in that job.
   `make test-secret-service` sets `AERONAUTES_REQUIRE_SECRET_SERVICE_FIXTURE=1`, making a missing daemon fail the CI-required lane; optional local runs may report an explicit skip.
   Native keychain integration tests remain in their existing separate binary and are not satisfied by the synthetic service.
4. Run focused race tests, meaningful failure controls and the application verification required by the affected paths; record the results and remaining native proof limits.
   Obtain independent local approval before pushing, require all five green PR checks on the current base, merge normally, and verify master.

## Cost

Each credential write adds one keychain read, and startup adds one lookup after probe deletion.
The cross-platform read-back adds one `security` subprocess per credential write on macOS, even though the reproduced dismissal defect is in the Linux client.
Bookmark writes are infrequent; correctness takes priority over this extra call.
Measure or revisit latency with native platform evidence rather than claiming a synthetic fixture establishes prompt or desktop performance.

The [validation record](../experiments/2026-09-06-keychain-writes.md) distinguishes completed implementation steps from the remaining write, native authority and cleanup work.
