# ADR 0050 — prove a Swift fixture is writable before a test uses it

**Status:** Accepted and implemented in the harness.
**Scope:** The readiness gate the system tests and the pipeline apply to the Swift fixture before the first harness writes to it.
It changes no product source, no assertion, no deadline and no lane membership.

## Problem and oracle

The pipeline waits for the fixture by polling `/info` until the proxy answers, at `.github/workflows/ci.yml`.
The harness applies no readiness gate of its own: `Harness.account` authenticates and keeps the token and storage URL, and the first thing a test does with them is a raw container PUT through `Harness.Container`.

Those are different claims.
`/info` establishes that the proxy is serving.
A test needs something stronger: that an authenticated client can create a container on the account it was given.

The gap is observable rather than theorised.
In the [readiness record](../experiments/2026-09-14-swift-fixture-readiness.md), heavy-lane job 103928287854 of run 34829207753 recorded `Swift is up` at 09:41:15 and, at 09:42:14 on that same fixture, a raw container PUT answering `HTTP 404` for the first test to write.
Whatever caused that 404 — an account layer not yet usable is plausible and is **not** established — the run demonstrates the gate passing while a client write did not succeed.
A gate that proves the wrong property is worth correcting on that ground alone, independently of the failure that exposed it.

The oracle is what the harness itself already asserts: `rawPutQuery` fails closed on any non-2xx seed, with the recorded reason that an unchecked seed leaves an empty container and lets the matrix assert a completed copy against absent objects.
That refusal is correct and stays.
It is the reason the failure was legible rather than silent, and it is also why an unready fixture costs a test run rather than producing a false pass.

## Decision

1. **Gate on a client round trip, not on the proxy answering.**
   Before the first test writes, the harness authenticates and completes a container PUT followed by a container DELETE, and treats the fixture as ready only when both succeed.
2. **Gate both accounts.**
   The suite requires two accounts where `AERONAUTES_REQUIRE_TWO_ACCOUNTS` is set, and a fixture whose secondary account is not yet writable is not ready for a cross-account test.
   Where the secondary is legitimately absent the existing skip applies unchanged; readiness is not a new requirement for a one-account workstation.
3. **Retry to a deadline, then fail with what was observed.**
   A readiness gate that gives up silently, or that waits without bound, replaces one unhelpful failure with another.
   The deadline is thirty seconds, and the last status, body, error and attempt count are reported when it passes.
4. **Put the gate in the harness, not only in the pipeline.**
   A workstation run has the same fixture and the same first write, so the gate is `Harness.writableWithin`, called for every required account before the primary connection is made.
   The pipeline keeps its own wait, which remains useful as an early and cheap failure.
5. **Use a namespace the suite already owns.**
   The probe container is created and removed under the harness namespace, so a readiness probe cannot collide with a concurrent suite, and a probe that failed to remove its own container is caught by the residue sweep and by a named case.

## What this does not establish

It does not establish the cause of the 404 in the recorded run, and it must not be described as fixing it.
An account layer that was not yet usable is one explanation; an account reaped between runs, or a fault specific to the secondary account, are not excluded by anything measured here.

It does not prove the fixture stays writable.
The gate observes one round trip before the run, and a fixture that fails later fails the test that reaches it, as it does today.

It grants no scenario, audit, native-platform or valuable-data acceptance, and changes no finding disposition or release gate.
