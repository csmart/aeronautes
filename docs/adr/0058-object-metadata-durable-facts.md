# ADR 0058: identity-keyed durable facts for object metadata writes

**Status:** Proposed. Contract only; nothing in it is implemented yet.
This does not close L04, L08, P12, P13, P28 or the valuable-data hold.

## Context

Finish-line item L04 asks for P12/P13 remaining durable effects and complete outcomes on the mutation service.
v1 keeps object and container metadata writes; download terminal-write faults are already under [ADR 0024](0024-local-job-completion.md) and [ADR 0033](0033-download-item-recording.md).
This slice is object metadata only.
L09 owns `containerupdate.go` on `versioning_preflight` and is not merged, so container metadata is the follow-up after that merge.

`MutateService.UpdateObjectMetadata` occupies the [ADR 0053](0053-synchronous-writer-shutdown-ownership.md) writer gate, obtains a session, calls `Session.UpdateObjectMetadata`, then `audit.Append`.
The append's error is logged and discarded, and a completed save returns `applied` with a nil error even when that append fails.
L08 keeps the database open for that best-effort append; it does not make the audit required, and it does not persist an intent before the store POST.

`audit_events` is a product trail with a retention cap (§51).
Compaction deletes the oldest rows on every append.
That table cannot be the ledger of required facts.

`Session.UpdateObjectMetadata` issues HEAD, maybe POST, then a verify HEAD.
A verify-read failure after a successful POST returns `nil, error` and drops the write transaction id.
The service then records `failed`, which is false: the write is unknown, not known not to have happened.

The [object metadata durable-facts record](../experiments/2026-09-16-object-metadata-durable-facts.md) states this from the current source.

## Decision

Object `UpdateObjectMetadata` records identity-keyed facts that outlive a process, and it does not POST until the intent is durable.

1. Persist an intent before the session can POST.
   The intent carries a unique identity, the bookmark, the operation `object.metadata.update`, and the resource `container/name`.
   A SQL abort of that intent issues zero store POSTs and leaves no committed fact.

2. After the store attempt, persist the outcome and the audit event in one write transaction.
   `record` surfaces that transaction's error.
   The call never returns `applied` with a nil error if that transaction fails.

3. A POST that succeeded, followed by a failed outcome transaction, is residue.
   The live call returns a named residue error that includes the identity.
   Remote metadata is an independent snapshot and is not rolled back.
   Reopening the same database, that identity is not `applied`.

4. A verify-read failure after a POST is an unknown/unverified write, not `failed`.
   The adapter must return enough of the write (at least that the POST happened, and the write transaction id) that the fact can be stored as `unverified`.
   The live call returns a named error, not `applied` and not a successful DTO.

5. The ledger is a new `mutation_facts` table, migration `0011` after `0010_transfer_items.sql`.
   Audit retention, audit compaction and terminal-job pruning cannot delete those rows.
   Compacted `audit_events` remain the §50 trail, not the required fact.

6. The existing writer gate stays occupied until after the outcome transaction.
   This record does not edit `writer_gate.go`, the `ShutdownWork` drain order, `main.go`'s `ShutdownWork` call, or job Finish/item recorders.

7. `CreateContainer`, `CreateMarker`, `DeleteMarker` and `DeleteManifest` stay on today's gate plus best-effort audit.
   `UpdateContainer` is unchanged in this slice.

Committed outcomes are `intent`, `applied`, `conflict`, `verify_mismatch`, `failed` and `unverified`.
`intent` means no outcome has been committed; it is not `applied`.
A pre-POST failure that never issued a POST records `failed` or `conflict` in the outcome transaction.
A POST whose effect is not proven records `unverified` rather than `failed`.

The identity is unique per attempt, generated before the intent insert, and is the lookup key across reopen.
Bookmark, operation and resource are attributes of that identity, not a replacement for it: sequential writes to the same object are distinct facts.

## Consequences

An operator whose intent cannot be recorded does not change the object.
An operator whose POST lands and whose outcome cannot be recorded is told the write is residue, and a later reopen of that identity does not report success.
Applied, conflict and verify-mismatch facts survive reopen of the same file.
The writer gate still delays shutdown through the required record, which L08 already owns.

This does not address crash or kill, fsync or power-loss, window-close, segments, deletion, copy, upload, or marker/create/manifest as v1 capabilities.
Container metadata is the follow-up after L09 merges.
Download terminal writes stay on ADR 0024/0033.
L04, L08, P12, P13 and P28 stay open, and the valuable-data hold is unchanged.
