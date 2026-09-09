# ADR 0033 — checked download item records

**Status:** Proposed for independent local review under the authorised audit remediation plan.
**Scope:** The separate download caller migration required by [ADR 0032](0032-download-placement-prerequisites.md) step seven.
Implementation requires approval of this contract first.

## Problem and oracle

The [reviewed download archive](../experiments/2026-09-09-download-boundaries.md) reproduces 201 and 401 failed downloads reported as 200, with no retained item identities, through actual services on both owned providers and both execution routes.
Its native route uses a newline in the remote prefix while keeping ordinary supported local filenames.
The captured-root, stage-lifecycle and native-closure corrections have passed independent review and protected PR/master delivery; [PR70](../experiments/2026-09-09-mock-scheduling.md#independent-review-and-protected-delivery) also delivered their admission tests and measured scheduling prerequisite.
Downloads still use record version zero.

The oracle is the exact selected-name set, captured local destination, independently witnessed placement/content/cleanup effects and the checked logical-record contract in [ADR 0028](0028-transfer-item-records.md).
[ADR 0031](0031-upload-item-recording.md) supplies the existing optional-recorder, checked application adapter and item-derived completion pattern.
[ADRs 0024](0024-local-job-completion.md), [0025](0025-local-interruption-outcomes.md) and [0032](0032-download-placement-prerequisites.md) retain earlier copied bytes and problems beside later failure, cancellation or cleanup issues.
The original archives and failed producers remain unchanged.

Retain every admitted download identity and its actual final observations, independently of display samples.
A required record refusal must stop further dependent work and leave missing facts visibly unresolved.
This is a logical-item migration; full per-effect history, stronger content verification and stable filesystem authority remain separate requirements.

## Identity and admission

Add a download-specific optional recorder with checked admission, execution and verification callbacks.
Require all three callbacks when configured and reject partial configuration before transfer work.
Direct unrecorded executor callers keep their compatibility mode; only the complete new application caller opts into version one.
Keep jobs-store and bridge dependencies in the application adapter and reuse the existing typed observation and diagnostic values in the executor.
Pass the original `DownloadItem` to callbacks so an adapter need not reconstruct identity from display text or retain another whole-selection map.

| Recorded field | Download value |
| --- | --- |
| Logical key | Exact full remote object name from the preflight item |
| Source | Captured source bookmark, container and that full object name |
| Destination | Local location formed from the unchanged captured absolute root and the validated supported relative path |
| Staging | Exact planned native temporary pathname or engine staged-file pathname; absent for a directory marker |
| Listed bytes | Nonnegative preflight size observation, explicitly advisory |
| Planned/admitted | Both true for a preflight-approved item admitted to this execution |

Preserve remote prefix/control/Unicode spelling and the supported local spelling exactly.
A root directory marker keeps its real nonempty remote object name and maps its empty relative path to the captured root; do not invent an empty Swift source or a temporary file for it.
Validate constructed item identity, size and relative-path representation before dependent work, including the existing namespace restrictions and the empty-relative-path exception only for a directory marker.
Require the full object name to belong to the unchanged captured prefix and its exact suffix to map losslessly to the recorded relative path using the existing directory-marker/root exception.
Individually valid fields are insufficient: the engine's selected name, admitted staged pathname and final destination must identify the same preflight item.
Do not join and thereby clean an unsupported relative spelling into a different admitted target.
Validate the application intent through the existing pure jobs validator before attempting its store transaction; invalid caller representations must refuse effects without deliberately tripping the persistence latch.
Keep the existing complete preflight and captured-root guard.

Use checked one-item admission transactions initially and measure their cost.
Every native item is acknowledged before its selected-object body is opened or its parents/temporary file are created.
Every directory marker is acknowledged before materialisation.
Every member of an engine batch has its exact staged pathname acknowledged before group-stage creation or child dispatch.
Planning listings and other existing read-only preflight checks are not transfer dispatch.
An item record never grants retry, cleanup, native approval or exclusive ownership authority.

## Native temporary identity

Choose the native temporary basename in memory before admission, using fresh unpredictable randomness and the existing private temporary-name prefix beside the final target.
If name generation fails, stop before admission or filesystem work.
After acknowledgement, open exactly that pathname with exclusive creation and private file permissions.
Do not use `CreateTemp` to create an unrecorded pathname and then record it after the effect, record only its parent, or silently try another name after an admitted collision.

An existing file, directory or symlink at the admitted temporary name refuses that item and is retained unchanged.
Track whether this invocation's exclusive creation succeeded; never unlink a pathname whose exclusive create failed.
Keep checked source-body closure when parent-directory or temporary-file admission fails.
Preserve the native length, sync, file-close and source-close prerequisites before no-replace final placement, and preserve simultaneous primary/cleanup errors.
Successful final placement retains copied bytes beside a later temporary cleanup refusal.

This changes when the temporary name is chosen so the immutable intent can name the actual attempted location.
It does not establish a stable parent inode, exclude concurrent replacement of the selected temporary pathname or certify physical durability.
The existing selected-path cleanup race remains part of P18 rather than being erased by the recorded name.

## Observations and bounded verification

Return a typed final item result from the native and staged helpers, retaining whether placement succeeded, actual attempted verification, diagnostics and exact post-placement issue counts.
Append execution only after the helper has made its required closure and permitted item-cleanup decisions.
Check execution acknowledgement before appending verification or starting later dependent work.
If execution persistence fails after placement, retain the earlier intent and expose unavailability; do not append verification to conceal that missing acknowledgement.
If verification persistence fails, retain the acknowledged copied execution and its missing-verification facet.

For files, successful verification means only the established size/closure prerequisites and successful no-replace placement were observed.
A staged length check alone must not produce successful final verification when later placement fails.
Record failed verification only for a check actually attempted; a source-open failure or failed child does not acquire a fictional verification observation.
No fresh source version, strong checksum or stable final inode is proved by these existing checks.

For a directory marker, require successful `MkdirAll` followed by an actual observation that the target is a directory.
Only that observed materialisation contributes one copied/verified marker and zero bytes.
If either step fails, retain an unresolved failed result because parents or the directory may already have been created; do not claim no effect from the final error.
An existing directory can satisfy materialisation, so the copied marker does not assert exclusive directory creation.
This extra directory observation is the bounded verification prerequisite for its new recorded success.

| Witnessed boundary | Execution and optional verification |
| --- | --- |
| Admitted item stopped before any relevant local mutation or engine invocation | `not_attempted` with `not_attempted` effect knowledge and typed cancellation/incomplete reason; no verification |
| Native selected-object open fails before local work | `failed`, `not_attempted`; no verification |
| Parent/temporary/stage creation, native transfer or engine invocation began but final placement is not established | Failed/conflict or missing execution with unresolved effects; failed verification only when an actual check failed |
| Staged/native checks pass but final no-replace link fails | Failed/conflict execution with unresolved effects; no successful verification and no copied bytes |
| File passes the established checks and final placement succeeds | `copied`, `reported_applied`, with witnessed bytes; verified observation |
| Final placement succeeds but item cleanup has a problem | Keep `copied` and bytes with a typed problem and unresolved effects; retain actual verification when its acknowledgement succeeds |
| Directory materialisation and directory observation both succeed | Zero-byte `copied`, `reported_applied`; verified observation |
| Directory creation or its subsequent observation fails | Failed execution with unresolved effects; failed verification only if the directory observation was attempted |

`not_attempted` requires positive knowledge that no relevant mutation was invoked.
Do not infer it merely because a child did not start after parent or group-stage creation, an operation returned an error, or cancellation arrived.
Missing admitted execution remains unresolved under the existing storage rules.
One identity with both failed execution and failed verification contributes one failed name, while copied bytes, conflicts, uncertainty and post-placement problems remain separate overlapping facts.
Preserve the exact post-placement issue count independently of its 200 display samples; the first typed diagnostic is not a complete per-effect issue history.

## Stop boundaries and group cleanup

A required admission or observation refusal becomes sticky for this download.
Do not start another native object read, directory or temporary creation, engine invocation/retry, final placement or deferred filesystem cleanup after observing that refusal.
Close already open handles and wait for an already started synchronous child to return.
That child may already have written any admitted batch member; a callback error does not prove child quiescence or absence of staged effects.
Already admitted work may save remaining witnessed facts when storage permits, but such completion recording cannot authorise another transfer effect.

Check known cancellation before each directory materialisation, native read/create/placement, engine invocation/retry and staged placement.
Stop launching later transfer work after that check observes cancellation and preserve earlier results.
A call already started before cancellation can still have effects; record the exact witnessed limit rather than promise an atomic cancellation interlock.
Cancellation alone does not turn existing permitted cleanup into proof of no effect or new reclamation authority.
Required-record refusal also stops that cleanup, including the engine group's deferred cleanup.
Use a non-cancelled context only for saving completion facts.

Retain the existing exclusive engine-stage creation and conservative selected empty-directory cleanup.
Group cleanup belongs in an explicit group result and matching final job/audit reason, without a fabricated failed-object identity.
Distinguish cleanup actually attempted and refused from a stage deliberately retained because required recording failed.
Keep the existing cleanup issue count for actual scope, inspection, type and removal refusals.
Deliberate retention must have its own explicit group fact and reason even when that issue count is zero; say cleanup was not attempted rather than inventing another cleanup failure.
Both kinds of group problem preserve earlier copy/verification/byte facts and the earlier job reason.
No recursive traversal/deletion, uncertain-file reclamation or automatic recovery is added.

## Application completion and access

Create only new downloads with immutable `ItemRecordsVersion=1` after the complete executor/adapter migration is implemented and reviewed.
Carry that version through start-audit failure, planning/running refusal, queued cancellation and every terminal path.
Use the existing `FinishFromItems` transaction to derive distinct failed names and all six item facets alongside the matching terminal audit.
Keep copied/verified/byte counters from witnessed helper results, as for upload; those counters do not count acknowledged observation rows.
A refused verification write can therefore retain a witnessed verified counter alongside missing durable verification, an unverified item facet and unavailable public reads.
Do not relabel either observation as the other or erase already witnessed placement to make the counters appear disjoint.
Historical version-zero jobs retain their existing unrecorded status and reads.
New version-one empty selections have an available empty ledger, distinct from missing or unavailable records.

The shared local runner must use a neutral or operation-specific sampled-failure label for recorded downloads, replacing its current hardcoded upload label.
No display sample may supply an apparently exact failed count in job text or terminal counters.
Preserve source bookmark audit authority, existing local terminal operation names, cancellation-registration release and earlier partial/cancelled/failed distinctions.
Complete remains forbidden whenever failed, conflicting, refused, unresolved, unverified or problematic items remain.
Group retention/cleanup problems independently prevent unqualified completion.

Reuse bounded complete item pages and the quoted local/remote location view to expose every retained download identity, exact temporary location, both phases and absent facts.
Keep backend and UI rerun refusal for local history; a displayed pathname cannot become a new selection token.
The shared item-store latch must still make unavailable reads explicit and refuse all new job families after a required item write fails.
Orderly reopening preserves every retained item, missing suffix and interrupted facet without HTTP, cleanup or a fabricated terminal success audit.
New diagnostics remain typed stage/code/status only; existing free-form job/audit redaction and retention/export work remain separate.

## Implementation and required evidence

1. Review this contract against the unchanged archive, delivered prerequisites and actual storage/executor code before application edits.
   Preserve separate logical commits and independent reviews for unrelated corrections.
2. Implement the checked executor and first version-one application adapter/completion together, including exact native temporary admission, directory observation, bounded cancellation checks and recording-stop cleanup retention.
   Keep the existing direct-call compatibility mode and upload/Copy callers covered; do not ship a partially configured recorder or an unchecked version-one terminal path.
3. Maintain actual-service mock and two-account SAIO cases for both execution routes with 0/1/201/401 failed ordinary local names plus a successful item.
   Require exact selected keys, source/destination/temporary identities, complete pages beyond 200, independently read content and exact public/terminal/audit counts, with no lost or duplicate names.
   Keep separate empty-selection and historical-unrecorded controls, and refuse invalid constructed relative paths or mismatched name/prefix/path fields before effects without latching the store.
4. Inject actual SQL refusal before native/directory/engine effects and after independently witnessed placement, separately for execution and verification observations.
   Prove acknowledgement order, retained earlier facts, no later dependent effects or deferred filesystem cleanup, unavailable public reads and refusal of all eight new-job families sharing the store.
   Include a writable-storage control and inspect the actual database and DTO rather than only callback counts.
5. Cover admitted temporary collisions, root/nested/existing directory markers, failed directory observation, existing engine stages, partial child data, raced final targets and simultaneous native closure/cleanup errors.
   Check cancellation before dispatch, after stage creation, between placements and before native final placement, plus missing observations after an already started child.
   Preserve exact copied bytes, group reasons and issue counts with complete permitted local/remote deltas, unrelated staged data and owned shared DLO/SLO collateral where applicable.
6. Reopen complete retained records in an orderly child process and exercise actual UI pages, exact quoted paths, overlapping facets, unavailable versus empty state and local rerun refusal.
   Do not promote an injected picker, helper-only fault, orderly reopen or DOM rendering test into native-dialog, full-app restart, crash or accessibility certification.
7. Remove admission checks, exact temporary identity, sticky stop, cancellation guards, observation ordering, verification/closure/cleanup distinctions, distinct-name accounting and complete reads in deliberate broken controls.
   Require meaningful behavioural failures alongside passing controls, preserving the original archives and all failed producer evidence.
8. Run relevant focused checks, complete race/inventory/scenario verification and build/binding checks against the final source within the existing finite CI limits.
   Require independent local review, all five latest current-base PR jobs, protected merge of the exact approved head and all five resulting master jobs before delivery.
   Record actual source/merged tree equality, complete inventories and measured hosted runtime without inventing an execution result for an unstarted job.

## Limits

This migration does not close P13/P15/P18/P19/P20/P28 or any complete audit package by itself.
Stable root capabilities, concurrent pathname replacement, stronger source/content identity, full per-effect history, physical durability, native approval, orphan-child quiescence, retention/export and all six native-platform acceptance gates remain separate.
Mirror starts, changed Copy overwrites and automatic recovery reclamation remain refused.
The [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remains in force.
