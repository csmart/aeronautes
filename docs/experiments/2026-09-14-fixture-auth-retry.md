# Fixture authentication has no retry while the step after it does

14 September 2026, against `1e056c7488acf0e76799374a019639cf755e7ce2`.

## What this is and is not

This records an asymmetry in system-test fixture setup and the change that closes it.
It does **not** establish the cause of the failure that exposed it, and no claim here should be read as fixing that failure.

## The observation that exposed it

Run 34896846326, job `Swift tests — main`, package `internal/systemtest` completing in 748.24 seconds, so not a timeout:

```
--- FAIL: TestCopyInterruptionMock/cancel_after_copy
copy_interruption_test.go:357: second SAIO account unavailable but AERONAUTES_REQUIRE_TWO_ACCOUNTS is set:
  Get "http://127.0.0.1:45097/auth/v1.0": net/http: HTTP/1.x transport connection broken:
  http: CloseIdleConnections called
```

That is not an assertion about the product.
It failed inside harness construction, at `internal/systemtest/harness.go:162`, authenticating the secondary account.
The port is ephemeral, so the endpoint is the per-subtest `httptest` mock rather than the SAIO on 18080.

The failure is unreproduced.
Two authors ran `TestCopyInterruptionMock` five times each, on the suspect range and on the base without it, in isolation; it passed ten times out of ten.
The condition therefore needs the package's own concurrency, and nobody has reproduced it.

## The asymmetry, established by reading the source

`Harness.New` performs fixture setup in this order:

| line | step | retry |
| --- | --- | --- |
| 144 | `h.account` for the primary, then `t.Fatalf` on error | **none** |
| 154 | `h.account` for the secondary, then `t.Fatalf` or `t.Skipf` | **none** |
| 170, 174 | `h.writableWithin` for each account | **every 250 ms to a 30 s deadline** |

`writableWithin` retries on *any* outcome — a non-2xx status and a transport error alike — and only its deadline returns an error.
`h.account` makes two network calls, `oracle.Connect` at `harness.go:433` and `h.rawAuth` at `harness.go:446`, and neither retries: the first error returns.

So the later step tolerates a fixture that is slow or briefly refusing, and the step that must succeed before it fails on a single transient error.
That gap is worth closing on its own terms, independently of what caused the run above.

## What was ruled out

A shared transport being closed under an in-flight request was the first theory, and it does not hold.
Every `CloseIdleConnections` call site outside `third_party/` was inspected.
Each operates on a transport obtained by `http.DefaultTransport.(*http.Transport).Clone()`, on a transport constructed locally in the same function, or on a client the caller owns:
`internal/systemtest/oracle/oracle.go:57` closes a client created a few lines above it and only on the failure path, `oracle.go:85` closes the oracle's own client, `internal/transfer/rclone/gateway.go` closes a transport it built, and `internal/swiftcontrol/objectread_transport_test.go:62` closes a transport belonging to the session that test constructed.
Nothing mutates or closes the shared default transport.

## Measured: a definitive refusal is distinguishable from a transient one

Against the live fixture at `127.0.0.1:18080`:

| credentials | result |
| --- | --- |
| `test:tester` / `testing` | HTTP 200 |
| `nosuch:account` / `nokey` | HTTP 401 |
| `test:tester` / `wrongkey` | HTTP 401 |

Tempauth answers an absent account and a wrong key with 401, immediately.
That matters for the shape of the retry, below.

## Why a naive retry would be wrong

The secondary account is deliberately optional: a missing secondary is a `Skip` on a one-account workstation and a `Fatal` only when `AERONAUTES_REQUIRE_TWO_ACCOUNTS` is set.
Retrying every failure to a thirty second deadline would make a one-account workstation wait thirty seconds before each skip.
`internal/systemtest` constructs the harness from twenty-two call sites and far more subtests, so that is not a marginal cost.

The measurement above is what avoids it: a genuinely absent account answers 401 and can be refused at once.

That consequence was measured rather than reasoned about.
With `AERONAUTES_SAIO_USER2` pointed at an account that does not exist and `AERONAUTES_REQUIRE_TWO_ACCOUNTS` unset, `TestDelimiterMarkerHTTPRealSwift` skips in **0.20 seconds** against the live fixture, reporting `secondary account refused authentication with HTTP 401, which no retry changes`.
A retry that did not separate the two would have made that 30 seconds.

## The contract, as delivered

1. **Retry fixture authentication in `Harness.account`, not in `oracle.Connect`.**
   Eight call sites use `oracle.Connect`, and four of them authenticate against an `httptest` server — `internal/app/recovery_retention_test.go`, `internal/app/mirror_refusal_test.go`, `internal/transfer/copy/response_barrier_test.go` and `internal/transfer/local/upload_refusal_test.go`.
   For those an absent endpoint is the expected condition and must fail at once.
   Retrying inside `Connect` would change behaviour for every caller and slow each of them; retrying in `account` confines it to fixture setup, which is the thing being made resilient.

2. **Retry only what can be transient, and refuse what is definitive.**
   A transport error, a 5xx, or any other non-2xx retries to the same thirty second deadline the readiness gate uses.
   A 401 or 403 returns immediately, because the fixture has answered and its answer is that the credentials do not name a usable account.
   This preserves the one-account skip at its current speed.

3. **Say when setup was retried.**
   An error after the deadline names the account, the deadline, the attempt count and the last failure, as `writableWithin` already does.
   A success that needed more than one attempt is reported through `t.Logf` with the attempt count, so a fixture that authenticates only on the fourth try is visible in the run rather than indistinguishable from one that answered first time.

## What a retry can hide, and what stops it hiding this

A retry converts a fixture that is unhealthy-but-eventually-working into a run that looks clean.
That is a real cost and it is the reason for point 3: the attempt count is reported on success, so the evidence of an unhealthy fixture survives into the log instead of being swallowed.

A reader should not treat a retried setup as a healthy one.
The retry exists so that a transient failure costs 250 milliseconds instead of a whole pipeline run; it is not evidence that the fixture is sound, and the record of attempts is what distinguishes the two.

This also does not make the observed failure impossible.
If the condition is a connection being broken repeatedly rather than once, the retry will exhaust its deadline and the test will still fail — with a better diagnosis than a single-shot error gives.

## Controls, and two requirements a guard producer must meet

Five controls hold this, each failing its own named case: the retry removed, a credential refusal retried, the attempt count pinned to one, the deadline reaching its branch without reporting, and the oracle not recorded on success.
A retained self-test inserts a deliberate syntax error and confirms the producer reports a build failure rather than crediting a control that never ran.

Three of those controls first failed for reasons that were not their mutation, and two of the three are requirements rather than anecdotes.

**A control that hangs is not a catch, and a producer must record it as its own outcome.**
One mutation pushed the deadline an hour out. The case then retried until the subprocess timeout, which cost ten minutes and produced no named failure.
Had the producer treated a timeout as a catch it would have reported success for a control that never demonstrated anything, and had it crashed on the timeout it would have lost the whole run.
The producer records `TIMED_OUT`, credits nothing for it, and the control was retargeted to break the deadline's *report* so it fails in under a second.

**A producer's all-caught summary must compare the expected set against the credited set, never filter on a field that failing entries do not carry.**
This producer first computed `all_caught` by filtering results on a key that only successfully-run controls carry, so entries recorded as `BUILD_FAILED` were skipped by the filter entirely.
It reported `all_caught=True` while two of five controls had not run at all.
That is worse than losing a result: it fabricates confidence, and the number it prints is the number a reader trusts instead of reading the log.
It now compares the set of controls expected to catch against the set actually credited, and names the difference.

Two smaller faults of the same family, where a name said something the value did not: two mutations rewrote the deadline test in a way that left `deadline` declared and unused, so they failed to compile rather than testing the retry; and the test fixture's counter counted HTTP requests while being named `attempts`, when one attempt makes two requests, so the first expectations were wrong against correct code.

## A count corrected in review

This record first said nine `oracle.Connect` call sites. There are eight.
The miscount came from a search whose second alternative matched the function's own declaration, so the definition was counted as a use of itself.
The four `httptest` sites and the conclusion drawn from them are unaffected, but a count offered as evidence should be produced by a search that cannot match the thing being counted.

## Scope

Fixture setup in `internal/systemtest` only.
No product source, no assertion, no deadline belonging to a test, no lane membership, and no change to `oracle.Connect` or any of its other callers.
The one-account `Skip` and the `AERONAUTES_REQUIRE_TWO_ACCOUNTS` `Fatal` keep their existing meanings.
