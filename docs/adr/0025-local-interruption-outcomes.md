# ADR 0025 — preserve local outcomes on interruption

**Status:** Awaiting independent local design review under the authorised audit remediation plan.
**Scope:** The bounded earlier-outcome reporting follow-up to ADR 0024 for local upload and download.

## Problem and oracle

The [actual-service reproductions](../experiments/2026-09-08-local-interruptions.md) show a cancelled upload retaining failed/copied counters while losing every error detail, and a download hiding an earlier filesystem failure behind a later one.
A download that completed a directory before the later failure is also labelled plain `failed`.
ADR 0024 explicitly requires preserving earlier failures beside later errors or cancellation.
The existing jobs lifecycle rule permits `running` to end directly in `partial` because interruption after a successful subset must not be recorded as plain cancellation or failure.
DESIGN §§46/47 and the operator's valuable-data priority require visible, honest partial outcomes.

## Decision

A later run error cannot erase the transfer report that preceded it.
For local upload and download, retain the final report's copied, verified, byte, native-route and failed counts, and apply this terminal rule:

| Report and terminal reason | Job state and error |
| --- | --- |
| Post-commit issues, with or without another error | Existing ADR 0023 `partial` result, issue count/sample, any transfer failure summary and later reason |
| Reported transfer failures followed by an error or cancellation | `partial`, with the existing failed-count/first-failure summary and the later error text |
| At least one reported completed item followed by an error or cancellation, without failures | `partial`, with the later error text and the reported completed counts |
| Error without reported completed items, failures or post-commit issues | Existing `failed` result and error text |
| Cancellation without those reported outcomes | Existing `cancelled` result with no added error text |
| No run error or post-commit issue | Existing verification transition, then `partial` for reported failures or `complete` otherwise |

Wrapped cancellation preserves its complete later error text when the job is partial.
Do not derive successful completion from advisory progress, convert unattempted items into failures, or change successful verification counts to account for bookkeeping errors.
The old direct-finaliser error/cancellation controls supplied one completed item; retain that input and explicitly change their expected state to `partial`.
Add distinct zero-completion controls instead of changing those existing inputs to conceal the corrected behaviour.

Use ADR 0024's atomic completion and admission contract unchanged.
The terminal audit continues to match the job state using the existing coarse `verify_mismatch` result for `partial`.
That result does not assert that a verification mismatch occurred or that any item succeeded; a more precise audit vocabulary remains P13 work.

## Proof and implementation sequence

Review this decision and its real-service reproductions before changing the runner.
Implement only the local terminal selection and message preservation, with focused table tests and maintained system regressions in one logical commit.
Cover earlier failed writes, a successful subset, a wrapped cancellation, later filesystem errors, and the unchanged failure/success/no-outcome controls.
Use the existing HTTP response barrier and actual CancelJob calls; verify cancellation of the selected exchange instead of relying on elapsed sleeps.
For the native upload fixture, explicitly finish its `100-continue` handshake and read at most 64 fixture bytes before response capture; keep the common barrier's refusal of unsupported interim responses intact.
Use the existing public application setting to hold one active download while a second job finishes preflight and remains queued, then create owned filesystem refusals before releasing the first job.
Check full owned namespaces in both accounts, exact selected bytes and local filesystem deltas, other-job/unselected sentinels, retained recovery identities, job/audit state and authority, and separate-process database reopening without remote traffic.
The newline-name upload fixture is unavailable on Windows and must say so; Linux fixture evidence is not six-platform proof.
Restore the old terminal branch and independently break failure-detail preservation, later-reason preservation, partial-state selection and counter handling; every relevant regression must detect its broken rule.
Require the relevant full race suite, builds, independent local review and all five PR/master checks before delivery.

## Limits

`partial` records incomplete reported work; it is not a claim that effects are reversible or that some write necessarily succeeded.
Likewise, a `failed` or `cancelled` job without reported outcomes does not prove that no effect landed.
The report still samples at most 200 transfer failures, and copied/verified counters retain their existing verification meaning.
This change cannot reconstruct unreported identities, unknown remote acceptance, missing persistence, stronger integrity evidence or safe retries.
Orderly child-process database reopening is not a kill, fsync, lost-history or power-loss test.
No schema, bridge DTO, execution admission, local placement, cancellation boundary, cleanup authority, Copy caller or other mutation family changes here.
P12/P13, complete failure access, Copy migration, P20 cancellation safety and the valuable-data release hold remain open.
