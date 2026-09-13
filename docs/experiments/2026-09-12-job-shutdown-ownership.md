# Whole-job shutdown ownership

The SEC-06/P30 oracle is DESIGN §§26.4, 32.1, 38, 46 and 65.10: owned job bodies and final cleanup must finish before shell resources are released.
ADR 0020 still retains uncertain stages, segments and recovery records.
This prerequisite must ship with the [actual shell consumer](2026-09-12-shell-shutdown-consumer.md).

## Behaviour

All five public Start families acquire runner ownership atomically with the admission-stop lock, before credential pinning, preflight, engine construction or durable job creation.
Ownership lasts through complete refusal cleanup or the launched job's final pin/handle callback.
Slots, cancellation registrations and terminal rows cannot substitute for that lifetime.

Startup keeps caller cancellation; successful asynchronous handoff keeps the existing caller detachment.
CancelJob cancels the registered job, while shutdown also cancels admitted synchronous startup.
Cancellation callbacks run outside the runner mutex, and queue wakeup shares its condition mutex.

Shutdown closes admission, requests cancellation and waits for actual drain.
It adds no total deadline and preserves the child's ten-second escalation grace.
A cancelled/expired waiter returns a wrapped context error without releasing ownership or reopening admission; repeated callers can observe eventual completion.
Existing durable outcomes, unavailable-store handling and retained-stage policy remain unchanged.

## Verification and limits

Before observations on `1f57000c` held running-body, terminal-cleanup and final-pin boundaries while the old resource-order model closed SQLite and released the lock.
A second process acquired the lock and invoked recovery while ownership remained.
This was a resource-order model, without native Wails quit or remote-corruption proof.
Two initial public-service controls failed because of an incorrect empty-prefix fixture; the corrected probe passed five leaves and retained the caller-detachment/explicit-cancellation comparison.

The correction at `997e7f02` passed 62 focused race leaves: 38 new ownership/service/handle cases and 24 compatibility cases.
Eight isolated broken guards each returned exit 1 with targeted failures and healthy controls.
A Linux descriptor check required the download's derived handles to close at drain while the picker-owned handle remained usable.
The initial absent-API build failure has no behavioural reproduction credit; staticcheck was unavailable.
Raw folders are `20260912-shutdown-ordering` and `20260912-job-shutdown-ownership`.

The separate consumer supplies real-child and shared-shell proof; neither record establishes native window quit or ownership of every synchronous bridge/native request.
Credential-store lifecycle, DS-07/SEC-06/P20/P30, platform acceptance, protected delivery and the valuable-data hold remain open.

## Evidence retention

The original record, probes, structured evidence, failed runs and raw verification outputs are retained in the private packet `/home/csmart/code/.aeronautes-work/evidence/20260913-service-records`.
Its `SHA256SUMS` digest is `d0ce6823082a0aab3ddb7f0b563ccc4028aba68c6471b124cad41c925650ae99`.
`original/docs/experiments/` preserves the record and probe paths at checkpoint `860ddec9ce72c241f349f491ca3b63dbab8ed5ce`; `INDEX.json` maps the raw packets and lists excluded dependency/cache directories.
Historical receipts remain unchanged; this archive does not claim to retain those excluded execution inputs or constitute a hermetic environment.
The maintained regression tests remain in the repository.
