# ADR 0027 — preserve Copy outcomes on interruption

**Status:** Accepted after independent local design review under the authorised audit remediation plan.
**Scope:** The separate earlier-outcome reporting follow-up scheduled by ADR 0026.

## Problem and oracle

The [actual-service reproductions](../experiments/2026-09-08-copy-interruptions.md) show Copy retaining copied or failed counters while reporting plain cancellation or failure after a later listing error.
Cancellation loses its reason entirely when no overwrite refusal exists, and both error paths omit earlier transfer-failure details.
The existing refusal override preserves partial state and its own reason but also loses an earlier transfer failure.
ADR 0026 deliberately left these caller decisions unchanged while making the summary atomic.
ADR 0024, ADR 0025's earlier-outcome rule, DESIGN §§46/47, K-XFER-008 and the operator's valuable-data priority require an honest partial result that retains both the report and the later reason.

## Decision

When Copy's existing selected run error is non-nil, preserve its final execution report and apply this rule before the existing overwrite-refusal finaliser:

| Report and selected run error | Requested terminal state and reason |
| --- | --- |
| One or more reported transfer failures | `partial`, with the existing failed-count/first-failure format followed by the complete later error text |
| At least one reported completed item, without failures | `partial`, with the complete later error text |
| Cancellation without either of those reported outcomes | Existing `cancelled` result and empty error unless an overwrite refusal requires retaining the later reason |
| Other error without either of those reported outcomes | Existing `failed` result with the later error text |
| No selected run error | Existing verification and finalisation paths |

Reported failures take precedence over reported completion when choosing the reason so a completed subset cannot hide an earlier failure.
Keep the wrapped cancellation text, including its source-listing and request context, when the result is partial.
Use final report values, never advisory progress, to make this decision.
Do not count names that the planner has not emitted as failed or copied, or acquire verified counts without entering verification.
The existing engine-barrier cancellation test reports an attempted failed item; retain its original input and explicitly correct its terminal expectation to partial with that failure and cancellation.
Keep the separate actual-service before-work control to prove zero-outcome cancellation remains cancelled.

Keep ADR 0026's atomic completion, admission, source-bookmark audit and destination-bookmark recovery contract unchanged.
Any overwrite refusal still forces `partial`, adds its separate count and existing explanation, and retains the earlier failure and later reason supplied by this rule.
The audit follows the final job state using the existing coarse `verify_mismatch` value for partial results; that value does not prove a mismatch or a successful write.

## Proof and implementation sequence

Review this decision and the reproduction evidence before changing the caller.
Then change only Copy's earlier-report/later-error terminal branch in one logical implementation commit, with maintained actual-service regression cases.
Reuse the existing completion operation and HTTP response barrier; do not introduce a new outcome framework or change the executor.

The actual StartCopy fixtures use mock Swift and two-account SAIO with the official pinned engine.
The fixture proxy requests one-object server pages, leaving the application listing, planner and executor intact.
The planner advances its source iterator before emitting an item, so the barrier deliberately holds the page after a lookahead engine name: earlier native outcomes and refusals are already reported while the lookahead name has not been executed.
Actual CancelJob cancels that selected response, or a controlled HTTP 403 response supplies the later non-cancellation error.
Retain success, refusal-only and before-work cancellation/error controls.
Check exact counters, first failure and full later reason, both complete fixture namespaces and intended bytes, shared DLO/SLO aliases, other-job staging and unselected names, source audit authority while the destination bookmark is active, retained destination recovery identities and cancellation deregistration.
Each namespace snapshot covers containers under that harness's unique prefix, including derived sidecars; it does not observe containers outside that prefix.
Wait for captured binding release, then compare orderly child-process database reopening without remote requests.
The remote newline names require native routing but do not require newline-containing local files or a Windows fixture skip.

Restore the old caller and independently break earlier-failure detail, later-reason preservation, copied-subset and failure precedence, refusal handling, counters, and wrapped-cancellation preservation; each relevant regression must detect its broken rule.
Run the relevant full race suite, frontend and build checks, obtain independent local implementation review, and require all five latest PR and resulting master jobs before delivery.

## Limits

`partial` describes incomplete reported work; it does not prove a successful write or reversible effects.
Likewise, a cancelled or failed job without reported outcomes does not establish that no remote effect landed.
The controlled fixtures independently witness their exact allowed effects; they do not generalise that evidence to unknown acceptance.
The 200-failure sample, duplicate execution/verification failures, DLO planned logical bytes, full identity ledger and complete failure access remain separate P13 work.
This slice preserves the existing selection between plan and Finish errors; simultaneous errors and logged executor-close or reconciliation failures remain explicitly tracked follow-up work.
Errors raised by the later verification stream also remain outside this selected-run-error branch and require their own reproduction and reviewed outcome contract.
Conflict-only reports and complete conflict details beside a later error remain part of that wider outcome work; the reproduced rule here covers reported copied items and transfer failures while preserving the existing overwrite-refusal override.
No schema, DTO, UI, execution, cleanup, admission, cancellation boundary, native lifetime or other mutation-family change is included.
Orderly child reopening is not crash, fsync, lost-history or power-loss evidence.
Mirror starts, changed-destination overwrites and automatic recovery reclamation remain refused, and the valuable-data release hold remains in force.
