# Job operations and reported deletion counts

The UI-09/P28 oracle is DESIGN §§47/49, K-UI-005, the actual Go counter writers and the drawer's existing incomplete-accounting and diagnostic behaviour.
Active diagnostics and the real Delete numerator already worked before this correction.

## Behaviour

Rows now display their actual operation: Delete prefix, Empty container, Delete container, Copy, Upload or Download.
Other nonempty stored types remain escaped literal text; an empty type displays Unknown operation.
The labels describe the operation without claiming an effect.

For the three actual Delete modes, the existing `objectsCopied` numerator and optional positive `objectsPlanned` denominator now say objects reported deleted, with no copied-byte suffix.
Mirror's separate `objectsDeleted` field never replaces that numerator.
Incomplete-accounting wording, failures/refusals, separate destination deletions, progress/rate guards, active diagnostics, actions and all non-Delete counters remain unchanged.
The existing grid columns remain, with the first-cell label allowed to wrap.

## Verification and limits

On `0066181d`, the original 19-case probe returned exit 1 with ten assertion failures and nine controls.
Nine failures concern labels/count nouns; the tenth exposes an independent missing-settings/defaults defect.
Contract `2f877e36` preceded correction `586e1014`.
The 44 maintained cases went from 25 failures/19 controls to 44 passes.
The identical original probe still returns exit 1: 18 pass and the unrecorded-settings case fails.
That failure remains open and is not skipped or credited as success.

Four isolated omission controls failed their intended assertions.
The final neighbouring selection passed 127 distinct cases, including the same 44 new cases; check and build passed.
An attempted Xvfb/WebKit run failed before WebKit started and produced no geometry observations or screenshot.
The packet retains that failed run and all original source/command receipts.
Raw folders are `aeronautes-job-presentation-before-5t6v9v90` and `aeronautes-job-presentation-implementation-8daBd9`.

These are frontend projection results, without a Go/Swift effect or native layout/accessibility acceptance.
Missing-settings interpretation, wider UI-09/P28, protected delivery and the valuable-data hold remain open.

## Evidence retention

The original record, probes, structured evidence, failed runs and raw verification outputs are retained in the private packet `/home/csmart/code/.aeronautes-work/evidence/20260913-service-records`.
Its `SHA256SUMS` digest is `d0ce6823082a0aab3ddb7f0b563ccc4028aba68c6471b124cad41c925650ae99`.
`original/docs/experiments/` preserves the record and probe paths at checkpoint `860ddec9ce72c241f349f491ca3b63dbab8ed5ce`; `INDEX.json` maps the raw packets and lists excluded dependency/cache directories.
Historical receipts remain unchanged; this archive does not claim to retain those excluded execution inputs or constitute a hermetic environment.
The maintained regression tests remain in the repository.
