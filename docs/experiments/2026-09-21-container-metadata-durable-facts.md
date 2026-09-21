# Container metadata can succeed without its required facts

21 September 2026.
Source: master `930fbe22e5757c195d917964e56c76160ca380ed`, after the L09 container versioning preflight correction.
This is the container metadata follow-up within frozen finish-line item L04.
The contract in [ADR 0062](../adr/0062-container-metadata-durable-facts.md) is proposed, not implemented.

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
This source diagnosis motivates preserving unknown outcomes and observed transaction IDs; those fault cases still require maintained implementation regressions.

## Planned correction and limits

Reuse the existing identity-keyed ledger: intent before the adapter, outcome and audit in one required transaction, explicit residue after an uncertain or completed write whose record cannot commit.
Capture account attribution once, keep the existing writer gate, and preserve the L09 preflight refusal and concurrent-writer limitation.
The implementation must carry independent remote snapshots, reopening proofs and focused broken-behaviour controls before delivery.
No code correction or release clearance is claimed by this record.
