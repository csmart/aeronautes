# Audit query and Recent observations

The UI-10 correction makes failed, pending, empty and stale audit observations distinct.
The oracle is DESIGN §§47, 49, 50 and 55, K-UI-005, K-SEC-002/006, and the store's descending unique-ID pages with an exclusive before-ID cursor.

## Behaviour

A selected literal operation/result pair owns its query generation and cursor.
Changing either filter retires old success, failure and finalisation replies, including an away-and-back change.
More admits only one request synchronously and accepts at most 50 positive safe integer IDs, strictly descending and below the cursor.
An invalid page is rejected whole, preserving the previous rows and retry cursor.
Recent has its own bounded 50-row observation and generation; its counts do not describe the selected Search query.

Pending, successful empty, unavailable, stale and failed-page states use fixed safe wording and explicit read-only recovery.
Audit read failure no longer rejects an otherwise acknowledged mutation.
All seven existing mutation wrappers and connection awaits retain their ordering; actual mutation errors still reject.

## Verification and limits

On unchanged source `836ad0d2`, the exact six-case mounted probe produced five intended assertion failures and one healthy control in two actual exit-1 runs.
The legal 50-row More case is the paging proof; an earlier manually seeded two-row diagnostic is not a legal-page reproduction.
Contract `4b112f80` preceded the correction at `4f9e356d`.
The final focused selection passed 450 distinct cases: 61 state, 13 mounted audit and 376 neighbouring cases; the original six cases also passed.
Frontend check and build passed.
Twelve isolated broken variants produced 18 targeted failures with actual exit 1; repeated selections are not additional coverage.

The packet retains the initial test failures, source hashes, complete inventories and parser/command receipts.
Raw folders are `aeronautes-audit-query-before-2txxcc77`, `aeronautes-audit-query-before-jah_04xb` and `aeronautes-audit-verification`.
These results establish frontend observation behaviour, without native Wails, deployed Swift or full-repository acceptance.
Account-specific history, broader P24/P28 obligations, protected delivery and the valuable-data hold remain open.

## Evidence retention

The original record, probes, structured evidence, failed runs and raw verification outputs are retained in the private packet `/home/csmart/code/.aeronautes-work/evidence/20260913-service-records`.
Its `SHA256SUMS` digest is `d0ce6823082a0aab3ddb7f0b563ccc4028aba68c6471b124cad41c925650ae99`.
`original/docs/experiments/` preserves the record and probe paths at checkpoint `860ddec9ce72c241f349f491ca3b63dbab8ed5ce`; `INDEX.json` maps the raw packets and lists excluded dependency/cache directories.
Historical receipts remain unchanged; this archive does not claim to retain those excluded execution inputs or constitute a hermetic environment.
The maintained regression tests remain in the repository.
