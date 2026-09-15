# ADR 0055: bounded native credential operations

**Status:** Accepted for the Linux Secret Service adapter wait bound.
Native acceptance on the six targets remains separately required. L01 does not close.

## Context

`internal/secrets.KeychainStore` is the OS-keychain adapter (§12.1, ADR 0012).
Its three methods accept a `context.Context` and discard it: each signature reads `_ context.Context`, and the pinned `github.com/zalando/go-keyring` v0.2.8 exposes `Set`, `Get`, `Delete` and `DeleteAll` with no context-aware form.
A Secret Service that accepts a call and holds the reply therefore blocks the caller for as long as it likes, and cancelling the caller's context has no effect.

The tree already records the symptom without naming the cause.
`internal/secrets/protocoltest/main_linux_test.go` guards its own binary with a forty-five second timer and `os.Exit`, commented "the pinned client's prompt wait has no context", so a broken fixture aborts the run rather than hanging the suite.
Production has no equivalent guard.

The reproduction is archived with this record.
Against the private Secret Service fixture, with a reply held open, three cases fail and two controls pass in the same run:

- a cancelled credential write does not return;
- a cancelled credential read does not return;
- `OpenDefault` does not settle, so the backend is never selected.

The third is the most consequential.
`main.go` calls `secrets.OpenDefault` during startup, and `NewKeychainStore` probes the keychain with a write, a read and a delete before the shell exists.
§12.1 promises a visible fallback to the session store when the keychain does not work — "never silently" — and a held reply prevents that fallback from ever being chosen, because the probe that would reject the keychain does not return.
An operator whose Secret Service is present but wedged gets no window, no warning and no way to cancel.

## Decision

Bound the *wait*, at the adapter, and say plainly that bounding a wait is not cancelling the operation.

1. Every `KeychainStore` method honours the context it is given.
   When that context is done before the native call returns, the method returns and the caller is released.

2. The native call is not cancelled, because the pinned client offers no way to cancel it.
   The abandoned call continues until the Secret Service answers or the process exits.
   It owns its own values and writes to nothing the caller can still observe.

3. An abandoned **read** returns the context's error and no value.
   A read has no effect, so abandoning one asserts nothing false.

4. An abandoned **write or delete** returns an error that says the effect is unresolved, and it must not be reported as "did not happen".
   The Secret Service may complete the operation after the caller has given up.
   This matches the existing save path, which already tells the operator that "an unused key may remain in credential storage" when a write fails.

5. Backend selection is bounded by a **ten-second** deadline rather than by a caller's context, because `OpenDefault` has no caller context and runs before the shell.
   Ten seconds is an order of magnitude above a healthy Secret Service delete-get-delete round trip, and short enough that a wedged service does not look like a hung launch.
   It is not the protocol-test binary's forty-five second abort, which exists only to stop a prompt wait from hanging the suite.
   A probe that does not answer within ten seconds selects the session store, which is the visible fallback §12.1 already requires.
   The probe entry it may have written is not treated as removed.

## What this does not establish

It does not cancel a native operation, and it does not bound the memory or handle held by an abandoned call beyond the lifetime of the process.

It does not establish native keychain behaviour on the six targets; that acceptance remains separately required, and this record's evidence is the Linux Secret Service fixture only.

It does not close L01.
Verified persistent-backend selection and physical cleanup and retry are untouched, and no key is removed from any store by this decision.

It grants no release acceptance, and the valuable-data hold is unchanged.

## Caller context

No backstop deadline applies to `Get`, `Put` or `Delete` when the caller supplies a context.
A legitimate native prompt can take as long as an operator takes to type a password, and a fixed ceiling would abandon a working interaction.
The caller's context is the mechanism; the startup probe is the one place a deadline is unavoidable because there is no caller.

`Connections.activeSnapshot` passes `context.Background()` into `guardCache`.
That path runs `bookmarks.Store.WithCurrentGeneration`, which reads the SQLite generation row and does not call the secret store.
It is not a hole in the caller-context mechanism.
