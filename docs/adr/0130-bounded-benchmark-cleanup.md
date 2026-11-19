# ADR 0130: bounded benchmark cleanup

Status: Accepted
Date: 2026-11-20

## Context

The benchmark is the last production caller of the general `internal/deletion` executor.
Its production workload creates 200 names, all generated locally before use; the private test scale only needs smaller workloads.
Product deletion jobs already own their separate durable intent and item records under ADR 0122.
Mirror retains its comparison planning and refuses execution; it does not call this executor.

ADR 0036 made an unbounded namespace safe to account for using a private SQLite index.
It expressly made that index working state, not a restart ledger or deletion authority.
Maintaining sweeps, sampled verification, configurable strategies and disk-backed identity facets solely for this small benchmark obscures its real ownership and failure paths.
A new cleanup invocation also forgets which names earlier phases already requested.

## Decision

Keep the consent, capability discovery, connection pins, measured phases and per-bookmark recommendations.
Keep the production workload bounded to 200 generated names; smaller private test workloads do not create a public setting.
Use one bounded entry per generated name to record whether its PUT and DELETE were issued and what the DELETE answered.
Record issuance before the request and join workers before reading their results.
Each worker owns one distinct entry at a time.
No deletion name comes from a listing, and no name whose DELETE was issued is requested again, including during cleanup.
The existing listing and HEAD phases remain measurements, never deletion authority or post-delete verification.

Native deletion removes only the named object, without following a manifest's segments.
A successful response or not-found settles that request for cleanup purposes.
A reported error leaves its effect unresolved; it is not evidence that the object survived.
Bulk requests stay within the advertised limit and the existing 1,000-name ceiling, and use `swiftcontrol.BulkDelete` validation.
An invalid or unavailable bulk answer leaves every issued name in that batch unresolved.
A valid partial answer retains its reported failure names as unresolved and may credit only its validated aggregate.
An incomplete phase supplies neither a successful throughput measurement nor a recommendation.

Before deletion has failed, an ordinary phase failure or cancellation may clean up the generated names whose PUT was issued, using bounded native workers and the same issuance record.
A failed PUT may have taken effect, so cleanup includes that attempted name.
Once a deletion outcome is unresolved, stop further deletion, retain the container and report the incomplete cleanup.
Already admitted workers must join and retain their returned outcomes.
Remove the container only after every attempted PUT name has a settled deletion answer.
An unexpected object makes container removal fail; it is never adopted into cleanup scope.
Report container cleanup failures without creating a new sweep or retry.

Retain the exact unresolved names and available local error details with the existing checked private JSONL writer and its `name`/`detail` schema.
Report an evidence open, encode, flush or close failure explicitly, without claiming that the complete list was saved.
These bounded, known unresolved identities differ from lost accounting: preserve the historical `deletionAccountingIncomplete` field and its readers, but do not set it merely because the service reported an error.
Finish through the existing atomic `jobs.Finish` transaction for state, counters, reason and audit.
A failed completion transaction still makes public status unavailable through the store latch.

This replaces ADR 0036's private index and executor requirements for the benchmark only.
It does not weaken product deletion records, historical evidence, retained-list failure semantics, segment ownership, versioning checks or the refusal to execute Mirror.
It adds no automatic restart or retry protocol and makes no crash-durability claim for the bounded working entries.

## Delivery and verification

Review this contract before its implementation.
The existing executor remains until the replacement and its consumers have passed independent review.
Move durable bulk reply validation, identity encoding, malformed-row evidence and real-Swift effect witnesses to direct adapter tests.
Do not reproduce the removed accounting engine in test helpers.
Replace index and sweep implementation tests with benchmark contracts for healthy bulk and native fallback, incomplete answers, cancellation, once-only issuance, unrelated-name preservation, evidence-write failure and atomic completion.
Preserve negative controls and run race tests, owned real-Swift checks, the full suite and the required PR and resulting-master pipelines.

Only then remove the unused executor, private index, options, snapshots and their implementation-specific tests.
The benchmark remains deferred by its existing release gate; this refactor does not enable it or release 0.5.0.

The replacement is implemented in `internal/app/benchmarkservice.go`.
Bulk protocol and effect contracts live beside the adapter in `internal/swiftcontrol/bulk_contract_test.go`.
The real-Swift integration checks call the supported deletion primitives directly; they do not certify Mirror execution.
The former `internal/deletion` package has no remaining caller and is removed.
