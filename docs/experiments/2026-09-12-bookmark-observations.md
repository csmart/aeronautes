# Bookmark observation outcomes

The UI-10 oracle distinguishes a Save acknowledgement from a later List observation.
A rejected or uncertain Save remains a rejection; a failed List cannot erase an acknowledged bookmark ID or justify repeating an empty-ID creation.

## Behaviour

The rail distinguishes not loaded, loading, current empty/nonempty, unavailable and explicitly stale observations, using fixed safe errors and a read-only List retry.
Retry admits one read synchronously; Add remains available.
Only the latest List generation can publish success, failure or finalisation.
Save/Delete admission and settlement retire obsolete reads, including reads started during a mutation and uncertain mutation outcomes.

Successful Save retains its returned DTO if the existing awaited List fails.
Successful Delete removes only its acknowledged ID.
Actual mutation errors, arguments, authority checks, credential refresh, connection/selection revisions and existing awaits remain unchanged.
Save-and-connect uses the acknowledged ID only after the existing List await and current form/connection-scope check.
The editor and mutation policy are unchanged.

## Verification and limits

The exact ten-case before probe on `4f9e356d` returned exit 1 with six intended failures and four controls.
It reproduced duplicate empty-ID creation after acknowledged Save plus failed List, misleading startup emptiness and obsolete List replies overwriting acknowledged effects.
Contract `15255cd2` preceded correction `0066181d`.
The final focused selection passed 450 distinct cases: 45 dedicated and 405 unchanged neighbours; the original ten cases, frontend check and build also passed.
Ten private broken variants returned exit 1 with 12 targeted failures.

The packet retains the original failing draft, callback-await test correction, type-check failures, duplicate-admission proof and expected Delete-rejection fixture correction.
Raw folders are `aeronautes-bookmark-observation-before-hr_yd7nf` and `aeronautes-bookmark-verification-q3woru50`.
These are state/component results, without native interaction, secret-store lifecycle, deployment or full-repository acceptance.
UI-10/P28, protected delivery and the valuable-data hold remain open.

## Evidence retention

The original record, probes, structured evidence, failed runs and raw verification outputs are retained in the private packet `/home/csmart/code/.aeronautes-work/evidence/20260913-service-records`.
Its `SHA256SUMS` digest is `d0ce6823082a0aab3ddb7f0b563ccc4028aba68c6471b124cad41c925650ae99`.
`original/docs/experiments/` preserves the record and probe paths at checkpoint `860ddec9ce72c241f349f491ca3b63dbab8ed5ce`; `INDEX.json` maps the raw packets and lists excluded dependency/cache directories.
Historical receipts remain unchanged; this archive does not claim to retain those excluded execution inputs or constitute a hermetic environment.
The maintained regression tests remain in the repository.
