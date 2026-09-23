# Object metadata saves can change accounts in the audit trail

The before evidence below motivated the implemented contract in [ADR 0065](../adr/0065-object-metadata-account-attribution.md).
The reproduction describes the stated source; the implementation and maintained regressions follow it.

## Reproduction

Source is `e4b7730721adc2beea38c0e22257325a352e9132`, with tracked files unchanged and one private focused test added.
Two independent mock accounts hold the same object name.
An object metadata save on the first account reaches a barrier after its POST; the application connects to the second account before releasing that response.
The call returns applied, independent HEADs confirm the first object's metadata changed and the second object's metadata did not, and reopening the database finds an applied fact for the first bookmark.
The raw audit event instead records the second bookmark.
The audit store is read directly, so the separate Audit.Search projection defect cannot explain this failure.

The same-account control uses the same barrier and assertions and passes.
The race-enabled probe compiles and fails at the named audit-account assertion in the connection-switch case.
This establishes the later audit relabelling; it does not establish a particular scheduling occurrence between session capture and intent.

## Implementation and maintained proof

The service captures the validated session and bookmark together at admission, then keeps that bookmark through intent and the outcome/audit transaction.
It retains the existing write and record ordering, uncertainty, writer lifetime and bridge signature.
`TestObjectMetadataAccountMock` and `TestObjectMetadataAccountSwift` each cover the same-account control, a switch after POST, a preflight conflict, a lost POST response and both required outcome/audit record refusals.
Both accounts have the same resource names; independent remote snapshots verify the first account's intended delta, unchanged content and unchanged collateral on both accounts.
The intent and reopened outcome must keep the same identity and first bookmark, and the raw audit must agree with that fact.
The maintained before run catches three account-attribution failures on each provider, with the same-account and two record-refusal controls passing on each.
The first focused run with the correction passes all twelve leaf cases.
Restoring the later active bookmark only in the outcome audit produces the same six named failures after a successful test compilation, while all six controls pass.
The guard producer separately rejects a deliberate syntax error as a build failure and grants it no assertion credit.
Existing failed-intent, failed-outcome, unverified-write, retention and shutdown regressions remain unchanged.

## Evidence and limits

Private packet `~/code/.aeronautes-work/20260921-l11-object-attribution/` contains the exact probe, raw JSON stream, stderr and source/load metadata.
Its five-file `SHA256SUMS` has digest `a9a0a968a0e4a76f6d1551092b9c4d4904370c488ff601dbd63a103287641fce`.
The focused run took 4.97 seconds at load 4.12 initially and 3.87 finally on the shared host; other authors and the reviewer could run concurrently.
One named control passed and one named case failed; this was not a full suite or real Swift measurement.
The initial private probe made no tracked production edit.
Its packet remains unchanged; `maintained-before/` separately retains the twelve-case before run, source copy and metadata at proposal19a98cae.
No new authority, native or release acceptance follows from these observations.
Caller-named mutation binding, deferred create/marker recording and historical connection-generation provenance remain open.

## Follow-up, 23 September 2026: the creation paths

Container and marker creation, which the 22 and 23 September decisions returned to v1, now capture the admitted account once as the metadata writes do: they write under the captured session and record under the captured bookmark (`MutateService.recordFor`), so the relabel window this record named for the create paths is closed for them; the deferred deletions still record under the active connection.
`internal/systemtest/creation_account_test.go` (`TestCreationAccountMock`, `TestCreationAccountSwift`) holds the creating reply (the container PUT, or the marker's verify read, because the marker PUT carries an Expect header the barrier cannot hold), connects to the other account meanwhile, and requires the audit event to name the creating account with nothing changed in the other account.
Caller-named mutation binding, which needs the caller to name the account on the bridge, remains open.
