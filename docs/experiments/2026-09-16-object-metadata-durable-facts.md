# Object metadata writes: durable facts are not required

16 September 2026.
Finish-line item L04, the portion v1 keeps for object metadata: P12 required facts durable before the effect, and P13 identity-keyed outcome across restart.
Download terminal-write faults are already under ADR 0024/0033 and are not re-examined.
Container metadata is owned by L09 on an unmerged branch and is not examined here.
The correction in [ADR 0058](../adr/0058-object-metadata-durable-facts.md) is delivered for object metadata writes.

Source: GitHub master `e770f8a98c8ad67bc70e2137f66a598271be8195`, which already carries the L08 writer gate.

## What the path does today

`MutateService.UpdateObjectMetadata` occupies the writer gate, obtains a session, and calls `Session.UpdateObjectMetadata`.
That adapter HEADs, may POST, then HEADs to verify.
On return the service calls `record`, which `audit.Append`s and logs a failure without returning it.
A completed save then returns the DTO and a nil error.

So the current order is occupy → HEAD → POST → verify → best-effort audit → applied/nil.
L08 keeps the database open for that append; the append is still optional.
There is no intent row, and there is no identity that a later process can look up.

`record` says a mutation that cannot be accounted for is loud in the log, but the mutation's own result is preserved.
That is the opposite of P12's required-record rule.

`audit_events` compaction deletes oldest rows on every append, and `Prune` deletes by time.
Those rows are not a ledger.

## What a verify-read failure does

After a successful POST, a failed verify HEAD returns `nil, error` from the adapter, discarding the result that already holds the write transaction id.
The service then records `failed` and returns that error.
The write has happened; `failed` claims it did not, or that it is known not to have applied.
That is an unknown/unverified write, not a failed one.

## What P12 and P13 require of this path

P12: required facts are durable before the effect; a SQL abort of the intent must issue no store POST.
P13: the outcome is keyed by identity and survives reopen; POST-then-audit-fail is residue, not applied; verify-read failure after POST is unknown/unverified, not failed.

None of those hold.
A POST can complete with no durable identity at all.
A failed append still reports success.
A verify-read failure is stored as `failed` when it is stored at all, and it is stored only best-effort.

## Correction

Persist an intent (identity, bookmark, operation, resource) before the session can POST.
SQL abort of that insert issues zero POSTs and commits no fact.
After the store attempt, persist the outcome and the audit event in one transaction, and surface that error so the call cannot return applied/nil.
POST succeeded and that transaction failed: named residue error; remote metadata is an independent snapshot; reopen of that identity is not applied.
Verify-read failure after POST records `unverified`, not `failed`.
A POST whose request left and whose store status never arrived is also `unverified`; a received 403 or 404 is `failed`.
ADR 0046 still qualifies a completed POST as not establishing exclusivity; decision 4 only changes the verify-read-failure classification from `failed` to unknown/unverified.
The ledger is a new `mutation_facts` table that retention cannot drop.
The existing writer gate stays occupied until after that final transaction.
Create, marker and manifest stay on today's gate plus best-effort audit.

## Proofs carried by the implementation

- Abort the intent insert: zero POST in the store log, empty facts.
- Abort the outcome transaction after POST: residue error, reopen identity not applied.
- Applied, conflict and verify_mismatch survive close and reopen of the same database.
- Existing L08 writer-gate tests still pass.
- Mutants: swallowing `Append`, or issuing POST before the intent insert, must fail those new tests.

## Limits

This does not address crash or kill, fsync, window-close, segments, deletion, copy, upload, or marker/create/manifest as v1.
It does not edit the writer gate, the drain order, `ShutdownWork`, or job Finish/item recorders.
It does not close L04, L08, P12, P13, P28 or the valuable-data hold.
