# Active-job budget feedback

The UI-10 oracle requires honest settings observations and attempted-write outcomes.
DESIGN §§38/40 retain the complete four-field settings document, profile policy and choices 1, 2, 4 and 8.
A rejected Set reply cannot establish whether that write took effect.

## Behaviour

BrowserState owns one synchronous Get/Set operation until its awaited reply settles.
Writes require a current successful observation and cannot overlap reads or other writes.
A failed read retains explicitly stale data or shows a cold unavailable state.
A rejected Set retains its attempted budget and unconfirmed outcome; the component catches the rejection.
There is no automatic retry or inferred rollback.

An explicit successful Get is required before a new choice after an uncertain write.
It establishes the current observation and reconciles the select even when the value equals the old cache, without retrospectively acknowledging Set.
Failed recovery reads preserve both the unconfirmed attempt and stale qualifier.
Only a successful Set acknowledgement produces saved feedback.
Pending and unconfirmed state survives job polling, tab changes, drawer collapse and remount.

## Verification and limits

The original probe on `4f9e356d` recorded five failures and two controls, including unhandled rejected callbacks and duplicate DOM admission.
Its v2 follow-up exposed the same-value readback mismatch after an assertion had prevented v1 from reaching later observations.
Contract `d3adfb8c` preceded correction `902ee324`.
The final selection passed 194 distinct cases across nine files, including 21 dedicated state/mounted cases, with no skips or unhandled rejections.
Frontend check and build passed.

All eight final omission controls exited 1; the callback control had passing assertions but two unhandled rejections, so its JSON case-success flag does not make the process successful.
Initial missing-state assertions are not independently reproduced historical defects.
The packet retains both omission rounds, the failed recovery-feedback regression, all 24 command runs and parser corrections.
Raw folders are `aeronautes-ui10-budget-before-4f9e356.xBhGvABS` and `aeronautes-budget-feedback-author.i7lexb9r`.

The injected API exercises UI ownership and display, without actual settings persistence, scheduler effects, native accessibility or full-repository acceptance.
UI-10/P28, protected delivery and the valuable-data hold remain open.

## Evidence retention

The original record, probes, structured evidence, failed runs and raw verification outputs are retained in the private packet `/home/csmart/code/.aeronautes-work/evidence/20260913-service-records`.
Its `SHA256SUMS` digest is `d0ce6823082a0aab3ddb7f0b563ccc4028aba68c6471b124cad41c925650ae99`.
`original/docs/experiments/` preserves the record and probe paths at checkpoint `860ddec9ce72c241f349f491ca3b63dbab8ed5ce`; `INDEX.json` maps the raw packets and lists excluded dependency/cache directories.
Historical receipts remain unchanged; this archive does not claim to retain those excluded execution inputs or constitute a hermetic environment.
The maintained regression tests remain in the repository.
