# Shell consumer for owned job and Find shutdown

The oracle is DESIGN §§26.4, 32.1, 38, 46, 55 and 65.10, the [whole-job prerequisite](2026-09-12-job-shutdown-ownership.md), Find's reserved hook and ADR 0020 retention.
This correction consumes actual job and Find ownership before releasing shell resources.

## Behaviour

ShutdownWork closes/cancels both admissions and joins their real owners; an incomplete context wait returns an error and grants no resource-release authority.
The actual shell assembly uses the same idempotent background drain in OnShutdown and post-Run cleanup.
A held owner keeps the database and lock held.
An unexpected drain error is logged and remains blocked, without a quit-anyway path or invented timeout.

Database Close runs once after drain; only nil permits explicit lock release.
A close error is latched alongside any Run error, with the lock owner kept reachable through top-level failure reporting.
Kernel release on process exit is not graceful-close success.
Post-lock startup/Run errors follow checked cleanup; a pre-database failure still releases its unused lock.
Find's reserved five-second hook and all existing job outcomes remain unchanged.

## Verification and limits

The actual-main before probe at `997e7f02` used four narrow interpositions for runtime construction, an isolated secret store and resource observations.
Its final race run had one control and three failures: held work outlived resource release, Run error bypassed defers, and failed database Close still released the lock.
The earlier packets remain retained; this is actual shell control flow with an injected runtime, without native Wails quit.

Correction `27185bc7` and clarification `437b70af` passed local review.
The focused race baseline passed 39 leaves: 21 new app/root and 18 existing Find cases.
Held-download assertions preserve the existing partial outcome from retained-stage cleanup failure; their earlier incorrect cancelled expectation remains a failed packet.
Two real-child mock and two actual two-account Swift cases passed, covering graceful exit and ten-second forced escalation.
They establish direct-child reaping, later filesystem/namespace stability and lock/recovery ordering through ShutdownWork, separately from the shared-shell wiring tests.

Nine omission controls failed their intended assertions.
The actual-main close-error probe retained the database and cross-process lock through five GC/finalizer barriers; omitting KeepAlive made all five lock probes acquire prematurely.
Vet, formatting and diff checks passed; staticcheck was unavailable.
Raw folders are `20260912-shutdown-ordering` and `20260912-shell-consumer`.
The bound-action inventory remains unchanged and receives no new scenario cell.

Today's production background drain cannot return its context-wait errors.
Visible finishing/error feedback for a future defensive failure remains open.
Wails may report Find's reserved-hook timeout on a Run failure before the post-Run join finishes; that message does not establish a failed complete drain.
Synchronous bridge/native-request ownership, Connections/SecretStore closure, non-Linux paths, native window quit, DS-07/SEC-06/P20/P30, protected delivery and the valuable-data hold remain open.

## Evidence retention

The original record, probes, structured evidence, failed runs and raw verification outputs are retained in the private packet `/home/csmart/code/.aeronautes-work/evidence/20260913-service-records`.
Its `SHA256SUMS` digest is `d0ce6823082a0aab3ddb7f0b563ccc4028aba68c6471b124cad41c925650ae99`.
`original/docs/experiments/` preserves the record and probe paths at checkpoint `860ddec9ce72c241f349f491ca3b63dbab8ed5ce`; `INDEX.json` maps the raw packets and lists excluded dependency/cache directories.
Historical receipts remain unchanged; this archive does not claim to retain those excluded execution inputs or constitute a hermetic environment.
The maintained regression tests remain in the repository.
