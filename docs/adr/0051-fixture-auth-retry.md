# ADR 0051 — retry fixture authentication as the readiness gate already retries the write

**Status:** Accepted. Implemented in the range that carries this record.
**Scope:** Authentication of a Swift account during `internal/systemtest` harness construction.
It changes no product source, no assertion, no test deadline and no lane membership, and it does not alter `oracle.Connect` or any caller of it outside the harness.

## Problem and oracle

[ADR 0050](0050-swift-fixture-readiness.md) made the harness prove a fixture is writable before a test uses it, by retrying a container PUT and DELETE every 250 milliseconds to a thirty second deadline on any outcome.
The two network calls that must succeed *before* that gate — `oracle.Connect` and `Harness.rawAuth`, both inside `Harness.account` — retry not at all.
The first error returns, and the caller turns it into `t.Fatalf` for the primary account, or `t.Fatalf` under `AERONAUTES_REQUIRE_TWO_ACCOUNTS` and `t.Skipf` otherwise for the secondary.

So one step of fixture setup tolerates a fixture that is briefly refusing and the step before it does not.
The [authentication retry record](../experiments/2026-09-14-fixture-auth-retry.md) shows the ordering in `Harness.New` and the retry behaviour of each step.

The oracle is ADR 0050's own reasoning applied one step earlier.
That record argued a gate should prove what a test actually needs rather than a weaker adjacent property, and that an unready fixture should cost a bounded wait rather than a run.
Authentication is the same kind of setup step against the same fixture, and nothing distinguishes it except that nobody has needed it to be resilient yet.

A run exposed the gap: job `Swift tests — main` of run 34896846326 failed in harness construction on a transport error authenticating the secondary account.
That failure is **unreproduced** — ten isolated runs across two ranges passed — and its cause is **not established**.
This decision does not claim to fix it and must not be cited as having done so.
The gap stands on the asymmetry, which is visible in the source without reference to any failure.

## Decision

1. **The retry lives in `Harness.account`, not in `oracle.Connect`.**
   Four of the eight `oracle.Connect` call sites authenticate against an `httptest` server, where an absent endpoint is the expected condition and must fail at once.
   Confining the retry to the harness changes fixture setup and nothing else.

2. **Transient failures retry; definitive refusals do not.**
   A transport error, a 5xx, or any other non-2xx retries every 250 milliseconds to the same thirty second deadline the readiness gate uses.
   A 401 or 403 returns immediately: the fixture has answered, and its answer is that these credentials do not name a usable account.

3. **The one-account workstation keeps its immediate skip.**
   Tempauth answers an absent account with 401, measured in the record, so decision 2 refuses it on the first attempt.
   Without that split, a missing secondary would cost thirty seconds at each of the harness's twenty-two construction sites before skipping.

4. **A retried success is reported, not swallowed.**
   Exhausting the deadline returns an error naming the account, the deadline, the attempt count and the last failure.
   Succeeding after more than one attempt logs the attempt count, which `Harness.account` does from the count `authenticateWithin` returns.
   A fixture that authenticates only on the fourth try is then visible in the run.

5. **Nothing else changes.**
   The `Skip` and `Fatal` paths keep their conditions and their wording, `secondaryReachable` is untouched, and no assertion is relaxed.

## Consequences

A transient failure in fixture authentication costs 250 milliseconds rather than a pipeline run.
A fixture that is genuinely unavailable still fails, later by at most the deadline, with a better diagnosis than a single-shot error carries.

A retry can make an unhealthy fixture look like a clean run, which is the cost of this decision.
Decision 4 is what keeps that visible: the attempt count survives into the log on success, so a reader can tell a fixture that answered first time from one that needed four tries.
A retried setup is not evidence of a healthy fixture and the record says so.

This does not make the observed failure impossible.
A connection broken repeatedly rather than once will exhaust the deadline and fail the test, which is the correct outcome.

It grants no scenario, audit, native-platform or valuable-data acceptance, closes no finding, and leaves the valuable-data release hold unchanged.
