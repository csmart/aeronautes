# ADR 0088 — tell the operator what a restart interrupted, and let them dismiss it

**Status:** Proposed. Written for review before implementation, in the shape [ADR 0087](0087-copy-overwrite-on-upload-terms.md) and [ADR 0086](0086-mirror-deletes-what-the-source-does-not-have.md) took. Nothing here is built. It proposes no new capability, lifts nothing from [ADR 0052](0052-v1-capability-gate.md)'s deferred table, and changes no refusal.
**Scope:** What the application tells the operator at startup about the jobs a previous run did not finish, and what it lets them do about it. It does **not** propose resuming a job, restoring a queue, reclaiming residue, or widening `TransferService.RerunJob`. It does not touch [ADR 0020](0020-recovery-containment.md)'s retention or [ADR 0047](0047-single-active-job.md)'s ceiling.

## The operator's decision

Recorded as [decision 25](../audits/2026-09-05/linux-finish-line.md) of the finish line's operator
trail. On 18 October 2026 the operator asked what happens to a queue of jobs across accounts when the
application crashes, and then decided the shape: *"if they say ignore to them all, or individually,
that's their choice, I like that idea"* — prompt on restart, say what failed, and let the operator
dismiss the whole set or one job at a time rather than have the application restore a queue by
itself.

## What already happens, which is most of the work

The recording is in place and this decision alters none of it. The contracts below are quoted from
the records that own them rather than generalised from one branch of the code, which an earlier draft
of this page did.

1. **Required intent precedes the dependent effect, as a general rule.**
   [ADR 0020](0020-recovery-containment.md) line 87 states it: "Persist required intent before the
   dependent remote effect and refuse that effect when persistence reports failure." The
   `committing`/`committed` pair at `internal/transfer/local/upload.go:589` and
   `internal/transfer/local/upload.go:605` is **one instance** of that rule, inside the staged DLO
   publication branch opened at `internal/transfer/local/upload.go:581` — it is not the whole of it,
   and `committed` and the captured-prefix observations are written *after* their effects.
2. **The database is checked, not trusted** — `internal/db/connection.go:44`–`45` requires
   `journal_mode=wal` and `synchronous=FULL` and verifies the pragmas took effect. ADR 0020 line 86
   requires exactly that, and line 88 states its limit in the same breath: "Successful synchronisation
   depends on the operating system, filesystem and device honouring it; it is not evidence of a tested
   power-loss outcome." Configuration is not ordering, and neither is durability.
3. **A refusal stops later work; it does not stop a running child.** ADR 0031 line 77 is explicit:
   "After a captured-record refusal during an engine call, wait for that synchronous call to return
   and refuse its retry and all later commit/group work", and line 78: "An already started child may
   have written any admitted batch member; do not claim immediate child termination or global writer
   quiescence."
4. **Startup classifies, retains and reclaims nothing.** `main.go:93` calls
   `jobs.Store.FailInterrupted`, which sweeps every job whose state is not terminal — `queued`,
   `planning`, `running`, `verifying` — to `failed` with ADR 0020's reason, "interrupted by shutdown;
   data and recovery records retained, remote residue unverified". If that write cannot be made the
   store becomes unavailable and **blocks new jobs for the session**. ADR 0020 suspended remote
   reclamation and removed the background retry loop, and recovery rows are retained even for jobs
   that completed.

## The gap this decision closes

It is a disclosure gap, and a narrower one than an earlier draft of this page claimed. The
**aggregate** retained-record count is already visible without opening anything: it sits in the jobs
bar's own collapsed button at `frontend/src/features/jobs/JobsDrawer.svelte:272`–`303`, with the
count itself at `frontend/src/features/jobs/JobsDrawer.svelte:299`, which ADR 0020 lines 71 to 74
require. The interrupted jobs are in history carrying their reason.

What is absent is anything **interruption-specific**. `FailInterrupted` returns the count of jobs it
just marked and `main.go:96` writes it to the diagnostic log:

    log.Printf("job crash recovery: %d interrupted job(s) marked failed", interrupted)

A desktop operator never sees that line. Nothing tells them, at the moment that matters, that *this*
restart ended jobs, how many, or which. ADR 0020 required the classification *message* to say records
are retained, which the reason text does; it did not require anybody to be shown it.

## Decision

1. **Record what the restart interrupted as a fact, not as prose — and record the state it found.**
   `FailInterrupted` sets two new nullable columns in the transaction it already runs:
   `jobs.interrupted_at`, and `jobs.interrupted_state` holding **the state the sweep overwrote**.
   Keying on a column rather than on a match against ADR 0020's reason sentence matters because
   ADR 0020 owns that sentence and has already changed it once.

   The second column is not redundant, and an earlier draft of this page was wrong to omit it.
   `Transition` stamps `started_at` only on entering `Running`
   (`internal/jobs/jobs.go:190`–`191`), while an enabled download reaches `Running` through **two
   separate transactions**:
   `internal/app/localtransferservice.go:1182` loops `Planning` then `Running`. A crash between them
   leaves `state='planning'` with `started_at` NULL, and `internal/jobs/interrupted.go:55` then
   overwrites the state with `failed`. Without the prior state retained, that job is
   indistinguishable from one that was still `queued`, and decision 5 could not tell the operator the
   thing it most needs to: whether any request was made at all.

2. **Derive the notice; do not queue it.** The set to disclose is
   `interrupted_at IS NOT NULL AND interrupt_disclosed_at IS NULL`, evaluated on read. A crash
   *during* the disclosure therefore loses nothing. Nothing is written at startup beyond the
   classification that already happens.

   **That guarantee needs one more clause than an earlier draft of this page gave it.**
   `jobs.Store.PruneTerminal` (`internal/jobs/jobs.go:452`) deletes terminal jobs past the retention
   cutoff, excluding only those holding `job_recovery` or `job_items` rows, and
   `app.RunStartupRetention` runs it at `main.go:112`. A **queued download** holds neither — a queued
   upload already holds its staging recovery row, written before `owner.launch` at
   `internal/app/localtransferservice.go:894` — so a queued download would be deleted after
   `JobRetentionDays` with its disclosure still pending, and its whole message is that nothing was
   attempted. **Pruning must therefore exempt a job whose interruption has not been disclosed**, and
   the notice's promise to return until answered rests on that exemption rather than on the two
   timestamps. Dismissal returns the job to ordinary retention.

   **That backlog is not bounded, and an earlier draft of this page claimed it was.** The exemption
   keeps an undisclosed row while later sessions go on admitting new jobs, so a second crash before
   the first notices are dismissed leaves both cohorts pending, and repeated crashes can keep the set
   growing. [ADR 0047](0047-single-active-job.md)'s ceiling and
   [ADR 0074](0074-workload-bounds.md) decision 2's queue bound cap **concurrency**, not accumulated
   durable history — and `internal/app/jobrunner.go:30`–`34` qualifies even that, bounding the owners
   waiting inside `acquireSlot` rather than a burst of concurrent starts. Three different bounds are
   in play and only two of them hold here: the **read** is bounded by decision 4's page, **admission**
   is bounded by those ceilings, and the **retained undisclosed set is bounded by nothing but
   dismissal**. This record proposes no cap, no forced dismissal and no release gate for it; it states
   the cost of the until-dismissed contract so that whoever implements it is not surprised by a
   growing table.

3. **Dismissal is a disclosure fact and never a cleanup claim.** `interrupt_disclosed_at` records
   that the operator was told. Writing it must not touch `job_recovery`, must not change the job's
   state, counters or error, and must not be rendered as resolving anything. The retained-records
   summary keeps counting those records afterwards, and the notice's own wording says the records
   stay. A dismissed notice and a cleared residue are different facts and the interface must not
   blur them.

   The bulk control dismisses **exactly the entries it listed**, in one transaction, and is named for
   that — "Dismiss these N" — with the number of undisclosed entries beyond the page shown beside it.
   It is deliberately not called "all": decision 4 reads a bounded page, and a control claiming the
   whole undisclosed set would claim more than it did.

4. **The read is bounded and contacts nothing.** It returns a bounded page of undisclosed interrupted
   jobs, newest first, with a total count — the shape ADR 0020 lines 71 to 74 already require of the
   retained-record summary: "Expose a bounded read-only summary with the number of terminal jobs
   holding recovery records and the number of those records, independent of the recent-job list
   limit", with an unavailable read reported as unknown and "never a zero count or a successful
   cleanup claim". It performs no listing, HEAD, DELETE or authentication, consistent with ADR 0020's
   containment. ADR 0074 decision 2 bounds queue *admission* and is not the oracle for this read.

5. **Each entry reports what the retained facts prove, and the recovery ledger is never the
   classifier on its own.** An earlier draft of this page gave three cases keyed on `job_recovery`
   rows, and both halves of that were wrong: the cases were not exhaustive, and the ledger was asked
   to carry a verdict it cannot.

   First, **admission**, from `interrupted_state`:
   - `queued` — the job was never admitted to execution, so no request was made and nothing was
     attempted. This is the only case where that can be said.
   - `planning`, `running` or `verifying` — the job was admitted. Work **may** have started, and the
     facets below say what is retained about it.

   Then, **additively and without one erasing another**:
   - **Recorded item facts come first.** `job_items` carries the acknowledged execution and
     verification observations, and ADR 0031 line 94 governs them: "If an existing live HEAD has
     already succeeded, later closure, cleanup or bookkeeping problems must not erase its
     copied/verified object and byte contribution." An acknowledged `copied` or `verified` item
     therefore stands as it is, and a recorded `412` conflict is its own distinct fact.
   - **`job_recovery` rows say residue may exist remotely**, where the route writes them.
   - **No recovery rows does not mean nothing was attempted.** The download route writes **no**
     recovery rows at all: `StartDownload` calls `local.DownloadInto`
     (`internal/app/localtransferservice.go:1030`), whose variadic tail is `recorders
     ...*DownloadRecorder` (`internal/transfer/local/download_execute.go:50`) — an *item* recorder,
     with no upload-style recovery callback. An interrupted download can therefore hold item
     observations, a stage, and placed or partial files in the operator's own directory while
     `job_recovery` is empty. Any wording that reads zero recovery rows as "nothing happened" is
     wrong for every download.
   - **Local residue is its own facet**, for the routes that place bytes on the filesystem, and is
     **not completely determined by either ledger**. The download item records do retain the local
     destination and staging identities and their execution and publication observations, so the
     ledgers are not silent about it; what neither supplies is a fresh inventory of what is in the
     directory now. The additive rule above preserves those recorded facts; the entry must not
     present them as a current filesystem reading.
   - **Absence of evidence is reported as absence**, never as a clean outcome — ADR 0020 line 74's
     rule for the retained-record summary applies here too.

   And the case that needs the most care: a `committing` row with no `committed` row makes **the
   recovery ledger inconclusive for that one name**. It is not a verdict on the operation.
   `internal/transfer/local/upload.go:605` captures that write's error into `recordErr` and execution
   continues: `verifyUpload` still runs at `internal/transfer/local/upload.go:653` and the recording
   problem is retained as an issue at `internal/transfer/local/upload.go:654`, so item facts can
   already be acknowledged alongside the missing `committed` row. ADR 0031 line 75 says the same of
   this exact pair: "a committed-record refusal retains the affected stage and permits the existing
   read-only live HEAD check to report witnessed copied bytes." The entry therefore shows the item
   facts **and** the inconclusive ledger side by side, and describes the unresolved effect as
   unresolved — not as a failure, and not as a verdict over facts the item records already settled.

6. **The notice offers dismissal, not a restart.** It links to the ordinary transfer dialog and does
   not start anything itself. Three accepted contracts make a one-click restart the wrong affordance:
   - selection authority is one-shot and scoped ([ADR 0041](0041-scoped-selection-capabilities.md),
     [ADR 0073](0073-selected-resource-acknowledgement.md)), so a queued upload's authority
     deliberately does not survive the restart and the local selection must be made again;
   - a re-initiated transfer may replace a name the interrupted job had already published, so it
     needs [ADR 0081](0081-upload-overwrite-acknowledgement.md)'s acknowledgement and
     [ADR 0085](0085-reobserved-destructive-identity.md)'s identity comparison, which only the normal
     flow supplies. That holds **whether or not the earlier outcome was witnessed**: the name may
     exist for any reason, and this rationale needs no claim about what the interrupted job's records
     can resolve. An earlier draft said "the case nothing can resolve" here, which contradicted
     decision 5 two paragraphs above — a missing `committed` row leaves the ledger inconclusive while
     acknowledged item facts stand;
   - `RerunJob` already states the principle for every other type: "re-run supports copy jobs; a %s
     needs its own dialog (destructive halves keep their typed confirmation)"
     (`internal/app/transferservice.go:830`). This decision does not widen it and does not lift
     `rerun` from the deferred table, where `internal/app/v1capability.go:38` keeps it under the Copy
     token.

7. **A failed dismissal is a failure.** If the write cannot be made, the notice remains and the
   interface says the dismissal did not happen — the rule ADR 0020 applies to the classification
   write, for the same reason: an unavailable write is never a successful outcome.

8. **Where it appears.** A dismissible banner in the browser screen's shell, beside the existing
   `unproven-banner` precedent at `frontend/src/features/browser/BrowserScreen.svelte:93`, with the
   per-job detail and the per-job dismissal in the jobs drawer. The banner is the point of the
   decision: the operator should not have to go looking. **This placement is the one product choice
   this record proposes rather than reports**, and it is the operator's to redirect.

## What this record does NOT discharge

- **It is not the durability measurement.** It discloses what the records say; it does not establish
  that the records are right after an abrupt crash. The combination the
  [register reconciliation](../audits/2026-10-18/register-reconciliation.md) names — a desktop
  startup recovering from an abrupt in-flight crash, fsync and power-loss durability, and the
  non-Linux targets — is untouched by this and remains unmeasured.
- It restores no queue, resumes no job, and reclaims nothing.
- It lifts no capability: copy, mirror, bulk delete and `rerun` stay refused.
- It changes neither `FailInterrupted`'s classification nor ADR 0020's retention, and it adds no
  cleanup audit event.
- It does not decide whether a *successfully completed* job's retained records should ever be
  disclosed or cleared. That is ADR 0020's open reclamation question and stays open.
- It does not classify local residue, reconcile it, or offer to remove it. It reports that a route
  which places bytes locally was interrupted; what is in the operator's directory is theirs to look
  at, and ADR 0020's containment forbids this page proposing any sweep of it.

## Consequences

- One migration adding **three** nullable columns to `jobs` — `interrupted_at`,
  `interrupted_state` and `interrupt_disclosed_at` — and one clause on `PruneTerminal`'s `DELETE`
  exempting an undisclosed interruption. No existing row changes meaning. Rows interrupted **before**
  this ships have `interrupted_at` NULL and so produce no notice — chosen deliberately, because
  back-filling them would mean matching the reason prose, which is the fragility decision 1 exists to
  avoid. Those jobs remain in history exactly as they are and stay subject to the existing retention
  rules — which means the ones holding `job_recovery` or `job_items` rows are still protected by
  `PruneTerminal`'s existing exclusions, exactly as they are today.
- `FailInterrupted` writes two more columns in its existing transaction, so the count it returns and
  the set the notice derives stay consistent without a second source of truth. Writing the state it
  overwrote costs nothing: it is already reading the row it updates.
- One new bounded read on the jobs store, one new write for dismissal, one retention clause, and one
  frontend surface. The read joins `job_items` and `job_recovery` for the per-entry facets, which is
  the same aggregate shape ADR 0020's summary already uses.
- An undisclosed interruption is now retained past `JobRetentionDays`, and **the set of them is
  bounded only by dismissal**: earlier undisclosed rows stay while later crashes add new cohorts, so
  repeated crashes without dismissal keep it growing. That is the accepted cost of decision 2's
  guarantee and the same contract decision 2 states; dismissal is what ends it for a given row. No
  cap is proposed here.
- An operator who dismisses everything is left exactly where they are today, by their own choice,
  with the records still retained and still counted.

## Limits

This record is a proposal and nothing in it is implemented. It makes the application say out loud
what it already knows; it does not make the application know more. The valuable-data hold is
unchanged, the 0.1.0 test plan is unchanged, and no refusal is relaxed.
