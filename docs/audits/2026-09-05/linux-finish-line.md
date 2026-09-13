# Linux implementation finish line — 13 September 2026

This is the single scope list for the current Linux implementation checkpoint, based on source `e5be8215eb1d58cd26f62333c2cb6bc2dfbb2d40` and the [finding register](remediation-plan.md#finding-to-plan-coverage).
The operator adopted this independently reviewed list on 13 September 2026; its scope is frozen, and additions require operator sign-off recorded here.
New material risks must be reported for that decision, with affected release acceptance held.
The 34 open or partial findings remain open; these work areas are not equivalent-sized tasks or new finding IDs.

## Six production gates

| Gate | Existing packages | Completion condition |
| --- | --- | --- |
| Authorised effects and secrets | P10/P11/P14/P15/P26 | Enabled mutations enforce reviewed scope and current authority, protect collateral data and prevent credential retargeting. |
| Correct content | P16/P17/P21 | Enabled transfers detect corruption/stale identity and preserve declared metadata/link semantics before reporting success. |
| Durable recovery | P12/P13/P20/P28 | Storage failure, lost replies, cancellation and restart retain exact known/unknown outcomes and uncertain residue, with truthful UI. |
| Containment | P18/P19/P20/P30 | Placement stays inside the selected root; namespace conflicts refuse safely; writer lifetime respects shutdown and parent death. |
| Operating limits | P24; safety portions of P25/P28/P29 | Supported workloads stay bounded; scope, confirmation, cancellation and errors remain usable. |
| Supported artifacts | P31–P35; TEST-02–04 | Release artifacts pass dependency, installation, native and representative deployment checks on their claimed targets. |

## Frozen residual set

First deliver the already reviewed service/UI/shutdown batch with its required current-head PR and resulting-master checks.
The [scheduling record](../../experiments/2026-09-12-remaining-test-schedule.md#combined-measurement--13-september) fixes the hosted comparison and revert condition.
Then complete these bounded areas under [DESIGN §65.12](../../DESIGN.md#6512-proportionate-remediation-and-a-fixed-finish-line), respecting each existing package's dependencies.

| ID | Remaining scope | Completion test |
| --- | --- | --- |
| L01 | P10 credential-store identity and bounded lifecycle ([adapter](../../../internal/secrets/keychain.go)) | Changed backend, locked/cancelled access and restart preserve active credentials and retain unresolved cleanup against the original authority; native acceptance remains separately required. |
| L02 | P11 immutable backend plans and scoped capabilities ([service](../../../internal/app/deleteservice.go)) | Direct bridge, queued start, expired/replayed token and forged force/public-ACL cases cannot widen operation, account, generation or selected scope. |
| L03 | P14/P15 authority for enabled destructive paths ([segment deletion](../../../internal/app/segmentservice.go)) | Exact force prefixes, equal-ETag graph swaps, transfer cleanup, local competing stages and first/later-pass replacements follow an accepted allowed-effect contract with unchanged collateral. |
| L04 | P12/P13 remaining durable effects and complete outcomes ([mutation service](../../../internal/app/mutateservice.go)) | Synchronous mutation and segment actions, transfer terminal-write faults and ordinary/parallel deletion failure streams retain every known/unknown identity beyond display caps across restart; required-record failure prevents its dependent effect. |
| L05 | P16/P21 source, staged and published content binding ([Copy executor](../../../internal/transfer/copy/executor.go)) | Enabled Copy/upload/download routes reject same-size corruption, missing/reordered segments and source/publication substitutions, or report explicit unverified/refused outcomes with uncertain residue retained. |
| L06 | P17 replication fidelity ([planner](../../../internal/transfer/plan/plan.go)) | Metadata, content headers, expiry and link target/account differences cannot be reported as equivalent replication; preserve overwrite refusals. |
| L07 | P20 directory-materialisation and stage-cleanup cancellation ([local executor](../../../internal/transfer/local/download_execute.go)) | Cancellation at the remaining boundaries produces exactly the accepted directory/file delta and truthful retained outcomes, preserving selected-root authority. |
| L08 | P30 synchronous writer shutdown ownership ([shell drain](../../../internal/app/shutdown.go)) | A held synchronous mutation and its final record prevent database/lock release and new admission; prove the existing runtime guarantee or add only the missing ownership. |
| L09 | P26 versioning preflight and enabled-path safety ([container update](../../../internal/swiftcontrol/containerupdate.go)) | Unknown/changed mode refuses; the supported legacy transfer/delete/cleanup matrix preserves hidden versions, and modern editing remains refused. |
| L10 | P24 and safety P25 workload bounds ([download inventory](../../../internal/transfer/local/local.go)) | Establish reviewed bounds for namespace/browser retention, queued work, child batches and aggregate resources; overload refuses without truncating correctness, cancellation releases ownership, and control-plane requests remain responsive. |
| L11 | Safety P28 account-specific results ([audit UI](../../../frontend/src/features/browser/browserState.svelte.ts)) | Identical names in different accounts remain distinguishable; account-scoped results, paging, retry and error state stay bound to the selected account. |

These are source-supported residual boundaries, not eleven new runtime reproductions.
P01/P04/P05 containment remains under ADR 0020, P03 follows the harness rule in §65.12, and P08's remaining retention and queue bounds sit in L10.
Missing concurrency, durability, retained-stage removal, native approval or workload-limit decisions must be resolved before dependent implementation.
This list does not itself accept a new capability restriction, concurrency assumption or workload ceiling.
Existing root/namespace and destructive-consent guards remain required regressions; no additional P18/P19/P29 Linux defect was established by this scoping pass.

## Stop conditions and external acceptance

Complete this checkpoint when L01–L11 are fixed or covered by an explicitly accepted, enforced refusal, the prepared batch is delivered, and required Linux checks pass.
Do not add slices merely because a broad audit row is still partial.
Keep Mirror starts, existing-destination transfer overwrites and automatic recovery reclamation refused under ADR 0020; their re-enablement is outside this checkpoint.

Native keychain/picker/filesystem, actual window shutdown, descendant lifetime and accessibility journeys on the six targets remain separate acceptance work, as do physical durability evidence, sidecar/dependency dispositions, installation/upgrade/rollback, backup/restore, exact artifact identity and representative deployed Swift.
Native code changes retain their existing native-evidence gate.
Several production gates therefore cannot close on this workstation.
Declare the Linux checkpoint complete when its own conditions pass even while those gates remain open; that declaration never clears the valuable-data release hold.
Adaptive tuning, optional layout/shortcut polish and absent convenience features stay in the later v1 backlog, whose accepted scope is unchanged.
P27's exact-object deletion and remaining accepted account/directory-upload capabilities are outside this checkpoint and must be delivered or explicitly re-scoped before v1.
New implementation needed by external acceptance requires an explicit scope decision instead of silently reopening this list.
