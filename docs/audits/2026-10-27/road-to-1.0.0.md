# Road to 1.0.0 — release plan

Written on 27 October 2026, after the operator decided that deletion comes next, in their words "yes, we need delete", and asked for the plan and the outstanding features on the way to a 1.0.0 release.
This was the author's proposed sequence when it was written; on 27 October 2026 the operator accepted it through the Linux half of 0.7.0 and granted delivery authority for the Linux release (finish-line decisions 31 and 32), and the decisions still open are listed at the end.
The decisions it needs are listed at the end, and the [finish line](../2026-09-05/linux-finish-line.md) records them in its operator trail when they are given.

It supersedes the [25 September plan](../2026-09-25/next-release-plan.md) in its numbering only.
That plan's 0.2.0 was upload, which the operator returned to the release on 16 October 2026 (finish line decision 24), so the releases after it move up one place and deletion moves ahead of copy.
The principle is unchanged: none of the deferred capabilities is unbuilt, returning one is removing names from the [ADR 0052](../../adr/0052-v1-capability-gate.md) table, and the work is the safety proof the deferral stood in for.

## Where master stands on 27 October 2026

**A build offers** browse, inspect, download, upload including an acknowledged replacement, object and container metadata editing, container creation with a storage policy, prefix-marker creation, the container presets of [ADR 0090](../../adr/0090-container-presets.md), a verify-before-save action and a first-run welcome under [ADR 0091](../../adr/0091-verify-action-contract.md), an account rail that groups and filters many bookmarks, and listings that refresh after a job.

**Built, tested and refused behind the gate**, each with a trial token under [ADR 0082](../../adr/0082-operator-enabled-unproven-capabilities.md): copy and job re-run; bulk deletion of a prefix, of a container's contents or of a container; marker deletion; manifest deletion, alone or together with its segments on the force-only Backstage path, where recorded ownership is advisory and never authorises on its own; benchmark; and, with copy, the Auto, Balanced, Aggressive and Custom profiles with their per-job overrides.

**Refused independently of the gate:** Mirror, under [ADR 0020](../../adr/0020-recovery-containment.md), with [ADR 0086](../../adr/0086-mirror-deletes-what-the-source-does-not-have.md) as its proposed contract.

**Built behind trial tokens since this record was written:** deleting one selected object (P27, [ADR 0092](../../adr/0092-exact-object-deletion.md)) and deleting the selected objects as one job with the multi-selection it needs ([ADR 0093](../../adr/0093-selected-set-deletion.md)), both behind `object-delete`, and marker and manifest deletion made return-ready ([ADR 0094](../../adr/0094-marker-and-manifest-deletion-return-ready.md)) behind `marker-manifest-delete`.

**Not built:** directory upload; modern versioning management, which [ADR 0054](../../adr/0054-versioning-preflight-authority.md) keeps read-only while legacy versions and history locations are already editable; TempURL and CORS management, which are read-only; Keystone authentication, since the session speaks TempAuth v1 only; the encrypted vault fallback of [ADR 0006](../../adr/0006-secret-storage-linux.md); pause and resume (finish line decision 17); bookmark overrides and the adaptive back-off of K-PERF-003; job history export, support diagnostics and backup and restore (P28); the restart disclosure of [ADR 0088](../../adr/0088-interrupted-job-disclosure.md), whose recording half landed and whose notice has not.

**Release state.** 0.1.0 is not tagged: it waits on the operator's run of the [0.1.0 test plan](../2026-09-24/0.1.0-test-plan.md) and its results file, as [RELEASING](../../RELEASING.md) requires, against the Linux packages built from master `2928da48`.
Every artefact is unsigned by decision, v1 supports Linux only (finish line decision 1), and the macOS and Windows builds are development builds without acceptance.
The valuable-data release hold is in force.
What holds it is the artefact and deployment evidence that P31 to P35 of the [remediation plan](../2026-09-05/remediation-plan.md) require and the durability residual the [register reconciliation](../2026-10-18/register-reconciliation.md) names, not the rows of the register: among the twenty-three rows it examined, no outstanding clause names a known way for this build to lose data in ordinary operation.
The durability residual is consolidated and bounded as of 3 November 2026 by the [durability-limits record](../../experiments/2026-11-02-durability-limits.md) and the [register clause resolutions](../2026-11-02-register-clause-resolutions.md): the power-loss contract and the abrupt-crash recovery and disclosure components are evidenced on Linux, and what remains of it is the end-to-end desktop-start-after-crash measurement, within the fsync-honesty and non-Linux boundaries those records state; that measurement is deferred to nearer 1.0.0 by operator decision on 4 November 2026.

## What 1.0.0 means here

Proposed, for the operator to accept or amend.
1.0.0 is the release that satisfies the definition of done in [DESIGN §63](../../DESIGN.md), or has each remaining §63 item re-scoped by the operator in the finish line's trail; that lifts the valuable-data release hold on the evidence the remediation plan's acceptance gates require; that supports the six targets of K-PLAT-003 in [REQUIREMENTS](../../REQUIREMENTS.md), or records the operator's narrowing; and whose artefacts are signed, or whose unsigned state is the operator's recorded decision.
Every release below moves one of those four conditions or is work the operator asked for.

## The sequence

### 0.1.0 — the current candidate

Owed by the operator: steps 1 to 6 of the test plan, recorded one row per step in `0.1.0-test-results.md` beside it.
A failed step is a finding for the author before the tag.
Owed by the author: nothing new; whatever the test plan surfaces is fixed through the normal review and delivery.
The tag workflow then builds the six 0.1.0 artefacts.

### 0.2.0 — deletion

Why first: the operator met its absence on the first day of testing, and a workbench that cannot delete an object cannot be exercised end to end.
Deletion carries the most hazards of any capability, which is why the 25 September plan placed it last; bringing it forward does not shorten its proof, it only starts it sooner.

1. **Exact-object deletion**, which did not exist when this plan was written and which the remediation plan says must never be substituted by a prefix deletion (P27, UI-08).
   The shape: select an object, Delete, confirm.
   The request is bound to the identity the operator saw and refuses when the name no longer holds it, which is the fixed-point comparison [ADR 0085](../../adr/0085-reobserved-destructive-identity.md) already applies to manifests; it is manifest-only, so segments stay; the plain, empty, link and manifest identity transitions and same-prefix siblings P27 names are its test matrix, and its failure streams follow L04.
   It is a new bridge action, so its contract row and preservation baseline take the [ADR 0069](../../adr/0069-preserve-covered-scenario-cells.md) drift route as the verify action did, and it is refused under the gate until its proof is in, trialled with a token, then lifted.
   The Delete key opens it, as §4.4 of the design has always said.
2. **Selected-set deletion.**
   Multi-selection of the loaded rows, as ADR 0072 decision 3 records, and one confirmation that names what it will delete; each name is an exact deletion and no prefix is inferred from a selection.
   Its record is [ADR 0093](../../adr/0093-selected-set-deletion.md), proposed and built behind the `object-delete` token on 27 October: one job over the selected objects, each member deleted by the verified per-name protocol, prefix rows excluded, and the member count typed as the confirmation.
3. **Marker and manifest deletion return**, reversing ADR 0052 decision 7 on the operator's word.
   Manifest-only deletion is already bound by ADR 0085, and the two actions that return here are `MutateService.DeleteMarker` and `MutateService.DeleteManifest`, the pair the gate classifies as marker and manifest editing.
   Deleting a manifest together with its segments is a different pair, classified with bulk delete: `DeleteManifestWithSegments` is force-only with typed confirmation, recorded ownership is advisory because no live reverse-reference scan exists, and its dynamic large object branch is the prefix sweep itself.
   One admission covers both of its branches, so it returns with bulk deletion in 0.2.x rather than here, and a cleanup authorised by ownership alone would need the exclusivity proof K-SEG-004 requires and is not proposed.
   What the two returning actions must be before their return can be asked for is [ADR 0094](../../adr/0094-marker-and-manifest-deletion-return-ready.md), proposed on 27 October: the caller's scope and the durable facts for both, the marker compared and confirmed instead of deleted by one click, the manifest protocol aligned with the plain object's, and public proof on both fixtures.
4. **Bulk deletion of a prefix or a container**, proposed as 0.2.x so that exact deletion is not held behind the hardest proof.
   It needs the prefix protocol ADR 0085 records as owed, for the listing-to-request race and names first listed in a later pass; L03's allowed-effect contract; and L04's deletion failure streams.
   Its record is [ADR 0095](../../adr/0095-prefix-deletion-by-the-per-name-protocol.md), accepted and implemented on 28 October: the selected set's verified per-name protocol run over a listing pass, the members durable items in the job item ledger admitted a listing page at a time, one request attempt per name per job marked before its final HEAD, every outcome retained with its identities, and the manifest-with-segments deletion made a job that owns its manifest and segment facts.
   Automatic segment reclamation stays refused apart from [ADR 0081](../../adr/0081-upload-overwrite-acknowledgement.md) decision 5, because the DS-01 hazard is a recovery sweep deleting segments an unrelated live manifest still references.

Benchmark stays deferred; it writes real objects and bulk-deletes them, so it follows 0.2.x.

### 0.3.0 — copy and job re-run

Both copy routes are bound: the staged route under [ADR 0042](../../adr/0042-source-content-ladder.md) and the native route under [ADR 0083](../../adr/0083-native-copy-content-binding.md) as amended by [ADR 0084](../../adr/0084-held-manifest-copy.md).
Owed: [ADR 0087](../../adr/0087-copy-overwrite-on-upload-terms.md), which lifts the transfer-overwrite refusal for copy on the terms upload has, reviewed and then implemented; L06 replication fidelity (P17), so that metadata, content headers, expiry and link targets are never reported as equivalent replication when they differ; lifting the gate for copy and re-run; and copy steps in the test plan.

### 0.4.0 — what a restart lost, complete job records, and durability evidence

ADR 0088 slice 2: the bounded read of undisclosed interrupted jobs, the notice at startup and its dismissal, with the new bridge action taking the drift route.
P28 for UI-09 and UI-10 is delivered — operation counters, transaction identifiers, complete failure access (the job-item records page through every retained item), account-bound audit views, and the retention and export policy — and so is the presentation clause that was owed when this was written: the jobs drawer shows each job's recorded configuration, with where each value came from wherever the job recorded it (today a copy), and says "not recorded" for a settings record that is missing or malformed rather than showing defaults.
Three further items were settled by operator decision on 4 November 2026, recorded in the [finish line](../2026-09-05/linux-finish-line.md): support diagnostics are the native About dialog plus a levelled application log ([ADR 0099](../../adr/0099-application-log.md)) in lieu of a packaged diagnostics bundle; backup and restore is of the configuration only ([ADR 0098](../../adr/0098-settings-backup-and-restore.md)) — the application settings and bookmarks, never credentials — which re-scopes the "database and the keychain items" wording; and the durability measurement below is deferred.
The durability residual is now bounded to the recorded fsync and power-loss limits (P12 and P28) and the implemented, component-tested crash-recovery behaviour (ADR 0088 slice 2 and the startup `FailInterrupted`); its one remaining piece, a desktop start recovering from an abrupt in-flight crash measured through the real binary, is deferred to nearer 1.0.0 by the same decision as the end-to-end measurement receipt rather than missing behaviour.
The three clauses the register reconciliation could not establish are resolved here, one of which, shutdown ownership of credential publication and cleanup, is on an enabled path today.

### 0.5.0 — account and container features

Versioning (P26): the container editor already switches legacy versioning off or to a versions or history location with its archive container, and refuses an edit whose preflight is unestablished.
[ADR 0102](../../adr/0102-versioning-matrix-fixed-point-and-archives.md) closed the item's remaining asks on 9 November 2026: the legacy matrix is measured on a genuine Swift through the product's own routes and renewable with the real-cloud oracle, the modern mode is measured on the fixture and stays display-only, an upload or copy replacement is committed only under the versioning observation it was authorised under, and the bulk delete preview names the containers that archive into the one being emptied.
TempURL keys and CORS management, delivered under [ADR 0101](../../adr/0101-tempurl-key-slots-and-cors-editing.md).
Directory upload ([ADR 0103](../../adr/0103-directory-upload.md)) and account metadata (P27), delivered.
Pause and resume (finish line decision 17), delivered under [ADR 0104](../../adr/0104-pause-and-resume.md).
Raising the active-job budget: four at most and two by default, delivered under [ADR 0105](../../adr/0105-concurrent-jobs.md).
[ADR 0078](../../adr/0078-overlapping-download-destinations.md) had made two downloads safe, and ADR 0105 decides what keeps any two running jobs apart.
Keystone authentication is here by finish-line decision 31: a bookmark authentication type beside TempAuth v1, the session's auth version and a Keystone-shaped fixture for its proof, kept cheap because the engine never speaks to Keystone (K-SEC-001); the vault fallback belongs here if the operator decides it is in 1.0.0.

### 0.6.0 — performance

Measured workload bounds and fairness (P24 and P25), the benchmark's return, bookmark overrides, per-job settings on the local transfer dialog, and the adaptive back-off of K-PERF-003.
The Auto, Balanced, Aggressive and Custom profiles and their per-job overrides already exist behind the copy affordance and return with copy in 0.3.0; Auto's stored recommendation needs the benchmark.
Recommendation: re-scope the benchmark and the adaptive back-off to 1.x unless the operator needs them, since §63 lists the benchmark, DESIGN §41 and K-PERF-003 require the back-off, and only the operator can take them out.
Per-job settings on the local transfer dialog, delivered: an upload or download takes the profile and the copy dialog's overrides, a download leaving out the chunk size it never uses, and records each value with its origin as a copy does.

### 0.7.0 — platforms and distribution

macOS and Windows acceptance, which finish line decision 1 left as development builds.
The Windows native-question defect found in review on 16 October 2026: the pinned Wails beta dispatches a message-box answer only to a callback whose label matches a stock button, and both of this project's native questions use custom labels, so on Windows the public-ACL and overwrite questions wait until their context is cancelled.
Signing and notarisation, for which the keys are the operator's; the workflow marks where each tool slots in.
P32 installation, upgrade and rollback on the advertised distributions, Fedora current stable and Ubuntu current LTS.
Which Linux formats 1.0.0 publishes: the rpm, deb and tarball ship today, `make package` builds an AppImage the release workflow does not publish, and Flatpak is gated on the keychain and sidecar behaviour (§61.1).

### 1.0.0 — the hold lifted

P31 advisory dispositions for the shipped sidecar, which finish line decision 8 unblocked for v1 only.
P33 identity on the native About dialog and the installed packages, P34 the exact-commit release gate, and P35 documentation consistency (DOC-01).
P29 accessibility and layout (UI-11, UI-12, UI-13) with native assistive-technology evidence.
The representative deployed-Swift session of §57.4, which only the operator can run.
Mirror decided: either ADR 0086 is accepted and built, or 1.0.0 records Mirror as outside it.
The recommendation is outside: a mirror's deletes are justified by an absence, and the store offers nothing that makes that safe without an external-writer guarantee.
The table below at every row either shipped or re-scoped.

### Alongside every release

The hold's evidence work (P31 to P35) does not wait for features and runs in parallel.
The engine behaviour investigation the operator raised on 27 September 2026, for gotchas of the `leave_parts_on_error` kind that the project has never used or exposed.
Dependency policy: the pin on a Wails 3.0.0 beta and the muting of its remaining betas, decided again at 1.0.0.
Stale status lines fixed as they are met: ADR 0045 and ADR 0077 as the reconciliation records, ADR 0082 which reads Proposed though its switch is implemented and proven from the shipped binary, and ADR 0088 which reads as unbuilt though its recording half landed on 26 October 2026.

## Outstanding features against DESIGN §63

One row per item of the definition of done, with its state on master and the release that carries it.
Shipped means offered by a build; gated means built and refused under ADR 0052; not built means no code path exists.

| §63 area | Item | State | Release |
| --- | --- | --- | --- |
| Browse | save multiple bookmarks | shipped, TempAuth v1 and Keystone v3 (decision 31, ADR 0100) | 0.5.0 |
| Browse | browse containers without loading the account namespace | shipped | — |
| Browse | browse very large containers page by page | shipped | — |
| Browse | navigate marker-backed and implied prefixes | shipped | — |
| Browse | switch Logical, All and Backstage | shipped | — |
| Inspect | account, container and object headers | shipped | — |
| Inspect | derived, system and user metadata separately | shipped | — |
| Inspect | ACLs and configuration | shipped | — |
| Inspect | identify DLOs and SLOs | shipped | — |
| Inspect | exact SLO segments | shipped | — |
| Inspect | jump to confirmed backstage storage | shipped | — |
| Inspect | confirmed, declared and suspected segment stores | shipped | — |
| Mutate | edit object metadata without dropping keys | shipped | — |
| Mutate | edit DLO metadata without breaking the manifest | shipped | — |
| Mutate | edit container and account metadata | shipped | 0.5.0 |
| Mutate | edit container ACLs | shipped, with the public-read acknowledgement | — |
| Mutate | create containers with the correct policy | shipped | — |
| Mutate | configure supported container features | presets, quotas, legacy versioning locations, TempURL keys and CORS shipped (ADR 0101, ADR 0102); modern versioning read-only | 0.5.0 |
| Mutate | meaningful diffs and conflicts | shipped | — |
| Transfer | upload | shipped, with acknowledged replacement | — |
| Transfer | download | shipped | — |
| Transfer | upload directories and multiple files | shipped (ADR 0103) | 0.5.0 |
| Transfer | copy across bookmarks | shipped; returned on 31 October 2026 (ADR 0052 decision 12) | 0.3.0 |
| Transfer | mirror | refused under ADR 0020 | decision at 1.0.0 |
| Transfer | progress, throughput and worker allocation | shipped: progress, throughput, and each job's recorded settings, with their origins where recorded (a copy, and an upload or download from 0.6.0) (UI-09) | 0.4.0 |
| Transfer | cancel | shipped | — |
| Transfer | understand destination large-object placement | shipped: the local transfer dialog names the destination DLO placement for an upload that segments, as the copy preview does behind the gate; chunk sizing and collision preflight shipped | — |
| Delete | delete ordinary objects | shipped; returned to the release on 29 October 2026 (ADR 0092, ADR 0052 decision 11) | 0.2.0 |
| Delete | delete the selected objects as one job | shipped; returned on 29 October 2026 (ADR 0093, ADR 0052 decision 11), with multi-selection of the loaded rows | 0.2.0 |
| Delete | remove markers | shipped; returned on 1 November 2026 (ADR 0094, ADR 0052 decision 13) | 0.2.0 |
| Delete | recursively delete normal prefixes | shipped; returned on 1 November 2026 (ADR 0095, ADR 0052 decision 14) | 0.2.x |
| Delete | empty containers | shipped; returned on 1 November 2026 (ADR 0095, ADR 0052 decision 14) | 0.2.x |
| Delete | delete SLO manifests | shipped; returned on 1 November 2026 (ADR 0094, ADR 0052 decision 13) | 0.2.0 |
| Delete | delete SLOs with exact segments | shipped with bulk delete, force-only with typed confirmation; ownership advisory; a job (ADR 0095, ADR 0052 decision 14) | 0.2.x |
| Delete | delete DLO manifests | shipped; returned on 1 November 2026 (ADR 0094, ADR 0052 decision 13) | 0.2.0 |
| Delete | clean known-owned DLO segments | shipped, prefix-scoped and force-only; ownership advisory; a job (ADR 0095, ADR 0052 decision 14) | 0.2.x |
| Delete | deliberate Backstage cleanup | shipped with bulk delete, force and typed confirmation (ADR 0052 decision 14) | 0.2.x |
| Performance | global resource budgets | the active-job budget (ADR 0105) and the bounds of ADR 0074 are settings; the whole-application worker and memory ceilings are not built | — |
| Performance | bookmark overrides | not built | 0.6.0 |
| Performance | per-job overrides | shipped with copy, and on the local transfer dialog with the profile and the same overrides | 0.3.0 with copy; 0.6.0 for local transfers |
| Performance | Auto, Balanced, Aggressive and Custom | shipped with copy; Auto's recommendation needs the benchmark | 0.3.0 with copy |
| Performance | benchmark a bookmark | gated | 0.6.0 or re-scoped |
| Performance | report throughput and effective configuration | shipped: throughput and recorded settings, with their origins where recorded (a copy, and an upload or download from 0.6.0) (UI-09) | 0.4.0 |
| Performance | choose among native and engine strategies | shipped | — |
| Safety | the eight prohibitions | enforced by refusals and maintained tests | — |

Beyond §63, the operator has asked for or the records owe: selected-set deletion (0.2.0), the restart notice (0.4.0), export and backup (0.4.0), pause and resume (0.5.0), Keystone (0.5.0, decision 31) and the vault (a decision), signing and the six targets (0.7.0), and the Linux formats (decision).

## Decisions this plan needs from the operator

1. The sequence itself: accept it, or reorder it.
   Given on 27 October 2026 as finish-line decision 31: delivered through the Linux half of 0.7.0.
2. The deletion split: exact-object, selected-set and the return of marker and manifest deletion in 0.2.0, with bulk deletion of a prefix or container in 0.2.x once its protocol is written.
3. Mirror: in 1.0.0 under ADR 0086, or recorded as outside it (recommended).
4. Keystone authentication: in 1.0.0, or kept as the v2 candidate the design lists.
   Given on 27 October 2026 as finish-line decision 31: in 1.0.0, as part of 0.5.0.
5. Platforms: all six targets as K-PLAT-003 requires, which needs macOS and Windows machines for acceptance, or a recorded narrowing.
   Given on 27 October 2026 as finish-line decision 31: Linux first, as a complete release; macOS and Windows acceptance wait for hardware.
6. Signing: whose keys, and whether 1.0.0 ships signed.
7. The Wails 3.0.0 beta pin: ship 1.0.0 on it, or wait for a stable 3.0.0.
8. The benchmark and the adaptive back-off: 1.0.0 or 1.x.
9. The vault fallback for hosts without a Secret Service: 1.0.0 or 1.x.
10. Linux formats: whether AppImage or Flatpak join the rpm, deb and tarball.

## What this plan does not do

It accepts no capability, lifts no refusal, closes no finding and fixes no dates.
Each release's records carry their own decisions and their own proof, and the valuable-data release hold is lifted only by the evidence the remediation plan's acceptance gates name.
