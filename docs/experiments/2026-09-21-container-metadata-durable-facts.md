# Required facts for container metadata writes

21 September 2026.
Source: master `930fbe22e5757c195d917964e56c76160ca380ed`, after the L09 container versioning preflight correction.
This is the container metadata follow-up within frozen finish-line item L04.
The contract in [ADR 0062](../adr/0062-container-metadata-durable-facts.md) is now implemented.
The reproduction and diagnosis below describe the stated source; the implementation follows them.

## Reproduction

A private Go overlay adds a test through `MutateService.UpdateContainer` using the existing application harness and mock Swift.
The source checkout remains clean.
Each case connects the fixture bookmark, installs a SQLite refusal trigger, updates one plain metadata key, counts store POSTs and reopens the same database to inspect `mutation_facts`.

| Refused record | Returned outcome | Error | POSTs | Facts after reopen |
| --- | --- | --- | --- | --- |
| Container intent insert | applied | nil | 1 | 0 |
| Container audit insert | applied | nil | 1 | 0 |

Both named tests fail their required-record assertions under Go 1.27.1 with the race detector and `-count=1`.
The intent trigger is never reached because this path has no intent write.
The audit trigger raises the intended SQLite abort, which the service logs and discards before returning success.
These observations establish the recording defect; the initial probe does not claim a real-Swift round trip, remote content comparison or crash durability.
Raw JSON output, the overlay, exact command and source/environment metadata are retained in the private evidence packet identified in the review request.

## Source diagnosis

The current order is writer admission, session lookup, local admission checks, container HEAD, possible POST, verification HEAD, best-effort audit, then the result.
`audit_events` has retention, so it cannot substitute for the required ledger even when the append succeeds.
`Session.UpdateContainer` drops its partial result on a failed verification read and returns a bare error after a failed POST.
The service treats both as failed and discards audit errors.
This source diagnosis motivated preserving unknown outcomes and observed transaction IDs.

## Implementation and maintained proof

The service reuses the existing identity-keyed ledger: intent before the adapter, outcome and audit in one required transaction, explicit residue after an uncertain or completed write whose record cannot commit.
It captures the account once for intent, audit and returned classification, and keeps the writer gate through the required record.
The adapter retains partial results and observed transaction IDs, distinguishes received HTTP refusals from unknown POST outcomes, and returns a container-specific unverified error when verification cannot complete.

`TestContainerMetadataFactsMock` and `TestContainerMetadataFactsSwift` each maintain 18 system-boundary cases.
Against the proposal's production source, 17 cases fail and the public-ACL refusal control passes on each provider; all 18 pass with the implementation.
The cases cover intent, outcome and audit refusal; applied, conflict, mismatch, failed and unverified outcomes; lost POST replies, failed verify reads, close/reopen by identity, audit retention, observed transaction IDs and an account switch while the write reply is held.
Raw HTTP snapshots check the exact intended metadata delta and unchanged modelled collateral, including object bytes and the secondary account.
The modern-mode case injects a preflight header because the mock does not implement modern versioning; the existing `TestVersioningPreflight` separately passes against actual modern-versioned Swift containers.
The mock container POST emits a transaction header, and the existing adapter test requires three non-empty observed IDs.
A separate system case removes both POST transaction headers and requires the returned result, fact and audit to contain only the two observed HEAD IDs.
`TestContainerMetadataDrainWaitsForOutcome` holds the database writer after intent and proves shutdown waits for the outcome transaction.
The private implementation packet retains source, commands, raw results, the initial failed fixture attempts, corrected before/after proof and controls for POST-before-intent, swallowed record errors and unknown-as-failed classification.
Its path and digest are recorded with the implementation review.

This is ordinary database reopen and a local owned Swift fixture, not crash, power-loss or representative-deployment proof.
The oracle models named container configuration and objects; it does not claim every Swift header family or account configuration.
The L09 concurrent-writer limitation, separate object attribution path, public-ACL policy and all named finish-line and release holds remain unchanged.

## Conflict scenario receipts

21 September 2026, following protected source `213728269e8d45167379e520b3c9534870a3503b`.
The original 18-case matrix on each provider is unchanged.
`TestContainerMetadataConflictReceiptMock` and `TestContainerMetadataConflictReceiptSwift` add top-level witnesses for the existing `MutateService.UpdateContainer/conflict` cell under [ADR 0013](../adr/0013-scenario-coverage-reconciliation.md).
Each owns its harness, one public-service invocation and its returned receipt in the same top-level test, without subtests.

The stale fingerprint returns conflict with a nil error after one observed target HEAD and zero POSTs on that instrumented primary target route.
The reply, mutation fact and audit event must preserve the actual non-empty HEAD transaction ID.
Distinct seeded metadata and object bytes make the primary target, same-named secondary target and primary collateral non-vacuous controls.
Raw HTTP snapshots require both owned namespaces to remain unchanged, with no allowed delta.
This does not observe every account request or state outside those namespaces.

The case requires one identity-bearing conflict fact and one audit event with the exact captured bookmark, operation, literal resource and observed IDs.
It closes the writer handle and reopens the same database, then requires the same fact by identity, unchanged fact cardinality and the complete original audit event, including its ID and timestamp.
Only after those assertions does the invocation-bound receipt emit its claim.
Failed or skipped claimants and later cleanup failures cannot earn a valid reconciled credit; the reporter still requires a terminally passing top-level test and expected package.
A passing parent cannot establish that a skipped child exercised an oracle, so these scenarios do not delegate their assertions or receipt to a child.

The private verification packet retains the focused provider streams, real producer exits, unchanged-matrix comparison and reporter results for the reviewed source.
No inventory requirement, reporting gate, production behaviour or existing test budget changes.
These are development receipts and ordinary database reopen evidence; native, representative-deployment, release and valuable-data acceptance remain separate.
