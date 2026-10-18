# Finding register reconciled against master — 18 October 2026

## Why this exists

The [finding register](../2026-09-05/remediation-plan.md) was last updated on **22 September 2026**.
[ADR 0080](../../adr/0080-upload-content-binding.md) and
[ADR 0085](../../adr/0085-reobserved-destructive-identity.md) landed on **16 October**, and the
capability they serve was returned to the release the same day. Several rows therefore still say that
work is "still needed" which records since have delivered, and at least two ADR **status lines** are
themselves stale in the same direction.

The consequence is not cosmetic. Asked on 18 October whether a Linux 0.1.0 is data-safe or whether
implementation remains, the honest answer from the register alone was the finish line's own sentence,
*"The 34 open or partial findings remain open"* — which is true as bookkeeping and useless as a
safety picture,
because nobody can tell from it which of those are discharged, which are delivered inside a boundary
the record itself states, which are limits somebody already decided to accept, which belong to
capabilities this build refuses, which are other platforms' work, and which are genuinely owed.

This record reconciles them. It changes no code and runs no new test.

**Coverage: a subset, chosen by family and bounded here.** The register's disposition table has
**thirty-three** rows. This page reconciles the **twenty-three** in the DS, SEC, PERF and TEST
families: nineteen DS and SEC rows clause by clause, and the four remaining PERF and TEST rows with
one line each at the end. **Ten rows are excluded** — the nine **UI** rows and **DOC-01** — as a
choice of scope and **not** because they are irrelevant to safety. Several of them plainly are not:
the register gives **UI-13** a "blocking safety portion" for reachable confirmation, cancellation and
errors, **UI-09** and **UI-10** owe complete failure access and broader mutation-error handling, and
**DOC-01** owes consistent concurrency and durability limits across the documents. **This page gives
those ten rows no disposition at all** — it does not examine them, and nothing here says whether they
are delivered, owed or anything else. The conclusion below is bounded to the twenty-three it did
examine. (The first draft claimed every row, having counted only the families it had chosen to look
at; its second claimed these ten were not safety contracts, which the table itself contradicts.)

## Method, and what it cannot establish

For each row's "still needs / still must" clauses: find the record that claims to deliver the clause,
confirm that record's own status says delivered or implemented, and confirm the contract appears in
the code with cases. Where none of that holds, the clause is marked **not established** rather than
assumed either way.

**Two limits, stated because they bound every line below.** This reconciles *records and code*; it
does not re-run the evidence those records retain, so a clause marked delivered is delivered **on its
record's own claim plus the presence of its contract in the tree**, not on a fresh measurement. And
where a clause's wording does not map onto any record or symbol, "not established" means exactly
that — not "known broken" and not "known fine".

Six verdicts are used. The first review of this page was right that four were too few: collapsing a
bounded delivery, a decided limit and an unknown into one word is the same loss of precision the
register itself suffers from.

| Verdict | Meaning |
| --- | --- |
| **delivered** | a record claims it, its status says so, and the contract is in the tree with cases |
| **delivered within a stated boundary** | the same, *and* the record names what it does not cover; the boundary is quoted here rather than summarised |
| **documented limit** | the gap is stated in a record and disposed of — accepted by the operator, or deferred to a named item. A decision, not an absence |
| **deferred capability** | it belongs to copy, mirror or bulk delete, which a v1 build refuses |
| **platform or acceptance** | not Linux code: another platform, or the operator's own journey |
| **not established** | could not be confirmed from the records or the code, in either direction |

## The rows

### DS-01 / SEC-03 — register: "Contained; wider recovery blocking"

| Clause | Verdict |
| --- | --- |
| required durable facts | **delivered** for the surface v1 enables: [ADR 0058](../../adr/0058-object-metadata-durable-facts.md) object metadata, [ADR 0062](../../adr/0062-container-metadata-durable-facts.md) container metadata, [ADR 0077](../../adr/0077-creation-durable-facts.md) container and marker creation (`internal/app/container_metadata_facts.go`, `internal/app/mutateservice.go`, cases in `internal/systemtest/creation_facts_test.go`), [ADR 0031](../../adr/0031-upload-item-recording.md) upload items, [ADR 0033](../../adr/0033-download-item-recording.md) download items |
| rejected write/sync and lost-history cases | **delivered within a stated boundary.** The [storage foundation record](../../experiments/2026-09-08-transfer-item-storage.md) claims SQL-refusal and orderly child-reopen evidence, against which its review detected sixteen meaningful broken variants, and states its own boundary in its Limits section: "The child test is an orderly database reopen, not a full application restart, process kill, orphan-child, fsync or power-loss test." The **rejected-write** half is therefore delivered; **sync and lost-history under a crash are not measured by it** |
| real crash/restart and safe recovery | **partly delivered, and by two records other than the recovery ones.** A process **kill** with recovery after it *is* measured, on Linux, for the direct child: `internal/systemtest/child_lifetime_linux_test.go` kills the service parent (`:568`–`576`), requires the child to die of that parent's death by `SIGKILL` (`:650`–`656`), checks the retained partial against the object's own leading bytes, and then starts **another process** to read the retained and interrupted state back (`:689` onward). What the **recovery** records do not cover, in their own words: [recovery retention](../../experiments/2026-09-06-recovery-retention.md) — "The child-process check opens the database while the parent fixture still owns a connection", and of what that proves, "it is not a full desktop restart, parent kill, child-quiescence or physical power-loss test"; [upload recovery](../../experiments/2026-09-08-upload-recovery.md) — "This is a process reopen after orderly close, not a process kill, full application startup, power-loss or native desktop recovery test." Their tests match those words: `TestRecoveryRecordsAfterProcessReopen` (`internal/app/recovery_retention_test.go:112`) starts a reader child **without killing its parent and without closing the parent's connection**, while `TestTransferItemStorageReopen` (`internal/systemtest/item_storage_test.go:88`) and `uploadRecoveryReopen` (`internal/systemtest/upload_recovery_test.go:143`) close the database normally first. A **desktop launch and restart** is recorded separately in the [release evidence journeys](../../experiments/2026-09-12-release-evidence.md) — the production Linux binary relaunched on the same state directory and showed the retained job — though for a historical build, against the mock, headless, and stopped **after** its transfer finished. So bounded kill-and-recover and bounded launch-and-restart cases both exist; what remains unmeasured is **the combination**: a desktop startup recovering from an abrupt in-flight crash, plus fsync and power-loss durability, plus the non-Linux targets |
| reclamation prerequisites (P10/P15/P30 authority, writer quiescence) | **deferred capability** — reclamation stays refused under ADR 0020 apart from ADR 0081 decision 5's one named upload exception, which stays narrow |

### DS-02 / DS-03 — register: "Contained; re-enablement gated"

No "still needs" clause. The row's own condition — P14 may restore only operations with a reviewed
enforceable protocol — now has a candidate protocol for one of them:
[ADR 0087](../../adr/0087-copy-overwrite-on-upload-terms.md), **proposed**, which also names the
prerequisites it does not discharge. Mirror's equivalent is
[ADR 0086](../../adr/0086-mirror-deletes-what-the-source-does-not-have.md), also proposed. Both
capabilities remain refused.

### DS-04 — register: "Partly delivered; blocking"

This row needed splitting by **route** and by **observation window**; the first draft of this page
had one verdict where there are three different dispositions.

| Clause | Verdict |
| --- | --- |
| local-transfer verification | **delivered** for the download routes: [ADR 0060](../../adr/0060-native-download-content-binding.md) native, [ADR 0070](../../adr/0070-engine-download-content-binding.md) engine (implemented 23 September). **Delivered within a stated boundary** for both upload paths under [ADR 0080](../../adr/0080-upload-content-binding.md). What the tree does today is stronger than that ADR's own prose: `verifyUpload` (`internal/transfer/local/upload.go:217`–`231`) compares the live entity tag against the local file's **MD5**, so plain upload verification is a digest comparison, not the length-only check ADR 0080's line 65 describes |
| final-publication binding | **delivered for the download routes.** [ADR 0061](../../adr/0061-download-publication-facts.md)'s own Scope is the native-download recording correction and says "Engine content binding and L07 cancellation expansion remain outside this decision"; ADR 0070 extends the download final check. **Upload's publication window is a documented limit**, in ADR 0080's own words at line 65: "a source substituted between planning and sending, or a publication substituted after it, is outside what this decision binds." That is the accepted L05 exposure, not a new finding |
| source-version binding | **delivered for download within a recorded exception, documented limit for upload.** Download refuses an item whose plan captured no source identity (`engineExpectation`, `internal/transfer/local/download_execute.go:576`–`580`) — but *captured* is not the same as *pinned*, and ADR 0060 says so itself at lines 83–85: "an SLO listed with bytes and no `SLOETag` has only size as captured identity. A same-size substitution of that object is not claimed from the listing." The code matches that exception rather than exceeding it: `listingSourceIdentity` (`internal/transfer/local/download_source.go:24`) marks such an item captured with a length and an empty `sloETag`, and the SLO comparison refuses a changed tag only when one was captured (`internal/transfer/local/download_source.go:63`), which `TestDownloadSourceIdentityMatch` (`internal/transfer/local/download_source_test.go:32`) requires to pass for the size-only case. Upload's preflight "captures a path, a size and a route rather than a source identity" (ADR 0080, line 65). Whether the clause's original wording reached wider than those routes is **not established** |
| segmented-source verification | **delivered for the enabled download routes — this is not copy-only**, which the first draft got wrong. Both `engineExpectation` and `nativeDownload` build a `transfercopy.Ladder` from the source (`internal/transfer/local/download_execute.go:576`–`606` and `internal/transfer/local/download_execute.go:636`–`655`), with cases in `internal/systemtest/download_content_binding_test.go` and `internal/systemtest/engine_binding_linux_test.go`. Copy's own segmented sources are bound by [ADR 0042](../../adr/0042-source-content-ladder.md) and, for the native route, [ADR 0083](../../adr/0083-native-copy-content-binding.md) as amended by [ADR 0084](../../adr/0084-held-manifest-copy.md), ahead of copy being enabled |
| truthful strength labels | **delivered in substance**: `CodeUnverified` and the `Unverified` total in `internal/jobs/item_types.go`, carried through `internal/jobs/completion.go`, and surfaced per item in `frontend/src/features/jobs/JobItems.svelte` as "Unverified copy." keyed on both the verification and the publication result. There is **no finer strength label** than verified/unverified; whether the clause wanted one is a judgement this record does not make |
| stale later listings | **documented limit.** ADR 0042 lines 91–92 keep it as an explicit Copy limitation: "the stale later listing limitation recorded in `segment_change_detection_test.go` is unchanged", and `TestStaleAggregateObservationStillSkipsGap` (`internal/transfer/copy/segment_change_detection_test.go:435`) changes the actual bytes while pinning the old aggregate and asserts the remaining skip. A recorded deferral on a capability v1 refuses — **the first draft called this not established, which was wrong** |
| large-source/plain-destination cases | **delivered.** ADR 0042 line 43: "The check applies to a plain staged object as well as a segmented one, which closes the large-source to plain-destination case"; the [data-safety record](../2026-09-05/data-safety.md) repeats it at line 143; `internal/transfer/copy/executor.go:894`–`901` runs the ladder for every pair except plain/plain; `TestSegmentedSourcePlainDestinationSubstitutionRefused` (`internal/transfer/copy/source_ladder_test.go:148`) catches that exact case. **The first draft called this not established too. It is closed and recorded**, and an under-claim like that sends the operator to implement what already exists |

### DS-05 — register: "Partly delivered; blocking"

| Clause | Verdict |
| --- | --- |
| native Windows and macOS acceptance, reparse-point evidence, the engine route on those platforms | **platform or acceptance** — supported v1 is Linux only; the Linux half is [ADR 0035](../../adr/0035-download-root-authority.md)'s held handle, delivered |

### DS-06 — register: "Partly delivered; blocking"

| Clause | Verdict |
| --- | --- |
| real target filesystems rejecting the complete unsafe plan before any placement | **platform or acceptance** — the lexical and identity rules are delivered at both boundaries per the row's own citations; running them against real filesystems is the operator's journey |

### DS-07 / SEC-06 — register: "Blocking"

| Clause | Verdict |
| --- | --- |
| cancellation ancestor reporting, bounded cleanup of a gracefully ended download's stage | **delivered** — [ADR 0043](../../adr/0043-cancellation-delta-authority.md) and [ADR 0048](../../adr/0048-cancelled-stage-removal.md), both scoped to the cancelled download's own stage |
| shutdown ownership of the synchronous writes v1 keeps | **delivered within a stated boundary, and the boundary is operator-accepted.** [ADR 0053](../../adr/0053-synchronous-writer-shutdown-ownership.md)'s status says "Accepted for the metadata writes v1 keeps" and, in the same breath, that "It does not address crash or kill, does not establish whether closing the webview window cancels an in-flight call's context, and does not close L08 or the valuable-data hold". That last clause is **historical**: the finish line's **decision 20** then accepted it — "The L08 shutdown ownership is accepted for v1 on its mock-transport evidence" — and assigned the live window-close check to the operator's test plan "rather than work owed by the author". It is **step 3** of the [0.1.0 test plan](../2026-09-24/0.1.0-test-plan.md) |
| credential-store ownership — **two contracts, not one**, and on an ENABLED path either way (the first draft filed the whole clause as unestablished and outside v1, which was wrong) | **(a) the caller's wait bound: delivered.** [ADR 0055](../../adr/0055-bounded-credential-operations.md) bounds the wait at the adapter and says plainly that bounding a wait is not cancelling the operation: "The native call is not cancelled, because the pinned client offers no way to cancel it. The abandoned call continues until the Secret Service answers or the process exits", and an abandoned write or delete "returns an error that says the effect is unresolved". That is an accepted exposure **for a cancelled caller**. **(b) shutdown ownership of credential publication and cleanup:** **not established**. `BookmarkService` is bound in the shell (`main.go:122`) and its `Save`/`Delete` publish and physically remove credentials, but `ShutdownWork(ctx, runner, find, mutate)` (`internal/app/shutdown.go:11`) joins the job runner, Find and MutateService — **not** `BookmarkService` — and no record decides whether it should. ADR 0055 accepts a cancelled caller; it does not dispose of shutdown's reach. **Nothing here reports a new defect or asks for a new gate** |
| descendant containment, protected delivery of the drain | **platform or acceptance, and v1 keeps it** — the finish line: "Containment is unchanged: placement inside the selected root, namespace refusals and writer lifetime all serve download and shutdown, which v1 keeps" ([linux-finish-line.md:128](../2026-09-05/linux-finish-line.md)). Not discardable with copy |
| the synchronous bridge and native-request half | **not established** — I could not map this clause's wording onto a record or a symbol in either direction |
| native acceptance on every supported target | **platform or acceptance** |

### DS-08 / SEC-08 — register: "Partly delivered; blocking"

| Clause | Verdict |
| --- | --- |
| bulk aggregates, one destructive request per name, the sweep that stops | **delivered** per the row's own citations ([ADR 0039](../../adr/0039-delete-request-authority.md)) |
| the listing-to-request race and names first listed in a later pass | **deferred capability** — bulk delete is refused; [ADR 0085](../../adr/0085-reobserved-destructive-identity.md) decides prefix deletion's protocol and records it as owed |
| complete per-effect/durable outcome contract (P12/P13) | **delivered** for the enabled surface by the records in DS-01 above, within the same crash boundary |
| ordinary segment-failure retention, the parallel unresolved stream | **deferred capability** |
| record retention policy (P28) | **delivered** — the §51 prune in `internal/app/settingsservice.go`, wired at startup in `main.go` |
| export/support policy (P28) | **open P28 work, and placed** — the finish line keeps "P20 and P28 whole" (linux-finish-line.md:127) and lists backup/restore and support dispositions among the separate acceptance journeys at line 178. Owed and located, **not** unestablished as the first draft had it |

### DS-09 — register: "Blocking"

| Clause | Verdict |
| --- | --- |
| P17's content-versus-replication equality, metadata-only and symlink fidelity | **deferred capability** — P17 is deferred with copy by the 14 September re-baseline, and changed existing destinations stay refused until P14 permits them |

### DS-10 — register: "Partly delivered; blocking"

| Clause | Verdict |
| --- | --- |
| crash boundaries, exact durable identities, terminal-write failure handling | **delivered for the orderly boundaries**: [ADR 0023](../../adr/0023-upload-post-commit-errors.md) errors after commit, [ADR 0024](../../adr/0024-local-job-completion.md) and [ADR 0025](../../adr/0025-local-interruption-outcomes.md) atomic completion and interruption, [ADR 0033](../../adr/0033-download-item-recording.md), plus the durable-fact records above. The word "crash" in this clause reaches past them: see DS-01 for what the kill case does measure and what remains unmeasured |
| **shared Copy/upload cleanup policy** | **delivered in its result-and-retention half; its crash case and its general cleanup authority are explicitly deferred.** The original remedy ([data-safety.md:305](../2026-09-05/data-safety.md)–306) asks for one cleanup result and durable-state policy across local upload and Copy, clearing intent "only after each created identity is accounted for or retained with a durable reason". That policy **is** stated: [ADR 0020](../../adr/0020-recovery-containment.md) lines 55–81 apply recovery retention to "Copy, Mirror or local upload", refuse automatic reclamation, and name the executor's `cleanStaged`, the per-object deletion in `local.commitStagedUpload` and the child's cancellation cleanup as "the exact transfer-time boundary for separate P12/P15/P21 work"; [ADR 0030](../../adr/0030-copy-item-recording.md) lines 65–85 record the observation only after required post-copy actions complete or are refused, retain `copied` and copied bytes with a typed problem, and say of committing intent and captured prefixes that "neither is cleanup authority"; [ADR 0031](../../adr/0031-upload-item-recording.md) lines 73–117 stop subsequent work under a failed acknowledgement, retain the affected stage on a committed-record refusal, and forbid retrying an uncertain failed deletion. Cases: `TestCopyRecordsAfterPostCopyActions` (`internal/transfer/copy/post_copy_recording_test.go:30`) and `internal/systemtest/upload_recovery_test.go`. What is **not** delivered is the remedy's acceptance case that crashes between commit and cleanup — the direct-child kill case under DS-01 does not place its kill at that point — and general transfer-time cleanup authority, deferred by ADR 0020 itself to P12/P15/P21. This is not a claim of one unified implementation. **The first draft said "no record states the policy". That was wrong, and it was wrong because I searched for the `StageCleanup` symbol instead of reading the records that state the policy in terms of retained identities and truthful results.** ADR 0081's predecessor-fragment exception stays narrow |
| P15/P16 cleanup authority and stronger verification | P15 shrank to local-job scope coordination under L03; P16's verification is DS-04 above |

### DS-11 — register: "Partly delivered; blocking"

| Clause | Verdict |
| --- | --- |
| reject incomplete or corrupt DLO content before commit | **delivered on both routes**: ADR 0080 decision 3 reads the staged object's assembled bytes and folds them through the source's own one-run expectation before the manifest PUT, and ADR 0042 bound the staged copy route; [ADR 0037](../../adr/0037-staged-content-read.md) owns the staged reads both rely on |
| P15/P16 verification and cleanup prerequisites | as DS-10 and DS-04 above |

### DS-12 / PERF-01 — register: "Partly delivered; blocking"

Absent from the first draft of this page.

| Clause | Verdict |
| --- | --- |
| P24's full namespace inventories, browser retention, queues, child batches, aggregate endpoint resources | **delivered for the bounds v1 needs** — [ADR 0072](../../adr/0072-large-namespace-handling.md) and [ADR 0074](../../adr/0074-workload-bounds.md), on which the finish line's **L10 closes**; "Operating limits is unchanged, because its bounds serve browse and download" (linux-finish-line.md:129) |
| measure RSS as names/jobs grow; prove cancellation/expiry releases resources | **platform or acceptance** — measurements on a real deployment, in the finish line's safety-measurement row with PERF-02 |

### DS-13 — register: "Delivered scope"

Planned-byte accounting and wider resource bounds are delivered by ADR 0072 and ADR 0074; the finish
line's **L10 closes** on them.

### SEC-01 / SEC-04 — register: "Partly delivered; blocking"

The credential and error-path row. The first draft folded it into SEC-07 and reconciled only SEC-07's
clauses, which left most of this row unaccounted for.

| Clause | Verdict |
| --- | --- |
| P10's binding/publication, generation admission, request pairing, verified keychain writes | **delivered** per the row's own citation |
| public error text | **delivered** — the locally reviewed [native HTTP message correction](../../experiments/2026-09-12-public-http-errors.md) excludes provider reason text from the public Message while preserving typed fields and causes |
| verified original native-backend selection | **delivered within a stated boundary** — [ADR 0057](../../adr/0057-persistent-backend-selection.md): "Accepted for the Linux Secret Service fixture pin. Native acceptance on the six targets remains separately required. L01 does not close." |
| bounded prompts | **delivered within a stated boundary** — ADR 0055 bounds the wait at the adapter and says plainly that bounding a wait is not cancelling the operation; the abandoned call continues until the Secret Service answers or the process exits |
| physical cleanup | **delivered** — ADR 0076, accepted 24 September and implemented the same day for the pass after a delete or a key change; the credential dialog's own "Remove retired keys" action waits for a bridge-action admission under ADR 0069 |
| historical recovery authority | **deferred capability** — recovery performs no remote listing, HEAD, DELETE or automatic authentication using a historical bookmark record (ADR 0020) |
| broader error paths and raw transaction headers; SQL/keychain faults and native lifetime on each supported platform | **platform or acceptance** for the per-platform half; the broader error paths remain open as the row says, and nothing here infers they are unimplemented |

### SEC-07 — register: "Blocking"

| Clause | Verdict |
| --- | --- |
| immutable backend plans and scoped one-shot capabilities | **delivered** for the halves v1 requires — [ADR 0041](../../adr/0041-scoped-selection-capabilities.md) and [ADR 0073](../../adr/0073-selected-resource-acknowledgement.md) option B since 24 September, which the finish line's **L02** records, validated by **step 2** of the test plan |
| the forged-force half | **deferred capability** |

### SEC-02 / SEC-05 — register: "Delivered scope"

No clause outstanding.

### SEC-09 / TEST-02 — register: "Partly delivered; blocking"

Absent from the first draft of this page.

| Clause | Verdict |
| --- | --- |
| P34 must make required per-action scenarios, engine observations and exact-source artifact evidence release gates; missing tools, skipped lanes, dropped regressions, failed producers or mismatched artifacts must block release | **platform or acceptance** — release-gate work on the artifact side, in the finish line's Supported artifacts row (P31–P35; TEST-02–04). It is about what the release process must refuse, not about this build's transfer behaviour |

### SEC-10, SEC-11, SEC-12 — register: "Blocking release evidence"

**Platform or acceptance**, and these are the valuable-data hold's own content: the artifact and
deployment gates P31–P35 name, including representative deployed-Swift evidence, which cannot be
produced on a workstation.

**SEC-11 is narrower than that paragraph implies**, and the finish line's **decision 8** says how:
the thirty re-proposed sidecar rows "stay as evidence without an acceptance step for v1", the pin
decision "is not a v1 gate", and what v1 owes is that "the release notes must say the sidecar's
advisories are recorded and not dispositioned as accepted". Importing every stale P31 requirement
through the blanket release-evidence wording would re-impose a gate the operator removed.

## Rows outside the DS and SEC families

Named so that coverage is complete rather than sampled. None is reconciled clause by clause here.

| Row | Register's disposition | Where it sits |
| --- | --- | --- |
| TEST-01 | "Delivered scope" | P02 / [ADR 0018](../../adr/0018-snapshot-representations.md) preserves encoded bytes and headers and refuses incomplete snapshots |
| TEST-03 | "Partly delivered; blocking" | P03's HTTP barrier and response-loss seam; bounded faults were added later. Fault-framework work, not a transfer contract |
| TEST-04 | "Blocking release evidence" | P29/P30/P34's production-bundle UI and native journeys — the same acceptance work as SEC-10/12 |
| PERF-02 | "Blocking safety measurements; optimisation later" | P24/P25 operator ceilings and endpoint/control-plane measurements, with DS-12/PERF-01 above |

## What this means for a Linux 0.1.0

**Among the twenty-three rows examined here, no outstanding clause names a known way for this build
to lose data in ordinary operation.** That is the answer, within that bound: the ten UI and DOC rows
are outside this page and several of them carry safety or release-bearing portions of their own. The
groups below are how the answer is reached — and one residual cuts across several rows, stated first
because it is the honest qualifier on the whole page.

**No retained evidence is a durability proof, and the kill evidence that exists is narrower than the
clauses that ask for one.** A process kill followed by recovery *is* measured: the Linux
child-lifetime test kills the service parent, requires the child to die with it, keeps the partial it
had written, and starts another process to read the retained and interrupted state. That is a real
case, and it is a **direct-child, single-platform** one. A **desktop launch and restart** is recorded
too, and by a third record again: the [release evidence journeys](../../experiments/2026-09-12-release-evidence.md)
ran the production Linux binary of that branch's head under a virtual display, copied through the
pinned engine, "stopped the process with a termination signal, relaunched it on the same state
directory and read the job history", and found "the same record with the same identifier and outcome"
in the state database afterwards. That is a historical build against the mock, headless, and its stop
came **after** the transfer completed. So the combination **no record claims** is the one the clauses
ask for: **a desktop startup exercising recovery after an abrupt in-flight crash**, plus **fsync and
power-loss durability**, plus **any of it on the other targets** — and the three recovery records say
so of their own cases, each naming the test it is not. The reopen cases they do run are an
orderly-reopen contract whose review detected sixteen meaningful broken variants against it: a real
and useful guarantee about **records**, and not a durability proof. That gap is owned by P28, by the
artifact and deployment gates, and by the operator's journey, not by a missing decision — and it is
one of the standing reasons the valuable-data hold exists.

With that stated, the clauses fall into five groups:

- **Discharged since 22 September** and simply not written down as such — the content bindings of the
  enabled download and upload routes, the durable facts of every enabled mutation, the identity
  comparison before a replacement, the item records, the retention prune, the large-source to
  plain-destination comparison, the cleanup result-and-retention policy across upload and Copy.
- **Delivered inside a boundary the record itself states** — upload's verification and publication
  window, shutdown's metadata-writer scope (accepted for v1 by decision 20), the backend-selection
  pin, the bounded credential wait, download's source identity inside ADR 0060's size-only exception.
  Each of these is a real contract with a named edge; the edge is quoted above rather than summarised.
- **Documented limits somebody already decided to accept** — the stale later listing on Copy, the
  upload source/publication substitution window that is the accepted L05 exposure, the abandoned
  credential call that no pinned client can cancel. These are decisions on the record, not unknowns.
- **Capabilities a v1 build refuses** — copy's replication equality, bulk delete's listing-to-request
  race, general segment reclamation (ADR 0081 decision 5's upload predecessor-fragment exception stays
  enabled, and narrow), mirror, historical recovery authority. Refusal is the safe direction and
  is why they are not v1 work; this is most of why the register reads as alarming.
- **Other platforms, or the operator's own journey** — Windows and macOS acceptance, reparse points,
  real target filesystems, the live window-close check, descendant containment evidence, RSS growth,
  the artifact and deployment gates, the real-cluster session. Several cannot close on a workstation.

**Three clauses this reconciliation could not establish**, in either direction, listed so they are
not lost: **DS-07's synchronous bridge and native-request half**, whose wording I could not map onto
a record or a symbol; **DS-07's shutdown ownership of credential publication and cleanup**, which is
on an enabled path — `ShutdownWork` does not join `BookmarkService`, and no record decides whether it
should, so ADR 0055's accepted *caller* wait bound does not dispose of it; and **DS-04's
"source-version binding" in its exact original scope**, which is delivered for download inside
ADR 0060's recorded size-only exception and a documented limit for upload, but whose original wording
may have meant something wider than either.

The first draft of this page listed four unestablished clauses, and all four were wrong. Three were
not unestablished at all — the large-source/plain-destination case is closed and recorded, the stale
later listing is a documented Copy limitation with a maintained test, and the shared cleanup policy
is stated across ADR 0020, ADR 0030 and ADR 0031 — and the fourth, export/support policy, is placed
P28 work. The three above were found by looking, including one the first draft had filed as outside
v1 when it is on an enabled path. An under-claim sends somebody to build what already exists, so the
corrections matter in both directions.

So: a Linux 0.1.0 can be cut and exercised, with the exposures its records already state accepted —
no conditional write exists on this store, so every replacement narrows a window it cannot close —
and **the valuable-data hold is unchanged by this reconciliation**, because what holds it is the
artifact and deployment evidence and the unmeasured durability combination, not these rows. And it
answers for the twenty-three rows it examined: the register's nine UI rows and DOC-01 are outside
this page, carry safety and release-bearing portions of their own, and are given no disposition here.

## Two stale status lines found while doing this

Neither is corrected here; both are recorded so the next reader is not misled as this one was.

- [**ADR 0045**](../../adr/0045-route-content-binding.md) still says "the native copy route, the
  native upload route and the staged upload commit remain in the sequence". ADR 0080 took both upload
  paths on 16 October and ADR 0083, as amended by ADR 0084, took the native copy route on 6 October.
- [**ADR 0077**](../../adr/0077-creation-durable-facts.md) opens "Proposed", and the same sentence
  goes on to say it was "implemented the same day" — which is true, and which a reader scanning
  status lines will miss. Its contract is in `internal/app/container_metadata_facts.go` and
  `internal/app/mutateservice.go` with cases in `internal/systemtest/creation_facts_test.go`.

ADR 0083's own line said "Proposed" until 17 October, ten days after its implementation landed, and
was read twice as establishing that the native copy route did not exist. That is the failure this
section exists to stop repeating.

## Limits

This record reconciles **records and code**. It re-runs no evidence, measures nothing, and closes no
finding: only the operator can re-dispose a row on a frozen scope list. It does not lift the
valuable-data hold, change any capability, alter the 0.1.0 test plan, or turn any correction above
into a new gate on the author. Where it says delivered, that rests on a record's own claim plus the
contract's presence in the tree, **not on a fresh measurement**.
