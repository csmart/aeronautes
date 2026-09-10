# Remediation plan

This plan addresses the [5 September 2026 audit](README.md) of `cc6465ac69ec00756d9976bbf6cf7531b07da66d` and the still-open DESIGN §65.10 W/R backlog.
The owner authorised remediation and, on 8 September, made **safe operation on valuable data** the first milestone.
The current disposition is recorded against delivered master `ae3ecd4962cbbc830d8e9f30f42aabc92c45364e`, including [PR52's bounded upload fix](../../experiments/2026-09-08-upload-recovery.md#delivery).
The domain reports preserve audit-time defects and experiments; the [finding register below](#finding-to-plan-coverage) distinguishes delivered fixes, containment, remaining safety blockers and later work.
An implementation-order change does not accept a new locked product decision, close a finding or waive a release requirement.
Keep the existing valuable-data release hold until the safety and release gates below have evidence.
Implement through the project's commit/review discipline, with a catching regression for every fix and an independent system reproduction once the relevant harness layer exists.

## Execution order for valuable-data safety

P09's endpoint/redirect fixes, core P10 credential/session safeguards, P02's snapshot corrections, the minimum P03 HTTP seam, P04/P05 containment, P06–P08's defined UI/Find fixes and P22/P23 are present in this master.
P10 and P03 still have the residual obligations listed below; delivered slices do not complete their wider packages.
The latest full local evidence is 594 top-level Go tests and 1,439 leaf cases without skips, plus 850 frontend tests.
The [same evidence record](../../experiments/2026-09-08-upload-recovery.md#maintained-implementation-and-evidence) reports valid scenario reconciliation of 32/110 cells with 38/49 actions still gapped.
These are dated observations, not a percentage of safety achieved or a passing release gate.

1. **Continue P12/P13, with bounded P21 upload callers.**
   Start by reproducing terminal job/audit persistence failures and incomplete failure reporting on current master, including earlier transfer failures followed by a later error without a post-commit issue.
   Review the smallest durable intent/outcome contract before changing storage or migrating sibling paths.
   Split intent admission, terminal outcomes, audit persistence and Copy/upload migration into coherent reviewed commits; retain exact identities beyond display samples and account for unknown effects after lost responses.
   The already delivered upload error summary is a prerequisite, not an exact durable outcome ledger.
2. **Bring authority, verification and filesystem prerequisites alongside their first dependent effect.**
   P10/P11 own native credential/recovery authority and backend-held plans; P16/P17 own current-identity verification and replication fidelity; P18/P19 own selected-root and full-path identity.
   A bounded persistence/reporting fix may proceed under retention, but cannot enable reclamation, retry, placement or mutation whose prerequisite remains unproved.
   Record any missing local-filesystem, concurrency or capability decision before its dependent code.
3. **Complete the enabled mutation and cancellation paths after those prerequisites.**
   P15/P20/P21/P26 must prove exact destructive scope, protection of shared backing data under the accepted authority contract, local placement, upload completeness and supported versioning semantics, using P14 where an enforceable concurrent-writer protocol is required.
   Keep Mirror starts, existing-destination transfer overwrites and automatic recovery reclamation refused under ADR 0020.
   Restoring those capabilities has a separate evidence gate; current refusal tests and the safety of still-enabled sibling paths remain mandatory.
4. **Attach supporting safety work to these slices.**
   P03/P34 fault and regression gates, P24 resource bounds, P28 truthful outcomes, P29 safe keyboard/focus/labels and P30 child quiescence must arrive when the affected operation needs them.
   Resource exhaustion, hidden cancellation failure, misleading scope, unreadable confirmation and a child writing after its parent dies are safety concerns regardless of their original phase label.
   Extend the harness for a concrete required scenario, keeping process kill, orderly reopen, modelled history loss and native-platform evidence distinct.
5. **Close the artifact and deployment gates before claiming valuable-data readiness.**
   P31–P35 must establish current dependency dispositions, the accepted engine, exact source/artifact identity, installation and recovery procedures, required native journeys and representative deployed-Swift evidence.
   Native work and advisory triage may start earlier when their prerequisites are available.
   Linux is the first implementation platform; the accepted six-target requirement and every advertised operation remain unchanged.
6. **Sequence broader product work after the relevant safety foundations.**
   Throughput optimisation, additional convenience/layout work and absent capabilities in P25/P27 follow their safety prerequisites.
   The register identifies the parts that can wait because they add capability or convenience without supplying a safety guarantee to an enabled path.
   They retain their accepted v1 gates; a narrower product/platform scope requires its own explicit DESIGN/ADR decision.

This is a dependency order, not a requirement to finish an entire package before any independent bounded fix.
The phase tables below retain their IDs and detailed acceptance tests; they no longer describe a fresh start at P09.
Progress reports should name the remaining safety blockers and their missing evidence, separately from delivered containment and later product work.

## Decisions to record before dependent code

ADR 0015 already governs credential publication and WAL/FULL settings, and ADR 0020 accepts conservative recovery and transfer refusals.
Those decisions remain in force while the stronger protocols and the other decisions below are completed; the table does not reopen their accepted containment policy.

| Decision | Evidence/problem | Safest interim behaviour | Needed outcome |
| --- | --- | --- | --- |
| External-writer concurrency | Final HEAD→PUT/COPY/DELETE races, DS-02/03; unsupported conditional DELETE in recorded Swift evidence | Refuse identity-sensitive destructive work where the stated guarantee cannot be enforced | An ADR naming supported server/workflow primitives, exclusive-writer assumptions if any, and precise refusal/conflict behaviour |
| Segment deletion authority | DS-01/SEC-03; §28.6 scan prohibition conflicts with later exclusivity demands | Preserve uncertain residue; retain force-only explicit cleanup; do not auto-reclaim based only on a generated prefix | A consistent ownership/reference/concurrency contract; a scan alone must not be called permanent exclusivity proof |
| Durability boundary | WAL NORMAL, remote success plus lost local facts, ignored persistence errors | Retain ambiguous state and report unresolved outcomes | Which intent/commit/outcome facts must survive process crash versus OS/power loss, and what synchronization makes them durable |
| Hostile-frontend protection | SEC-01/07; frontend booleans and reusable picker/plan tokens | No silent credential retarget; no broadened authority from caller-supplied confirmation | Trusted native approval and backend-held capability contracts, including legitimate credential migration |
| Supported local namespace | DS-05/06, known Windows reserved-name gaps | Reject unsafe/ambiguous paths before writes | Supported filesystems, case/normalization/reparse-point rules, full-plan rejection semantics and root identity |
| Distribution/security updates | SEC-11/12; strict engine pin versus unversioned packages | Keep checksum/version enforcement and visible transfer unavailability | Supported distro envelope or managed sidecar policy, urgent patch ownership and compatibility evidence |
| Remaining v1 capabilities | UI product matrix and DESIGN §63 | Label unavailable features accurately | Deliver or explicitly re-scope each promised capability through DESIGN/ADR; preserve stated non-goals |

These decisions are required for implementation, not for recording this audit.
Do not invent a concurrency primitive or weaken an oracle to avoid a decision.

## Ordered work packages

The IDs below make dependencies and finding coverage explicit.
Each implementation commit should solve one logical problem; split a package's independent paths/platforms into separate commits when that makes review clearer.
Do not combine a phase into one commit.
Acceptance tests named here add to the detailed tests in each finding.

### Phase A — trustworthy reproductions and containment

| ID | Work and dependency | Completion evidence |
| --- | --- | --- |
| P01 | Record the concurrency, ownership and durability contract/containment decisions above; reconcile conflicting DESIGN paragraphs and add the governing ADR | Reviewable allowed/forbidden effect table; tests can name one unambiguous oracle; no claim that a scan or final HEAD establishes atomicity |
| P02 | Correct independent snapshot encoding semantics (TEST-01); independent of P01 | Preserved gzip/plain probe fails before and passes after; stored headers and chosen byte representations remain separately observable |
| P03 | Add typed deterministic HTTP barriers and response-loss faults to the existing test seams; use P02's oracle | DS-01–04 and SEC-02 interleavings are reproducible without sleeps selecting the race; bounded waits/timeouts and cleanup have negative tests |
| P04 | Contain recovery reclamation (DS-01, SEC-03), after P01 | An outside live alias and malformed/missing provenance cause retention with durable visible residue; startup and in-process reconciliation cannot clear unresolved records |
| P05 | Contain unsupported final-hop overwrite/Mirror deletion (DS-02/03), after P01/P03 | Competing writes after the last HEAD survive; unsupported operation is refused before irreversible work; no unqualified success or hidden partial completion |
| P06 | Bind UI preview completion to the exact current request and dialog lifetime (UI-01), independent of the later server plan protocol | Change destination, source, mode, prefix or settings while Preview is pending; close/reopen; late result never arms Start |

Phase A is complete when the dangerous paths are either demonstrably safe under an explicit contract or visibly unavailable under that contract.
A warning or typed confirmation alone does not satisfy the containment gate.
P04/P05 containment is now implemented under [ADR 0020](../../adr/0020-recovery-containment.md); the original audit alone did not change operation availability.

### Phase B — identity, credentials and authority

| ID | Work and dependency | Completion evidence |
| --- | --- | --- |
| P07 | Bind asynchronous mutation completions and bookmark lifecycle to current UI identity/generation (UI-02/05), building on P06's intent model | Save A/select B/resolve A cannot populate B with A's details; edit/delete active bookmark clears invalid state and presents reconnect/cleanup outcomes |
| P08 | Isolate pagination and Find lifecycle (UI-03/06; PERF-01 scan retention); separate paging and scan fixes into their own commits | Late pages never cross namespaces; duplicate loads do not duplicate rows; Stop before StartFind returns cancels the returned scan; navigation/expiry frees retained scans |
| P09 | Enforce one endpoint/redirect trust policy on native and real-engine paths (SEC-02/05), with a separately reviewed engine change if needed | Returned plaintext escalation fails; real rclone 301/302/303/307/308 matrix sends no token to redirect targets; supported custom-CA/insecure policy remains scoped |
| P10 | Bind stored credentials to approved origin/trust/account identity and make reauth/session acquisition atomic (SEC-01/04; W-05/W-15) | Retarget probe passes; rejected tokens cannot survive refresh; token/URL/expiry/trust/config generation are one coherent snapshot; SQL/keychain partial failure leaves no deleted active authority |
| P11 | Implement backend-held immutable reviewed plans and narrowly scoped one-shot capabilities (W-01/W-10/W-17; SEC-07), after P01/P10 | Direct bridge calls cannot change operation, account, prefix, selected local resource, force/public-ACL approval or expected facts; expired/replayed tokens refuse; queued starts use the reviewed generation |

P11 must bind preview and execution but must not be presented as a fix for P05's external-writer race.
Use trusted native acknowledgement only for actions whose accepted threat model requires protection from a compromised renderer.
The plan does not add approval prompts to ordinary harmless browsing.

### Phase C — durable effects, exact outcomes and verified replication

| ID | Work and dependency | Completion evidence |
| --- | --- | --- |
| P12 | Implement durable safety facts, conservative recovery and shared cleanup policy (SEC-03; DS-01/10; W-09), after P01/P04 | Required facts are durably synchronized before effects; DB/fsync/list/delete failures retain exact residue; lost-suffix history and real restart tests preserve every live alias |
| P13 | Persist identity-keyed outcomes and an intent/outcome audit record (DS-08; SEC-08), after the P12 contract | One failed name counts once; 201+ failures retain every identity; malformed/unordered bulk responses become exact known/unknown outcomes; spill/DB failures remain recoverable and visible |
| P14 | Implement the accepted native commit/delete concurrency protocol, then re-enable only the supported operations (DS-02/03), after P05/P11/P12 | Final-hop races across plain/DLO/SLO/symlink, source-absence changes, landed-but-lost responses and retry preserve permitted identities and report exact outcomes |
| P15 | Complete segment/destructive identity rules and local-job scope coordination, after P01/P11/P13 | Explicit force prefix cannot widen into a sibling; empty-DLO manifest swaps are detected beyond equal ETag; another job's stage is protected; nested/shared SLO/DLO collateral bytes remain unchanged |
| P16 | Define and enforce verification strength and current identity binding (DS-04; W-07), before Mirror can rely on copy completion | Same-size corruption/permutation/stale fingerprint/missing segments/source mutation fail; ordered inventories or explicit byte digests establish the required strength; weak/unknown results block destructive follow-up |
| P17 | Distinguish content equality from replication equality; complete metadata-only and symlink fidelity (DS-09; W-12), after P16 | User metadata, content headers, expiry, link target/account and dangling links follow the declared semantics without needless content transfer; changed destination remains a conflict |

P12/P13 should introduce the smallest shared contract needed by Copy and local upload, then migrate sibling paths in separate reviewed commits.
The [current-master Copy failure-identity reproduction](../../experiments/2026-09-08-copy-failure-identities.md) supplies eight actual-service cases before that design: one, 201 and 401 failed names become two, 400 and 400 reported failures respectively on both providers, while only the first name appears in existing job/audit summaries.
Its six intended failures and two passing controls preserve the successful engine copy, complete owned fixture namespaces and orderly database reopening; exact records, unavailable-state handling and the schema/API contract remain design and implementation work.
[ADR 0028](../../adr/0028-transfer-item-records.md) defines the independently reviewed shared logical item-record storage contract, with a separately reviewed foundation followed by concrete Copy adapter/UI and local sibling migrations.
It does not claim a per-HTTP-effect journal, safe retry, immutable remote versions, native approval, exclusive ownership or a completed durability/valuable-data gate.
The [storage foundation](../../experiments/2026-09-08-transfer-item-storage.md) adds the versioned records, complete pages, exact failed-name totals, guarded terminal snapshots and retention checks, with SQL-refusal and orderly child-reopen evidence and sixteen rejected broken controls.
Existing callers remain unrecorded until separately migrated; the complete item-access UI and interrupted-job projection remain required before claiming their outcome gap fixed.
Independent local review and all five PR/master checks passed for [PR58](https://github.com/csmart/aeronautes/pull/58); its [delivery record](../../experiments/2026-09-08-transfer-item-storage.md#delivery) confirms the exact reviewed tree and four preserved logical commits.
The shared storage foundation is delivered; the Copy/local caller migrations, complete item-access UI, restart projection and valuable-data hold remain open.
The [listing identity reproduction and maintained fix](../../experiments/2026-09-08-listing-fidelity.md) address a P22 prerequisite before Copy item admission: malformed Unicode and duplicate identity fields reached native copies and a complete result.
[ADR 0029](../../adr/0029-listing-identity-decoding.md) defines the independently reviewed raw-row contract; the decoder rejects malformed Unicode, duplicate members and colliding identity aliases with complete-page refusal, valid-name controls and a third checked Swift-source patch.
Independent local review and all five PR/master checks passed for [PR59](https://github.com/csmart/aeronautes/pull/59); its [delivery record](../../experiments/2026-09-08-listing-fidelity.md#delivery) confirms the exact reviewed tree and three preserved logical commits.
This delivers the native listing prerequisite; it does not migrate the Copy recorder or close P12/P13/P22.
The Copy adapter plan must still state the full recording-latch scope, validate constructed locations before admission, define comparison-only failure/conflict mapping and project interrupted/unresolved results into the UI and rerun refusal.
The [current-master replay](../../experiments/2026-09-08-copy-failure-identities.md#replay-before-the-copy-caller-migration) reconfirms all eight original identity cases after the storage and listing deliveries, with six intended failures, two controls and unchanged application source.
[ADR 0030](../../adr/0030-copy-item-recording.md) specifies those concrete adapter decisions and the separate service/UI, interruption/rerun and checked Copy implementation sequence for independent review before caller opt-in.
Complete that sequence before claiming Copy has retained exact outcomes; local migrations and the wider P12/P13/P28 and valuable-data gates remain open.
The first [service/UI access slice](../../experiments/2026-09-08-transfer-item-access.md) now implements complete bounded history and item navigation, exact quoted identities, overlapping facets and explicit record availability.
Its evidence comes from constructed version-one records through the actual read service and drawer; actual transfer callers remain version zero, with interruption/rerun and checked recording still to follow.
Independent local review and all five protected PR/master checks passed for [PR60](https://github.com/csmart/aeronautes/pull/60); its [delivery record](../../experiments/2026-09-08-transfer-item-access.md#delivery) confirms the exact reviewed tree and five preserved logical commits.
Complete bounded access is delivered; interrupted-result projection, rerun refusal, checked Copy and local caller migration remain required before closing their outcome gaps.
The [interruption/rerun slice](../../experiments/2026-09-08-interrupted-item-records.md) now implements distinct startup failure totals, last-saved progress labels, atomic projection refusal and unavailable-store latching, plus complete-record rerun guards before admission or connection authority.
Maintained before/after system cases, mock and real-Swift namespace checks and UI refusal tests cover that bounded change; checked actual caller recording, the full P12/P13/P28 outcomes and valuable-data gates remain open.
Independent local review and all five protected PR/master checks passed for [PR61](https://github.com/csmart/aeronautes/pull/61); its [delivery record](../../experiments/2026-09-08-interrupted-item-records.md#delivery) confirms the exact reviewed tree and two preserved logical commits.
Interrupted-result projection, guarded reruns and checked Copy recording are delivered; their later local caller work is recorded below.
The [checked Copy implementation](../../experiments/2026-09-08-checked-copy-records.md) now opts new Copy jobs into complete logical records and atomically derives distinct failed names and audit facets.
Actual mock and SAIO regressions retain every failure identity and owned collateral, stop subsequent work after required recording refusal, and preserve uncertain DLO stages and segments after a committed-record fault.
Its full race, negative-control, frontend/build and recording-cost evidence passed independent local review.
All five protected PR/master checks passed for [PR62](https://github.com/csmart/aeronautes/pull/62); its [delivery record](../../experiments/2026-09-08-checked-copy-records.md#delivery) confirms the exact reviewed tree, three preserved logical commits and measured integration durations.
This addresses the Copy caller portion of DS-08 without closing P12/P13/P28, local caller migrations, the complete per-effect/authority/durability requirements or the valuable-data hold.
The next [upload prerequisite record](../../experiments/2026-09-08-upload-item-boundaries.md) preserves actual-service 201/401-name truncation on both providers, plus narrower local/mock recovery-stop and captured-path reproductions.
It requires a concrete upload contract and source-fidelity prerequisite before opt-in, with P18/P19/P11 and the other wider gates still open.
[ADR 0031](../../adr/0031-upload-item-recording.md) defines the independently accepted upload-specific pathname guard, checked execution/verification observations and item-derived terminal accounting, preserving exact post-commit issues and a separate download migration.
The first [source-path correction](../../experiments/2026-09-08-upload-source-paths.md#protected-delivery) is delivered and refuses ambiguous captured spellings before upload opt-in.
The [checked upload implementation](../../experiments/2026-09-08-checked-upload-records.md) now retains exact admitted paths, final typed execution/verification facts and distinct terminal/audit totals for new uploads, with checked SQL and recovery-stop boundaries.
Its maintained ordinary/native-name, 0/1/201/401-failure, closure, cancellation and DLO fixtures preserve complete owned effects and item history across orderly reopening.
The evidence record separates local validation, independent review and protected delivery; download recording and the wider per-effect, filesystem, authority and valuable-data gates remain open.
The [download prerequisite archive](../../experiments/2026-09-09-download-boundaries.md) now reproduces five boundaries: truncated failure identities, captured-root redirection, ignored native close corruption, copied bytes hidden after unlink refusal and recursive deletion of an unrelated writer's staging file.
Actual service probes run on both owned providers except the narrower POSIX placement probe, which uses the local executor and existing test engine.
The combined unchanged-source replay has seventeen expected failing leaves and seventeen passing controls, with no skips or unrelated diagnostics.
The download prerequisite contract and measured CI scheduling change have passed independent review and protected delivery before maintained corrections and download item recording.
These observations extend P13/P15/P18/P19/P20/P28; they do not close any package, scenario gap or valuable-data release gate.
[ADR 0032](../../adr/0032-download-placement-prerequisites.md) defines the independently reviewed download prerequisite contract, prioritising captured-root fidelity and protection of unrelated staging data before native closure and caller recording.
Give the root guard, engine stage lifecycle and native closure changes separate implementation reviews with full effects, collateral and current PR/master checks.
The [root-spelling correction](../../experiments/2026-09-09-download-root.md) adds actual-service refusal and accepted-path controls on both owned providers, with full local/remote effects, durable outcomes and six detected broken variants.
Its 2,402-test local race run preserves the full previous inventory; independent review and [protected PR67 delivery](../../experiments/2026-09-09-download-root.md#delivery) passed with all five PR and master checks green.
All broader package and valuable-data release gates remain open.
The [engine-stage correction](../../experiments/2026-09-09-download-stage.md) adds exclusive stage creation, conservative directory cleanup and honest post-link accounting, with actual-service fault evidence on both owned providers.
It preserves earlier job reasons and records group cleanup issues without inventing failed object identities.
Independent review and [protected PR68 delivery](../../experiments/2026-09-09-download-stage.md#delivery) passed with all five PR/master checks, exact tree equality and the full 2,453-test inventory retained.
The [native closure correction](../../experiments/2026-09-09-download-close.md) rejects placement before required source/file closure and retains simultaneous primary/cleanup errors and already-witnessed copied bytes.
Both-provider service regressions cover close corruption, raced final targets and retained temporary files.
Independent review and [protected PR69 delivery](../../experiments/2026-09-09-download-close.md#protected-delivery) passed with all five PR/master jobs, exact tree equality and the complete 2,480-test inventory preserved.
The admission follow-ups and mock scheduling prerequisite passed independent review and [protected PR70 delivery](../../experiments/2026-09-09-mock-scheduling.md#independent-review-and-protected-delivery), with all five PR/master jobs green and the complete 2,486-test terminal inventory retained.
Measured PR/master durations remain within the existing finite CI limits; download record version zero and every wider release gate remain unchanged.
[ADR 0033](../../adr/0033-download-item-recording.md) is the independently approved download recording contract for exact temporary-file admission, directory materialisation, recording/cancellation stops and complete logical outcomes.
The [maintained implementation and evidence](../../experiments/2026-09-09-checked-download-records.md) add complete checked item records to new downloads while preserving historical unrecorded jobs, helper-witnessed copied bytes and missing-record uncertainty.
Independent implementation review passed, including a second complete 2,653-test race run and acknowledgement, identity and effect checks.
Integrated-range review and [protected PR75 delivery](../../experiments/2026-09-09-checked-download-records.md#protected-delivery) passed, with all five PR/master checks green and the exact reviewed tree and five logical commits preserved.
This bounded migration closes no complete audit package or valuable-data release gate.
The [local-upload reproductions](../../experiments/2026-09-08-upload-recovery.md) confirmed P04's retained records while exposing ignored committed-record and staging-delete errors through the actual services on mock Swift and SAIO before the bounded follow-up fix.
[ADR 0023](../../adr/0023-upload-post-commit-errors.md) defines the independently reviewed first bounded P12/P21 follow-up: retain uncertainty, prevent the affected cleanup after persistence refusal and expose post-commit issues without corrupting successful-transfer counts.
The [maintained implementation and regressions](../../experiments/2026-09-08-upload-recovery.md#maintained-implementation-and-evidence) exercise both providers, exact recovery facts and collateral, child-process reopening and independent error/counter controls.
Independent local review and all five PR/master checks passed for [PR52](https://github.com/csmart/aeronautes/pull/52); its [delivery record](../../experiments/2026-09-08-upload-recovery.md#delivery) confirms the exact reviewed/merged tree and three preserved logical commits.
Its review and implementation sequence keeps the complete identity ledger, sibling Copy migration, audit atomicity, terminal-write failures and reclamation authority open.
The [terminal-persistence reproductions](../../experiments/2026-09-08-terminal-persistence.md) confirm contradictory job/audit summaries and rejected admission writes that still allow actual uploads on both providers.
[ADR 0024](../../adr/0024-local-job-completion.md) defines the next bounded shared contract: atomic final summaries, checked local admission, explicit persistence unavailability and conservative restart.
Review and implement that local slice first, then preserve earlier failures alongside later terminal reasons and migrate Copy in separate reviewed commits.
The complete identity ledger and other job/mutation families remain blocking; the process-local diagnostic latch is not durable outcome evidence or a global mutation interlock.
The local implementation has [maintained rollback, service, queue, UI and process-reopen evidence](../../experiments/2026-09-08-terminal-persistence.md#maintained-implementation-and-evidence).
Independent local review and all five PR/master checks passed for [PR54](https://github.com/csmart/aeronautes/pull/54); its [delivery record](../../experiments/2026-09-08-terminal-persistence.md#delivery) confirms the exact reviewed/merged tree and two preserved logical commits.
The [actual interruption reproductions](../../experiments/2026-09-08-local-interruptions.md) now confirm earlier failures hidden by cancellation/later errors and incorrectly labelled completed subsets on both providers.
[ADR 0025](../../adr/0025-local-interruption-outcomes.md) defines that next bounded terminal-reporting fix and its maintained regression controls before separate Copy migration.
The local implementation and [maintained evidence](../../experiments/2026-09-08-local-interruptions.md#maintained-implementation-and-evidence) now preserve earlier reported outcomes beside later reasons.
Independent local review and all five PR/master checks passed for [PR55](https://github.com/csmart/aeronautes/pull/55); its [delivery record](../../experiments/2026-09-08-local-interruptions.md#delivery) confirms the exact reviewed/merged tree and three preserved logical commits.
The [Copy completion reproductions](../../experiments/2026-09-08-copy-completion.md) recorded the sibling caller ignoring admission failures and committing contradictory terminal summaries before its migration.
[ADR 0026](../../adr/0026-copy-job-completion.md) defines its separate reviewed admission, atomic-summary and queued-cancellation migration while retaining source audit authority, destination recovery facts and overwrite refusals.
The Copy implementation has [maintained actual-service, shared-latch, atomic-audit, collateral and process-reopen evidence](../../experiments/2026-09-08-copy-completion.md#maintained-implementation-and-evidence).
Independent local review and all five PR/master checks passed for [PR56](https://github.com/csmart/aeronautes/pull/56); its [delivery record](../../experiments/2026-09-08-copy-completion.md#delivery) confirms the exact reviewed/merged tree and three preserved logical commits.
The [Copy interruption reproductions](../../experiments/2026-09-08-copy-interruptions.md) confirm that earlier copied/failed outcomes are still hidden by later cancellation or listing errors; [ADR 0027](../../adr/0027-copy-interruption-outcomes.md) defines the next separate caller correction and required controls.
Conflict details, verification-stream and simultaneous plan/Finish errors, logged close/reconcile failures and the complete identity ledger remain separate P13 outcome work; this correction does not close the wider package or valuable-data hold.
The bounded Copy interruption correction is now [delivered through PR57](../../experiments/2026-09-08-copy-interruptions.md#delivery), after independent local review and all five successful PR/master checks with the exact reviewed tree and three logical commits preserved.
The bounded Copy earlier-outcome correction now has [maintained actual-service, engine-cancellation, full-suite and broken-rule evidence](../../experiments/2026-09-08-copy-interruptions.md#maintained-implementation-and-evidence); independent implementation review and protected PR/master checks remain required before delivery.
The [P22 DLO fixture](../../experiments/2026-09-07-selected-set-copy.md#maintained-implementation-and-evidence) also records a planned-byte accounting gap for P13: a complete six-name Copy plans twenty-seven bytes from listing sizes but copies and independently verifies thirty-three logical bytes.
Do not create a general workflow engine.
The audit's modelled WAL lost-history case and an actual process kill must remain separately labelled tests.

### Phase D — local filesystem and complete namespace semantics

| ID | Work and dependency | Completion evidence |
| --- | --- | --- |
| P18 | Bind the selected local root's identity and use traversal/creation that cannot escape it (DS-05), after the local policy in P01 | Existing and raced parent symlinks/reparse points produce no outside delta for engine/native downloads and directory markers; final-leaf no-replace protection remains |
| P19 | Apply one materialization identity to complete paths and every ancestor (DS-06 and known Windows reserved-name gaps), after P18 | Case-folded and normalized ancestor conflicts, reserved devices, trailing dots/spaces and forbidden characters reject the whole plan before writes on real target filesystems |
| P20 | Honour cancellation at local placement/native-write boundaries and record partial results (DS-07), after P13/P18 | Cancel before/after staging, between placements and during cleanup yields exactly the allowed filesystem delta and durable partial/cancelled outcome |
| P21 | Share resolved chunk/admission settings and local-upload commit/verification/cleanup gates (DS-10/11, known local DLO completeness gap), after P12/P16 | Above-max explicit chunks refuse before job creation; defaults cap correctly; corrupt/incomplete DLO uploads cannot commit; cleanup failure never loses recovery intent |
| P22 | Correct provider pagination and guard nonprogress (DS-13; W-13), independent of local UI fixes | Short nonempty pages followed by data are complete; repeated/backward markers and malformed/truncated bodies fail visibly; execution and independent verification cover the same full intended namespace |
| P23 | Preserve exact prefix and metadata-input semantics (UI-04/07/14), independent of P18 | Spaces are not trimmed into root scope; literal server prefixes remain literal; inherited property names cannot disappear or corrupt diffs; unsupported Swift keys and raw-header values fail before writes |

The local filesystem oracle must snapshot the selected root and relevant outside sentinels.
P22 is delivered, including the [controlled pinned-rclone short-page omission](../../experiments/2026-09-07-short-page-copy.md) found during native continuation testing.
The native response and continuation fixes landed separately through PR44/45, preserving the mixed-engine partial result and uncapped control until the engine decision received its own design review.
The subsequent [selected-set experiment and maintained evidence](../../experiments/2026-09-07-selected-set-copy.md) establish the [ADR 0022](../../adr/0022-complete-rclone-selection.md) correction under the same provider cap across remote/local callers.
Independent implementation review, all five [PR46 checks](https://github.com/csmart/aeronautes/actions/runs/34086202150), protected merge and all five [resulting master checks](https://github.com/csmart/aeronautes/actions/runs/34086884737) close P22/DS-13 for the defined pagination/completeness contract.
The merged tree matches the reviewed source exactly; wider resource, accounting, platform and valuable-data release gates remain open.
P23 now has a [current-master reproduction record](../../experiments/2026-09-07-literal-inputs.md) and a four-slice implementation sequence in DESIGN §65.11.
Preserve transfer input, then literal browser filtering, then metadata dictionaries and Go wire-identity validation, with independent review between logical commits.
Deliver the two metadata commits together so the Go admission guard accompanies the frontend's literal working copy.
The [raw metadata-value observations](../../experiments/2026-09-07-literal-inputs.md#raw-metadata-value-observations) also require explicit refusal of boundary ASCII whitespace and empty container values while retaining supported empty object values; no implicit value codec is planned.
The fixture prerequisite is delivered through [PR47](https://github.com/csmart/aeronautes/pull/47), with independent review and all five PR/master checks passing; its merged tree matches the reviewed source exactly.
The [transfer implementation](../../experiments/2026-09-07-literal-inputs.md#literal-transfer-requests-and-refusal) preserves literal request strings and quoted scope, rejects slash-bearing containers before job admission and retains the existing prefix and Mirror restrictions.
Its maintained parent regressions, broken-behaviour controls, full frontend suite and full mock/SAIO Go race suite pass their intended checks.
Independent local review and all five PR/master checks passed for [PR48](https://github.com/csmart/aeronautes/pull/48), closing the defined UI-04 transfer-input scope.
The delimiter-marker fixture prerequisite is also delivered through [PR49](https://github.com/csmart/aeronautes/pull/49), with independent review, all five PR/master checks and an exact merged-tree comparison.
The [browser implementation](../../experiments/2026-09-07-literal-inputs.md#literal-browser-filtering-and-navigation) now preserves literal filters, complete leaf labels, folder navigation and exact matching objects.
Its parent regressions and 20 broken-behaviour controls catch their intended faults; all 788 frontend tests and the full 585-top-level mock/SAIO Go race suite pass without skips.
At browser delivery, the original transfer and browser probes passed while the two metadata probes still failed; eight other archived cases remained unselected.
Raw SAIO observations separately confirm that a successful metadata POST does not establish literal underscore-key preservation.
Independent local review and all five PR/master checks passed for [PR50](https://github.com/csmart/aeronautes/pull/50), closing the defined UI-07 browser scope; the merged tree matches the reviewed source exactly.
The subsequent metadata slices complete the remaining P23/UI-14 dictionaries and wire-identity validation.
The [metadata editor implementation](../../experiments/2026-09-07-literal-inputs.md#metadata-working-copy-regressions) now passes 65 focused cases and the full 849-test frontend suite, with 17 broken variants caught independently.
It preserves complete literal working copies and accurate diffs, and independent local review approved that frontend commit.
The [Go admission implementation](../../experiments/2026-09-07-literal-inputs.md#metadata-admission-and-provider-round-trips) now refuses unsupported metadata before object/container writes and container creation, with 132 passing bound cases on both providers, 23 validator cases and 16 caught broken variants.
The full internal race suite passes 590 top-level tests and 1,401 leaf cases in 20 packages without skips; valid scenario coverage is 32/110 with 38/49 actions still gapped.
Independent local review approved both metadata commits, delivered together through [PR51](https://github.com/csmart/aeronautes/pull/51).
All five [PR checks](https://github.com/csmart/aeronautes/actions/runs/34124455916) and all five [resulting master checks](https://github.com/csmart/aeronautes/actions/runs/34125252087) passed; the merged tree matches the reviewed source exactly, with all four logical commits preserved.
The [delivery record](../../experiments/2026-09-07-literal-inputs.md#metadata-delivery) closes UI-14/P23 for the defined literal-input contract, following the delivered UI-04 transfer and UI-07 browser slices.
The wider authority, durability, outcome, concurrency, local-platform, accessibility and valuable-data release gates remain open.
Linux success is insufficient for Windows/macOS namespace claims.
Download/upload UI should show a bounded selected-name summary, explicit account and exact scope tied to the backend plan.

### Phase E — resource budgets and product completeness

| ID | Work and dependency | Completion evidence |
| --- | --- | --- |
| P24 | Bound namespace inventories, queue length, scans, child batches and aggregate endpoint resources (DS-12; PERF-01), after P13/P19's durable indexes | RSS scales with configured bounds rather than total names; queued/cancelled/abandoned work releases state; million-name and many-job fixtures report measured limits without truncating correctness |
| P25 | Measure representative throughput/fairness, then implement bounded Auto feedback (PERF-02), after P24 | Small/large/mixed real-engine workloads record throughput/p95/retry/RSS; 429/5xx/latency pressure backs off; operator ceilings and control-plane responsiveness are preserved |
| P26 | Reconcile and implement the supported versioning/account-service model (W-14 and feature gaps), after P01/P14/P17 | Legacy archives and modern display-only/refusal paths have real Swift evidence; preliminary read failure refuses mutation; changing versioning invalidates relevant plans; hidden versions cannot be silently lost |
| P27 | Add the exact-object delete journey and remaining accepted v1 scope incrementally (UI-08; product matrix), after P11/P14/P15 | Plain/empty/link/manifest identity transitions and same-prefix siblings are safe; never substitute prefix deletion for exact deletion; directory upload/account metadata/configuration are delivered or explicitly re-scoped |
| P28 | Make jobs/audit/errors actionable and complete the agreed record retention/export/support policy (UI-09/10; SEC-08), after P13 | Live effective settings, operation-specific counts, transaction IDs, account filters and complete failure access; empty versus unavailable is distinct; retention/export and DB/keychain backup/restore are tested |
| P29 | Complete modal/keyboard/label semantics, contrast and adaptable layout (UI-11/12/13), in separate logical accessibility/layout commits | Keyboard/select/modal regressions pass; normal text reaches the chosen 4.5:1 AA target; both themes, zoom, 960×600, long names/errors and native screen readers are usable |

Versioning/account management, exact-object deletion, directory upload and settings are separate features within P26/P27 and must have individual contracts and commits.
The plan does not expand to the explicit v1 non-goals.
For accessibility, use [WCAG 2.2 contrast guidance](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html) as the measured target and add native assistive-technology evidence.
A token contrast calculation or jsdom test is not a whole-app conformance claim.

### Phase F — supported platforms and release evidence

| ID | Work and dependency | Completion evidence |
| --- | --- | --- |
| P30 | Establish child quiescence and shutdown/restart contracts on each platform (SEC-06), using P03/P12 fault boundaries | Parent killed at a child-write barrier leaves no subsequent child writes; Windows job-object/macOS lifetime solution works on native runners; graceful cancellation and hard kill have distinct exact outcomes |
| P31 | Triage all shipped sidecar advisories and update the engine through its compatibility gate (SEC-11), coordinated with P09/P30 | Every retained advisory has an enabled-path disposition; all six binaries scanned; selected pin passes redirects, TLS, names, staging, retries, cancellation and large-object tests; checksums/version refusal remain enforced |
| P32 | Align Linux distribution installation/update policy with the strict engine pin (SEC-12), after P31 | Clean install and ordinary upgrade on each advertised distro provide the accepted engine or clearly refuse unsupported transfer capability; recovery/repair instructions are actionable |
| P33 | Stamp version/source identity consistently through binary and all packages (SEC-10) | Two fixture release versions yield distinct, matching About/native/package metadata, filenames and source revision; checksums/provenance identify the exact artifacts |
| P34 | Join scenario, engine, real-Swift and native-artifact evidence into an exact-commit release gate (TEST-02/03/04; SEC-09), growing critical regression gates earlier as tests land | Missing tools, skipped required lanes, lost cells, failing fixtures or different source/artifact identity block release; matrix observations are compared with explicit expected behaviour instead of accepting its exit code alone; current accepted signing policy remains explicit |
| P35 | Refresh supported/deferred/status docs and release/support ownership (DOC-01 and security support notes), alongside each completed phase | DESIGN/ADRs/README/requirements/action inventory agree; security reporting and update ownership, third-party notices, manual upgrade/rollback and future-schema refusal policy are documented and verified where executable |

P30 does not need to wait for product-polish Phase E.
P31 advisory triage and P33 stamping can also run earlier once their dependencies are met.
Use the current execution order above and each package's prerequisites; phase labels are not a reason to delay independent, bounded safety fixes.
Native implementation changes still require native evidence rather than a cross-compilation-only approval.

## The fault/barrier harness is included

The harness is part of this remediation work because it is needed to reproduce the demonstrated failures and prove the fixes.
It extends the current `internal/swifttest` and `internal/systemtest` seams rather than replacing them.
P03 is deliberately small; later fault types land with the first real caller that needs them.

| Boundary | Deterministic control | Independent assertion |
| --- | --- | --- |
| Final source/destination HEAD | Pause after the selected request occurrence; let a second actor replace/create an identity; resume | Raced content survives or the operation refuses under its accepted contract; unrelated aliases unchanged |
| Remote mutation acceptance/response | Accept effect, then drop/delay/truncate response or inject a documented HTTP error | Actual remote delta and durable known/unknown outcome agree; retry does not repeat unreviewed destruction |
| Journal write/sync | Fail before persistence, after accepted persistence, or at a modelled lost-history boundary | No unsafe effect without durable intent; missing history causes retention; audit/job state is explicit |
| Stage/commit/cleanup | Pause around captured/committing/committed facts, live PUT, staging deletion and recovery clear | Full fixture namespace, references, digests and recovery records remain correct at every restart point |
| Native local placement | Barrier before parent traversal, creation and each final placement | Picked-root and outside-sentinel snapshots prove containment; cancelled work has exact partial results |
| Native child lifetime | IPC handshake/barrier, kill parent/child, start a fresh recovery process | No orphan continues writes while recovery proceeds; owned resources and result state are accounted for |
| Keychain/SQLite lifecycle | Inject independent failure before/after each store effect | No deleted usable authority, silent credential retarget, orphaned untracked secret or inconsistent bookmark generation |
| Frontend completion | Deferred API promises for preview/save/list/scan, selection changes and dialog unmount | Late results cannot alter current intent, re-arm Start, change selection identity or leak a running scan |

A barrier needs a typed event, operation/job identity, occurrence selector, bounded arrival wait, explicit release and cancellation/cleanup behaviour.
Faults should be scoped to the intended request/job; unrelated traffic must remain observable.
No arbitrary production scripting hook or always-on runtime fault API is required.
Tests must fail clearly if a boundary is never reached, and blocked children/tasks must be released or terminated on test failure.
Use IPC or explicit signals to select the interleaving; sleeps may bound polling but cannot select the race.

The first permanent regressions should be the archived DS-01–04, SEC-01/02/03 and UI-01 probes, plus local-root escape/cancellation.
Strengthen them through the public service boundary and independent account/filesystem/durable-state oracle as that layer lands.
A modelled failure, an actual process kill, a real rclone run and native/real-cluster evidence must be separate report dimensions.
Leave a broader general fault-testing platform separate unless an acceptance test requires it.

## Finding-to-plan coverage

**Blocking** means an unresolved obligation for valuable-data operation or its required release evidence.
**Contained** means the affected authority is withheld under an accepted contract; it is not a repaired implementation or permission to relax the refusal tests.
**Delivered scope** identifies the original finding's bounded fix and leaves sibling guarantees explicit.
**Later capability** changes implementation order only; it does not remove accepted v1 scope or make an untested operation safe.
The original finding IDs are all retained here, including completed fixes whose regressions must continue to run.

| Findings | Current disposition | Remaining work and closing evidence |
| --- | --- | --- |
| DS-01 / SEC-03 | Contained; wider recovery blocking | P01/P04 retain records and refuse automatic reclamation; WAL/FULL is enforced. P12 still needs required durable facts, rejected write/sync and lost-history cases, real crash/restart and safe recovery. Missing history must preserve live aliases; P10/P15/P30 authority and writer quiescence are prerequisites to reclamation. |
| DS-02 / DS-03 | Contained; re-enablement gated | P05 refuses Mirror starts and existing-destination overwrites under ADR 0020. Keep zero-effect refusal and conditional-new-name race tests. P14 may restore only operations with a reviewed enforceable protocol after P11/P12; this does not certify enabled manual deletion or metadata changes. |
| DS-04 | Blocking | P16 must reject same-size corruption, reordered/missing segments, stale fingerprints and changed source/destination identities. Independent ordered content evidence must establish the promised verification strength; weak or unknown equality cannot authorise destructive follow-up. |
| DS-05 | Partly delivered; blocking | [PR77](../../experiments/2026-09-09-download-admission.md#protected-delivery) refuses evidenced existing destination hazards before Start and executor effects, including complete-selection, queued-job and direct-caller checks. P18 still needs stable selected-root authority and protection against concurrent parent replacement through placement and cleanup, with native target-filesystem and complete outside-sentinel evidence. |
| DS-06 | Partly delivered; blocking | [PR78](../../experiments/2026-09-09-download-path-case.md#protected-delivery) applies the case-fold identity to planned files and every required ancestor, refusing the whole selection at preflight and at executor item admission. [PR81](../../experiments/2026-09-10-portable-download-names.md#protected-delivery) adds one lexical portable rule at the same boundaries for platform-reserved and console bases, the restricted characters and trailing spaces or dots. P19 still must apply collision identity to full paths under Unicode normalization and equivalence, leading spaces, length limits and encoding collisions. Real target filesystems must reject the complete unsafe plan before any placement. |
| DS-07 / SEC-06 | Blocking | P20/P30 must establish cancellation at native writes/placements, exact partial outcomes and child quiescence before recovery. Native parent-kill tests on all six targets must prove no later child writes; an orderly database reopen or cross-build is insufficient. |
| DS-08 / SEC-08 | Blocking | P12/P13/P28 must persist one outcome per identity and durable intent/outcome audit facts, retain every failure beyond 200 examples, distinguish known/unknown effects and handle job/audit/spill/DB failures visibly. Validate exact counts and planned logical bytes, malformed/unordered bulk replies, restart and retention/export/backup/restore. |
| DS-09 | Blocking | P17 after P16 must distinguish content equality from replication equality and preserve declared metadata, content headers, expiry and symlink target/account semantics, including dangling links. Changed existing destinations remain refused until P14 permits them. |
| DS-10 | Partly delivered; blocking | P04 retains recovery facts and PR52 delivers post-commit reporting and cleanup refusal after rejected committed records. P12/P13/P21 still need crash boundaries, exact durable identities, terminal-write failure handling and shared Copy/upload cleanup policy; P15/P16 still own cleanup authority and stronger verification. |
| DS-11 | Partly delivered; blocking | [PR73](https://github.com/csmart/aeronautes/pull/73) shares destination chunk admission/default capping between local upload and Copy and records the resolved setting used by the engine. Explicit above-maximum and below-minimum settings refuse before job creation. P21 still must reject incomplete or corrupt DLO content before commit, with P15/P16 verification and cleanup prerequisites. |
| DS-12 / PERF-01 | Partly delivered; blocking | P08 bounds Find workers and retained scans. P24 must also bound full namespace inventories, browser retention, queues, child batches and aggregate endpoint resources without truncating correctness. Measure RSS as names/jobs grow and prove cancellation/expiry releases resources. |
| DS-13 | Delivered scope | P22/ADR 0022 delivers native pagination and complete rclone selection through PR44–46. Preserve short-page, nonprogress, actual-engine and full intended-set regressions. Planned-byte accounting and wider resource bounds remain P13/P24 above. |
| UI-01 / UI-02 / UI-05 | Delivered scope | P06/P07 bind frontend requests, completions and connection state to current identity/lifetime. Preserve deferred-response and duplicate-submit controls; P10/P11 backend authority and P14 external writers remain separate blocking work. |
| UI-03 / UI-06 | Delivered scope | P08 delivers page ownership/deduplication and Find start/stop/expiry/admission. Preserve those regressions; total browser/queue retention remains P24. |
| UI-04 / UI-07 / UI-14 | Delivered scope | P23 delivers literal transfer/browser inputs and metadata working-copy/wire-identity admission through PR48/50/51. Preserve all original probes and refusal controls; this does not prove replication fidelity or backend plan authority. |
| UI-08 | Later capability; v1 gate retained | P27 adds exact-object deletion after P11/P14/P15. Its absence does not grant a prefix-delete substitute: retain explicit unavailability until exact plain/empty/link/manifest identities, final-hop races and same-prefix siblings are tested. Deliver or explicitly re-scope the promised capability before claiming v1 complete. |
| UI-09 | Partly delivered; blocking | PR52 exposes the bounded upload issue summary. P13/P28 must provide complete failure access, operation-specific counters, live effective settings, transaction IDs and account-bound audit views. Test identical names in two accounts and earlier failures followed by a later terminal reason; display samples cannot replace the durable ledger. |
| UI-10 | Partly delivered; blocking | Paging, Find and retained-record status have explicit failure states. P28 must complete cancellation, settings, bookmark and audit error handling; failed/unavailable/stale reads must remain distinct from an empty or successful result, including retry controls. |
| UI-11 | Partly delivered; blocking | [PR72](../../experiments/2026-09-09-keyboard-scope.md#protected-delivery) repairs bounded global-key/modal interference with component and native smoke evidence; the [Preview follow-up](../../experiments/2026-09-09-preview-focus.md) preserves request-specific focus intent. The [sensitive-label correction](../../experiments/2026-09-09-sensitive-labels.md#protected-delivery) connects Find, ACL, audit and typed-confirmation controls while preserving exact guards and per-instance ownership. P29 still needs tab/shortcut patterns, native select and assistive-technology journeys; the accepted keyboard contract remains open. |
| UI-12 | Partly delivered; blocking | [PR79](../../experiments/2026-09-09-text-contrast.md#protected-delivery) corrects the demonstrated colour tokens and the placeholder rule, with maintained pair checks and native observations of both themes across normal, hover, focus, error and disabled states. P29 must still make scope, counts, warnings and action labels legible under forced colours, scaling and assistive technology, and on the other native platforms. Palette preference alone cannot defer this evidence. |
| UI-13 | Blocking safety portion; broader layout later | P24/P29 must keep full names, confirmations, cancellation and errors accessible at supported small windows/zoom and bound large-namespace retention. Extra pane customisation follows those checks because it adds layout flexibility; the accepted responsive-layout gate remains open. |
| SEC-01 / SEC-04 | Partly delivered; blocking | P10 delivers binding/publication, generation admission, request pairing and verified keychain writes. Verified original native-backend selection, bounded prompts, historical recovery authority and physical cleanup remain open; test SQL/keychain faults and native lifetime on each supported platform without deleting live authority or treating a changed backend as erasure proof. |
| SEC-02 / SEC-05 | Delivered scope | P09/ADR 0014 delivers endpoint and child redirect policy. Retain native/real-engine redirect, TLS and retry regressions; P30 child lifetime and P31 shipped-sidecar advisory dispositions remain blocking. |
| SEC-07 | Blocking | P11 after P10 must bind immutable backend plans and narrow one-shot capabilities to operation, account, object set, local resource and current generation. Direct bridge calls, expiry, replay, queued starts and forged force/public-ACL approvals must refuse widened authority. |
| SEC-09 / TEST-02 | Partly delivered; blocking | P34 must make required per-action scenarios, engine observations and exact-source artifact evidence release gates. Existing five-job CI is necessary but insufficient; missing tools, skipped lanes, dropped critical regressions, failed producers or mismatched artifacts must block release. |
| SEC-10 | Partly delivered; blocking release evidence | [PR71](../../experiments/2026-09-09-release-identity.md#protected-delivery-and-hosted-artifacts) delivers shared source/version stamping, two fixture-release checks, checksums/provenance and six hosted development artifacts. Native About, installed-package/upgrade and remaining native-platform observations still block full P33 acceptance. |
| SEC-11 | Blocking release evidence | The [six-sidecar assessment](sidecar-assessment.md) records the accepted pin and candidate comparison at module-version precision, with OpenPGP reachability and all enabled-path dispositions still unaccepted. P31 must complete the application/native-library/npm scopes. Engine updates need the full name/TLS/redirect/staging/retry/cancellation/large-object compatibility gate on all shipped targets; an advisory count or version bump alone is insufficient. |
| SEC-12 | Blocking release evidence | P32 must prove that clean installation and ordinary upgrade supply the exact accepted engine on advertised Linux distributions, or refuse unsupported transfers visibly with an actionable repair path. Keep checksum/version refusal. |
| TEST-01 | Delivered scope | P02/ADR 0018 preserves encoded bytes and headers and refuses incomplete snapshots. Keep representation and partial-response negative controls before relying on the oracle for new safety claims. |
| TEST-03 | Partly delivered; blocking | P03 supplies the minimum HTTP barrier/response-loss seam and later fixtures add bounded faults. P12/P18/P20/P30/P34 still need the required commit, persistence, placement and child-lifetime boundaries, full collateral/durable-state comparisons and deliberately broken controls. Add each seam with its first real caller; no universal fault framework is a prerequisite. |
| TEST-04 | Blocking release evidence | P29/P30/P34 must run the required production-bundle UI, native keychain/picker/filesystem/process and accessibility journeys on all six OS/architecture targets, with representative deployed-Swift evidence. A mock, jsdom result or successful cross-build cannot replace them. |
| PERF-02 | Blocking safety measurements; optimisation later | P24/P25 must establish operator ceilings, endpoint/control-plane responsiveness and safe benchmark cleanup under representative sustained load before valuable-data use. Throughput tuning and adaptive Auto follow those bounded measurements because they optimise the established safe envelope; K-PERF-003 and the accepted performance contract remain open. |
| DOC-01 | Partly delivered; blocking release evidence | P35 must keep supported/refused/deferred operations, concurrency/durability limits and source-specific proof consistent across DESIGN/ADRs/README/requirements/UI. Security/update ownership, third-party notices, support diagnostics and tested upgrade/rollback/backup/restore remain required; historical phase-complete prose cannot grant safety. |

The prior backlog also carries cross-cutting requirements that are not new audit IDs:

| Existing work | Current disposition | Remaining work and closing evidence |
| --- | --- | --- |
| P15 segment/destructive identity and job scope | Blocking | After P11/P13, exact force prefixes must not widen to siblings; equal-ETag empty-DLO manifest swaps and another job's staging names must be protected. Normal transfer cleanup and explicit destructive paths need exact allowed-effect and unchanged-collateral comparisons for nested/shared DLO/SLO backing bytes under the accepted authority contract; creation provenance never proves exclusivity. |
| P26 supported versioning | Blocking for enabled operations | After P14/P17, prove the declared legacy archive and modern display-only/refusal matrix, refused mutations after preliminary read failure, plan invalidation and unchanged hidden versions. A versioning feature label cannot excuse damage from an enabled transfer or metadata action. |
| P27 remaining directory upload/account capabilities | Later capability; v1 gate retained | Absent directory upload or account-management capabilities may follow core safety because they supply no current mutation path. Audit each actually enabled account metadata/configuration path under the same safety gate; deliver or explicitly re-scope the remaining promised features, without changing current non-goals. |

The prior W-01–17 remain covered: plan authority (P06/P11), Mirror/overwrite/exclusivity (P01/P04/P05/P14/P15), credential tuple/returned endpoints (P09/P10), verification (P16), exact outcomes/outbox (P12/P13), force/native capabilities (P11), local transfers (P18–21/P30), fidelity (P17), paging (P22), versioning (P26), session generation (P07/P10), and release evidence (P31–34).
Keep the earlier R-* provenance in DESIGN; this audit does not rename those into newly discovered defects.
The recorded Grok sibling-path cases—local upload cleanup/completeness, unslashed force scope, empty-DLO identity, active-job staging deletion, child redirects and Windows reserved names—map to P15/P19/P21/P09 and need permanent catching tests.
Not all of those prior cases were independently re-executed in this audit.

## Acceptance gates and completion

For each logical fix, require a failing-before/passing-after behaviour test, relevant existing tests, accurate contract/docs, and independent review of the actual resulting commit.
Include the fault event and independent before/after state in the review evidence for irreversible paths.
Do not record an unexecuted scenario, a setup failure, a skipped platform or a weak size-only comparison as a pass.
Require all five actual PR jobs to succeed on the latest reviewed head with current master incorporated, then merge only that head through protection and check all five resulting master jobs.
A protected merge or aggregate label alone is not evidence that those individual jobs passed; inspect their actual conclusions.
Stop further feature delivery if master fails.

Close a safety blocker only with a linked reviewed commit, its catching regression and required native/provider/artifact evidence, or a reviewed accepted refusal of the affected capability.
Record which action, engine, object class, platform and failure boundary the evidence covers and what remains unproved.
Missing native runners or unresolved product decisions are explicit blockers, never implicit N/A cells or permission to weaken the contract.
The disposition table supports progress tracking; it does not itself lift the valuable-data hold or establish v1 completeness.

Before valuable-data release, all of the following must be true:

- The destructive and credential P1/critical findings are resolved or the affected capability is explicitly unavailable under the accepted product contract.
- Every supported bound action and corresponding UI journey has explicit required/N/A scenarios with rationale, critical regressions cannot disappear, and required lanes cannot silently skip.
- Independent snapshots verify intended content/header changes, unchanged collateral accounts/aliases/versions, selected local-root containment, and exact durable job/recovery/audit outcomes.
- Required two-account Swift, real-engine, parent/child recovery, native-platform, representative deployment and artifact tests pass for the release revision.
- All shipped sidecars and dependency scopes have current advisory evidence/dispositions; the pending npm scan is completed with permission or an appropriate fully local advisory source.
- Version/source identity, installation, upgrade/rollback, supported engine availability, support diagnostics and privacy boundaries match the actual artifacts.
- The accepted deferred/non-goal list and remaining risks are explicit; unresolved design decisions have not been silently converted into unsupported guarantees.

No calendar estimate is justified until the concurrency/durability choices and real-runner constraints are resolved.
Track completion by the remaining blockers and these reviewable evidence gates, following the current execution order above rather than the original phase labels or a nominal coverage percentage.
