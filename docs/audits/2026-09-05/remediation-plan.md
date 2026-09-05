# Remediation plan

This plan addresses the [5 September 2026 audit](README.md) of `cc6465ac69ec00756d9976bbf6cf7531b07da66d` and the still-open DESIGN §65.10 W/R backlog.
It is a proposed execution sequence, not a declaration that the defects have been fixed or that new locked product decisions have been accepted.
Keep the existing valuable-data release hold until the safety and release gates below have evidence.
Implement through the project's commit/review discipline, with a catching regression for every fix and an independent system reproduction once the relevant harness layer exists.

The first practical slice is **the oracle encoding correction plus the minimum deterministic HTTP barrier layer, followed immediately by recovery and destructive-race containment**.
Do not wait for a universal fault framework or the complete scenario matrix before containing demonstrated hazards.
Credential-origin and UI-intent fixes can proceed in separate bounded slices while the deeper mutation contract is settled.

## Decisions to record before dependent code

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
These changes are proposed; the audit itself has not disabled or changed any operation.

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
| P23 | Preserve exact prefix and metadata-input semantics (UI-04/07/14), independent of P18 | Spaces are not trimmed into root scope; literal server prefixes remain literal; inherited property names cannot disappear or corrupt diffs; unsupported Swift keys fail explicitly |

The local filesystem oracle must snapshot the selected root and relevant outside sentinels.
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
| P34 | Join scenario, engine, real-Swift and native-artifact evidence into an exact-commit release gate (TEST-02/03/04; SEC-09), growing critical regression gates earlier as tests land | Missing tools, skipped required lanes, lost cells, failing fixtures or different source/artifact identity block release; current accepted signing policy remains explicit |
| P35 | Refresh supported/deferred/status docs and release/support ownership (DOC-01 and security support notes), alongside each completed phase | DESIGN/ADRs/README/requirements/action inventory agree; security reporting and update ownership, third-party notices, manual upgrade/rollback and future-schema refusal policy are documented and verified where executable |

P30 does not need to wait for product-polish Phase E.
P31 advisory triage and P33 stamping can also run earlier once their dependencies are met.
The phase order is the risk-priority path; it is not a reason to delay independent, bounded fixes.
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

| Findings | Work packages |
| --- | --- |
| DS-01 / DS-02 / DS-03 | P01–05, P11–15 |
| DS-04 / DS-09 | P16–17 |
| DS-05 / DS-06 / DS-07 | P18–20, P30 |
| DS-08 / DS-10 / DS-11 | P12–13, P21, P28 |
| DS-12 / DS-13 | P22, P24 |
| UI-01 / UI-02 / UI-03 / UI-05 / UI-06 | P06–08, P11 |
| UI-04 / UI-07 / UI-14 | P23 |
| UI-08 / UI-09 / UI-10 | P27–28 |
| UI-11 / UI-12 / UI-13 | P24, P29 |
| SEC-01 / SEC-04 / SEC-07 | P10–11 |
| SEC-02 / SEC-05 | P09, P31 |
| SEC-03 / SEC-06 / SEC-08 | P12–13, P28, P30 |
| SEC-09 / SEC-10 / SEC-11 / SEC-12 | P31–34 |
| TEST-01 / TEST-02 / TEST-03 / TEST-04 | P02–03, P12, P29–30, P34 |
| PERF-01 / PERF-02 / DOC-01 | P08, P24–25, P35 |

The prior W-01–17 remain covered: plan authority (P06/P11), Mirror/overwrite/exclusivity (P01/P04/P05/P14/P15), credential tuple/returned endpoints (P09/P10), verification (P16), exact outcomes/outbox (P12/P13), force/native capabilities (P11), local transfers (P18–21/P30), fidelity (P17), paging (P22), versioning (P26), session generation (P07/P10), and release evidence (P31–34).
Keep the earlier R-* provenance in DESIGN; this audit does not rename those into newly discovered defects.
The recorded Grok sibling-path cases—local upload cleanup/completeness, unslashed force scope, empty-DLO identity, active-job staging deletion, child redirects and Windows reserved names—map to P15/P19/P21/P09 and need permanent catching tests.
Not all of those prior cases were independently re-executed in this audit.

## Acceptance gates and completion

For each logical fix, require a failing-before/passing-after behaviour test, relevant existing tests, accurate contract/docs, and independent review of the actual resulting commit.
Include the fault event and independent before/after state in the review evidence for irreversible paths.
Do not record an unexecuted scenario, a setup failure, a skipped platform or a weak size-only comparison as a pass.

Before valuable-data release, all of the following must be true:

- The destructive and credential P1/critical findings are resolved or the affected capability is explicitly unavailable under the accepted product contract.
- Every supported bound action and corresponding UI journey has explicit required/N/A scenarios with rationale, critical regressions cannot disappear, and required lanes cannot silently skip.
- Independent snapshots verify intended content/header changes, unchanged collateral accounts/aliases/versions, selected local-root containment, and exact durable job/recovery/audit outcomes.
- Required two-account Swift, real-engine, parent/child recovery, native-platform, representative deployment and artifact tests pass for the release revision.
- All shipped sidecars and dependency scopes have current advisory evidence/dispositions; the pending npm scan is completed with permission or an appropriate fully local advisory source.
- Version/source identity, installation, upgrade/rollback, supported engine availability, support diagnostics and privacy boundaries match the actual artifacts.
- The accepted deferred/non-goal list and remaining risks are explicit; unresolved design decisions have not been silently converted into unsupported guarantees.

No calendar estimate is justified until the concurrency/durability choices and real-runner constraints are resolved.
Track completion by these reviewable evidence gates, starting with P01–P06 and the independent credential-origin fix, rather than by a nominal coverage percentage or phase label.
