# Linux implementation finish line — 13 September 2026

This is the single scope list for the current Linux implementation checkpoint, based on source `e5be8215eb1d58cd26f62333c2cb6bc2dfbb2d40` and the [finding register](remediation-plan.md#finding-to-plan-coverage).
The operator adopted this independently reviewed list on 13 September 2026; its scope is frozen, and additions require operator sign-off recorded here.
New material risks must be reported for that decision, with affected release acceptance held.
The 34 open or partial findings remain open; these work areas are not equivalent-sized tasks or new finding IDs.

## Operator re-baseline — 14 September 2026

The operator narrowed v1 on 14 September 2026 to four capabilities: browse, inspect, download, and metadata read and write.
Copy, mirror, upload and bulk delete are deferred out of v1 and return one capability at a time, each when it is proven.
The operator's stated reason is that the product surface was too large for one person to make safe: every route needs the same proof, so fixing one route never closed an item.

This is a reduction of scope recorded under the sign-off rule above, not an addition.
It changes which residual areas v1 requires; it does not retract anything already delivered, and it does not lift the valuable-data release hold.

Deferring a capability defers its proof, not its defects.
Defects already found in copy, mirror, upload and delete remain in the shipped code and remain worth fixing, because the operator has ruled that fixing defects is critical regardless of which capabilities survive the cut.
A deferred capability that is still reachable in a build is still reachable by a user; deferral describes what v1 must prove, not what the binary refuses.
Whether the deferred capabilities are disabled, hidden or left enabled in a v1 build is a separate decision that this record does not make.

One boundary is not settled by the four capability names and needs the operator's answer before the affected slices are scoped.
`MutateService` holds both the object and container metadata edits that v1 keeps and, alongside them, container creation, prefix-marker creation and marker and manifest deletion.
Container creation and marker creation are neither metadata editing nor browsing, and marker and manifest deletion are destructive; the narrowing cuts through that service rather than around it.
The operator resolved container creation into v1 and both deletions out of it on 22 September 2026; whether prefix-marker creation follows container creation is still an open question, as the operator decisions section below records.

## Operator decisions — 22 September 2026

The operator settled the questions this record left open on 22 September 2026, under the sign-off rule above.

1. v1 supports Linux only.
   The release pipeline still builds all six targets and K-PLAT-003 remains the product requirement, but the four macOS and Windows artifact groups are development builds without acceptance until their own native, installation and provider runs are done; for v1, "Supported artifacts" means the Linux packages.
2. Public-read container ACL saves stay in v1 behind the existing explicit acknowledgement (§19.1): `MutateService` refuses a world-readable read ACL unless `ContainerEditDTO.AckPublic` is set, and the container editor collects that acknowledgement.
   The operator additionally requires something visual while browsing that shows world-readable and similar container settings, naming web listing, versioning and the like as examples; that is new v1 scope, and its design is a separate reviewed slice.
3. Container creation is a v1 capability; marker deletion and manifest deletion stay deferred and refused under [ADR 0052](../../adr/0052-v1-capability-gate.md).
   The operator's words were that creation is a v1 feature and deletes are blocked; whether prefix-marker creation is included is the author's reading, recorded as an open question to the operator, and marker creation stays deferred in the gate until the operator confirms.
4. Very large namespaces are v1 scope.
   The download inventory keeps a complete list today, measured in the [peak-memory record](../../experiments/2026-09-22-download-inventory-peak-memory.md); the operator directs that the approach follow how established object-storage clients handle containers with hundreds of millions of objects, researched and proposed before any implementation, rather than a bare count ceiling.
5. The stopped named-read session capture (ADR 0066), engine download binding assessment and P31 advisory mapping tasks are resumed.

None of these decisions closes a finding, changes a production gate or lifts the valuable-data hold.

## Operator decisions — 23 September 2026

The operator answered two of the questions the 22 September proposals left open, in these words:

6. Container badges: "sounds good".
   [ADR 0071](../../adr/0071-container-settings-indicators.md) is accepted for implementation as proposed; the author reads the words as covering the proposed badge set and the operator-started check over loaded rows, and records that reading here rather than as a decision.
7. Huge containers: "sounds good, ideally it should be configurable".
   [ADR 0072](../../adr/0072-large-namespace-handling.md) is accepted for implementation as proposed, with the download selection ceiling a setting the operator can change rather than a fixed constant; the author reads the recommended 250,000 objects as its default, the streaming count as v1 and the streaming batches beyond the ceiling as later work, and records those readings here rather than as decisions.

Prefix-marker creation remains an open question; marker creation stays deferred in the gate until the operator answers.
Acceptance of the thirty re-proposed P31 rows remains open.

## Six production gates

| Gate | Existing packages | Completion condition |
| --- | --- | --- |
| Authorised effects and secrets | P10/P11/P14/P15/P26 | Enabled mutations enforce reviewed scope and current authority, protect collateral data and prevent credential retargeting. |
| Correct content | P16/P17/P21 | Enabled transfers detect corruption/stale identity and preserve declared metadata/link semantics before reporting success. |
| Durable recovery | P12/P13/P20/P28 | Storage failure, lost replies, cancellation and restart retain exact known/unknown outcomes and uncertain residue, with truthful UI. |
| Containment | P18/P19/P20/P30 | Placement stays inside the selected root; namespace conflicts refuse safely; writer lifetime respects shutdown and parent death. |
| Operating limits | P24; safety portions of P25/P28/P29 | Supported workloads stay bounded; scope, confirmation, cancellation and errors remain usable. |
| Supported artifacts | P31–P35; TEST-02–04 | Release artifacts pass dependency, installation, native and representative deployment checks on their claimed targets. |

### The gates under the narrowed scope

No gate is dropped by the re-baseline, and none of them closes merely because a capability was deferred.
What changes is how much of each gate v1 has to satisfy.

Authorised effects and secrets keeps P10 whole, keeps the P11 halves named in the table, and keeps P26; P14 and P15 shrink to the local-job scope coordination noted against L03.
Correct content keeps the P16 download route and defers P17 and the P21 upload gates with the capabilities that need them.
Durable recovery keeps P20 and P28 whole and keeps the P12 and P13 obligations that metadata writing and download place on them.
Containment is unchanged: placement inside the selected root, namespace refusals and writer lifetime all serve download and shutdown, which v1 keeps.
Operating limits is unchanged, because its bounds serve browse and download.
Supported artifacts narrows to the claimed targets rather than to fewer checks: a Linux-first v1 still owes dependency, installation and representative-deployment evidence for Linux, and the six-target native acceptance is deferred with the targets rather than satisfied.
On 22 September 2026 the operator chose Linux only for v1, so the claimed targets are the Linux packages (see the operator decisions above).

Several of these still cannot close on this workstation at all, and deferring capabilities does not change that.
Native keychain, picker and filesystem journeys, actual window shutdown, descendant lifetime and accessibility on the six targets, physical durability evidence, installation, upgrade and rollback, backup and restore, exact artifact identity and representative deployed Swift remain external acceptance work.
Narrowing v1 reduces how many of those a claim would rest on; it does not make any of them satisfiable here.

## Frozen residual set

First deliver the already reviewed service/UI/shutdown batch with its required current-head PR and resulting-master checks.
The [scheduling record](../../experiments/2026-09-12-remaining-test-schedule.md#combined-measurement--13-september) fixes the hosted comparison and revert condition.
Then complete these bounded areas under [DESIGN §65.12](../../DESIGN.md#6512-proportionate-remediation-and-a-fixed-finish-line), respecting each existing package's dependencies.

| ID | Remaining scope | Completion test | v1 after the 14 September re-baseline |
| --- | --- | --- | --- |
| L01 | P10 credential-store identity and bounded lifecycle ([adapter](../../../internal/secrets/keychain.go)) | Changed backend, locked/cancelled access and restart preserve active credentials and retain unresolved cleanup against the original authority; native acceptance remains separately required. | Required. Credentials are acquired before any browse, and the whole item applies unchanged. |
| L02 | P11 immutable backend plans and scoped capabilities ([service](../../../internal/app/deleteservice.go)) | Direct bridge, queued start, expired/replayed token and forged force/public-ACL cases cannot widen operation, account, generation or selected scope. | Partly required. The selected-local-resource half binds the download picker and is required; the public-ACL approval half guards container metadata editing and is required with it. The forged-force half belongs to the deferred destructive paths. |
| L03 | P14/P15 authority for enabled destructive paths ([segment deletion](../../../internal/app/segmentservice.go)) | Exact force prefixes, equal-ETag graph swaps, transfer cleanup, local competing stages and first/later-pass replacements follow an accepted allowed-effect contract with unchanged collateral. | Deferred with the destructive paths, except the local-job scope coordination P15 names. Enforced single-job execution removes this application as a source of a competing stage; an external writer at the same name is not removed by it, and the download route's own protection is delivered under L05. |
| L04 | P12/P13 remaining durable effects and complete outcomes ([mutation service](../../../internal/app/mutateservice.go)) | Synchronous mutation and segment actions, transfer terminal-write faults and ordinary/parallel deletion failure streams retain every known/unknown identity beyond display caps across restart; required-record failure prevents its dependent effect. | Partly required, and more than the entry point suggests. Synchronous mutation is the object and container metadata write path, so P12's durable-fact and residue rules and P13's identity-keyed outcome and audit record apply to metadata writing and to download terminal-write faults. Segment actions and deletion failure streams are deferred. |
| L05 | P16/P21 source, staged and published content binding ([Copy executor](../../../internal/transfer/copy/executor.go)) | Enabled Copy/upload/download routes reject same-size corruption, missing/reordered segments and source/publication substitutions, or report explicit unverified/refused outcomes with uncertain residue retained. | Partly required. Native download same-size binding is delivered. Missing/reordered segments and whole-job source/publication substitution on that route are delivered under [ADR 0060](../../adr/0060-native-download-content-binding.md). The engine download path is bound under [ADR 0070](../../adr/0070-engine-download-content-binding.md) as of 23 September 2026: same-size corruption, run permutation, missing-with-duplicate substitution, staged, source and published replacement refuse or report on both fixtures with the refused bytes retained. Copy, native upload and staged upload commit remain a named deferred gap. L05 is not closed. |
| L06 | P17 replication fidelity ([planner](../../../internal/transfer/plan/plan.go)) | Metadata, content headers, expiry and link target/account differences cannot be reported as equivalent replication; preserve overwrite refusals. | Deferred. Replication equality is a property of copy and mirror; a download to the local filesystem makes no replication claim. |
| L07 | P20 directory-materialisation and stage-cleanup cancellation ([local executor](../../../internal/transfer/local/download_execute.go)) | Cancellation at the remaining boundaries produces exactly the accepted directory/file delta and truthful retained outcomes, preserving selected-root authority. | Required. Download cancellation and its stage and directory delta are entirely within v1. |
| L08 | P30 synchronous writer shutdown ownership ([shell drain](../../../internal/app/shutdown.go)) | A held synchronous mutation and its final record prevent database/lock release and new admission; prove the existing runtime guarantee or add only the missing ownership. | Required. The held synchronous mutation P30 names is a metadata write, so shutdown ownership is required by the metadata capability rather than by the deferred ones. |
| L09 | P26 versioning preflight and enabled-path safety ([container update](../../../internal/swiftcontrol/containerupdate.go)) | Unknown/changed mode refuses; the supported legacy transfer/delete/cleanup matrix preserves hidden versions, and modern editing remains refused. | Required. Container update is container metadata writing; the versioning refusals are part of the capability v1 keeps. The legacy transfer and delete portions of its matrix are deferred with those routes. |
| L10 | P24 and safety P25 workload bounds ([download inventory](../../../internal/transfer/local/local.go)) | Establish reviewed bounds for namespace/browser retention, queued work, child batches and aggregate resources; overload refuses without truncating correctness, cancellation releases ownership, and control-plane requests remain responsive. | Required. Namespace and browser retention serve browse, and the download inventory and queued-work bounds serve download. |
| L11 | Safety P28 account-specific results ([audit UI](../../../frontend/src/features/browser/browserState.svelte.ts)) | Identical names in different accounts remain distinguishable; account-scoped results, paging, retry and error state stay bound to the selected account. | Required. Account-scoped results serve browse and inspect directly. |

These are source-supported residual boundaries, not eleven new runtime reproductions.
P01/P04/P05 containment remains under ADR 0020, P03 follows the harness rule in §65.12, and P08's remaining retention and queue bounds sit in L10.
Missing concurrency, durability, retained-stage removal, native approval or workload-limit decisions must be resolved before dependent implementation.
This list does not itself accept a new capability restriction, concurrency assumption or workload ceiling.
Existing root/namespace and destructive-consent guards remain required regressions; no additional P18/P19/P29 Linux defect was established by this scoping pass.

## Stop conditions and external acceptance

Complete this checkpoint when the residual areas the re-baseline marks as required are fixed or covered by an explicitly accepted, enforced refusal, the prepared batch is delivered, and required Linux checks pass.
The deferred areas are neither fixed nor refused by this record; they are out of v1's completion test and keep whatever open findings they carry.
Do not add slices merely because a broad audit row is still partial.

Several of the required areas are expected to complete as refusals rather than as implementations, and that is a completion rather than a shortfall when the refusal is accepted and enforced.
Trusted acknowledgement of a selected local resource is the clearest case: the connect action is reachable from the bridge, so the backend cannot authenticate which account the operator believed they were choosing for, and that acknowledgement can be neither built nor validated on this workstation.
Where an area completes that way, the refusal and its limits belong in the record for that area, and the capability stays described by what it refuses rather than by what it proves.
Keep Mirror starts, existing-destination transfer overwrites and automatic recovery reclamation refused under ADR 0020; their re-enablement is outside this checkpoint.

Native keychain/picker/filesystem, actual window shutdown, descendant lifetime and accessibility journeys on the six targets remain separate acceptance work, as do physical durability evidence, sidecar/dependency dispositions, installation/upgrade/rollback, backup/restore, exact artifact identity and representative deployed Swift.
Native code changes retain their existing native-evidence gate.
Several production gates therefore cannot close on this workstation.
Declare the Linux checkpoint complete when its own conditions pass even while those gates remain open; that declaration never clears the valuable-data release hold.
Adaptive tuning, optional layout/shortcut polish and absent convenience features stay in the later v1 backlog, whose accepted scope is unchanged.
P27's exact-object deletion and the remaining accepted account and directory-upload capabilities were outside this checkpoint and previously had to be delivered or explicitly re-scoped before v1.
The 14 September re-baseline is that re-scoping for the upload capabilities and for exact-object deletion: they are deferred with the routes they belong to, and they are no longer required before v1.
New implementation needed by external acceptance requires an explicit scope decision instead of silently reopening this list.
