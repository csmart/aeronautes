# Object metadata saves can change accounts in the audit trail

This is before evidence and the proposal in [ADR 0065](../adr/0065-object-metadata-account-attribution.md).
The production correction is not implemented at this proposal head.

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

## Proposed correction and proof

Capture the validated session and bookmark together at admission, then keep that bookmark through intent and the outcome/audit transaction.
Keep the existing write and record ordering, uncertainty, writer lifetime and bridge signature.
Maintain the caught case and control through the service on both owned providers, with independent remote state, reopened records and a compilable broken-attribution control.
The implementation must preserve the existing failed-intent, failed-outcome, unverified-write, retention and shutdown regressions.

## Evidence and limits

Private packet `~/code/.aeronautes-work/20260921-l11-object-attribution/` contains the exact probe, raw JSON stream, stderr and source/load metadata.
Its five-file `SHA256SUMS` has digest `a9a0a968a0e4a76f6d1551092b9c4d4904370c488ff601dbd63a103287641fce`.
The focused run took 4.97 seconds at load 4.12 initially and 3.87 finally on the shared host; other authors and the reviewer could run concurrently.
One named control passed and one named case failed; this was not a full suite or real Swift measurement.
No tracked production edit, new authority, native or release acceptance follows from this probe.
