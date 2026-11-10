# ADR 0104: a job pauses at a unit boundary, keeps what it holds, and resumes its own run

**Status:** Proposed for 0.5.0 (10 November 2026).
Finish-line decision 17 asked for a contract of its own, and the roadmap puts pause and resume in this release.
**Scope:** How a transfer job is paused, what a paused job holds and gives up, how it resumes, how else it ends, and what the drawer shows.
It changes neither the single active slot ([ADR 0047](0047-single-active-job.md)), the queue depth and the units the routes hand to the engine ([ADR 0074](0074-workload-bounds.md) decisions 2 and 3, [ADR 0103](0103-directory-upload.md) decision 4), the download destination reservation ([ADR 0078](0078-overlapping-download-destinations.md)), the outcome rules of an interrupted local transfer ([ADR 0025](0025-local-interruption-outcomes.md)), the per-item records ([ADR 0031](0031-upload-item-recording.md)), the overwrite acknowledgement and its fixed point ([ADR 0081](0081-upload-overwrite-acknowledgement.md), [ADR 0085](0085-reobserved-destructive-identity.md), [ADR 0102](0102-versioning-matrix-fixed-point-and-archives.md)), nor the interrupted-job disclosure ([ADR 0088](0088-interrupted-job-disclosure.md)).

## Context

A transfer job runs from planning to a terminal state, and the only control the operator has is to cancel it.
Cancellation kills the engine child and ends the job with what it did recorded; a job cancelled by mistake cannot continue, and only a copy can be run again, as a fresh plan.

The routes already work in bounded units.
The download route and the copy executor hand the engine at most 500 names or 64 GiB of planned bytes at a time, and the upload route hands it one source directory's files at a time; a native-routed item is a unit of its own.
Each unit is staged, verified and recorded before the next one starts, and every unit loop already looks for cancellation at its head, before it starts anything.

The runner admits one job at a time.
A job waits in the queue for the slot, runs its whole body under it and gives it back at the end.
While it runs, the job's own goroutine holds everything the remaining work needs: the plan, the sessions under the bookmark generation it pinned at its start, the acknowledged baselines in its ledger and, for a download, the captured destination root.

Finish-line decision 17 recorded a reading, not a decision: a pause that finishes the current batch, and a resume that continues from the recovery records.

## Decision

1. **A pause is a request that takes effect at the next unit boundary and kills nothing.**
   `TransferService.PauseJob` marks a queued, planning or running upload, download or copy job as asked to pause, and refuses any other job.
   The route finishes the unit in hand, the engine child's batch or group or the native-routed item in flight, settles and records its items as it does today, and at the head of the next unit stops instead of starting it.
   The boundary is the head of a unit and nowhere else: the cancellation checks inside a unit, such as the placement of a batch the child has delivered or the steps of one native-routed item, are not boundaries, and a job never parks there.
   The job then enters a new non-terminal state, `paused`.
   A pause asked while the job is queued or planning takes effect at the first unit boundary: the job takes the slot, plans as far as its first unit, and parks before it transfers anything.
   A job that reaches its end before a boundary finishes as it would have, and the request is forgotten.
2. **A paused job is parked, not rebuilt.**
   The job's own run waits at the boundary.
   It keeps its plan, its sessions and the bookmark generation it pinned, its ledger and, for a download, the captured destination root; a copy that plans as it lists keeps its place in both listings.
   Nothing is planned again and nothing is read back from the records in order to continue.
   It gives up the job slot, so another job may run while it waits, and takes a place in the queue instead.
   A paused job counts against the queue depth of ADR 0074 decision 2 from the moment it parks until it is running again or has ended, so a start is refused when the jobs waiting and the jobs paused together fill the queue, and the refusal says so.
   A job that parks was running, and so was counted as neither waiting nor paused when it was admitted; it parks even when the queue is full, because refusing would leave a running job that cannot be paused.
   The jobs waiting and paused together can therefore exceed the depth by the number of active slots, which is one, and by no more.
   What parked runs retain, their plans, sessions, bookmark leases, download roots and copy spools, is bounded by the depth and that allowance; no second budget or setting is added.
   At the boundary it holds no engine child, no request in flight and no unit half done.
3. **Resume continues the same run.**
   `TransferService.ResumeJob` withdraws the request.
   A job that has not yet reached its boundary simply carries on.
   A paused job waits for a slot in the queue place it already holds, so a resume is never refused for a full queue and adds nothing to it, and it stays `paused` until it has the slot; then it returns to `running` and starts its next unit, with its counters and item records continuing.
   When it has the slot it asks whether the job store still records, as a launch does before a body starts: if another job's lost record has stopped the store while this one waited, the run starts no further unit and ends as a lost record of its own ends it, with what it staged kept.
   The overwrite question is not asked again: the run still holds what the operator was shown for each acknowledged item, in the ledger once the item is admitted and in the acknowledgement captured at the start until then, and the fixed point compares it against a fresh read as always, so a replacement whose predecessor changed while the job was paused is refused and recorded, not repeated.
4. **A paused job ends by resuming, by being cancelled, or with the process.**
   Cancelling a paused job ends it as a cancel seen at the same boundary ends a running one, by the route's existing rules and with no new one.
   The items the run had admitted and not started are recorded as not attempted.
   Names it had not yet admitted get no record, as with any cancel; for a copy that plans as it lists, the names it had not yet listed are unknown to the run, and nothing is read from either store to learn them.
   The terminal state is the one those rules give: `cancelled` only when the run holds no outcome but the cancellation, and `partial` whenever something had landed, failed, been refused or been left uncertain before the pause, as ADR 0025 requires even when nothing succeeded.
   That ending starts no transfer and sends no request to either store, so it does not wait for the slot.
   It does finish the run's local work, with the code and the guards any cancel uses: a download confirms the native publications it has in hand and runs its guarded stage cleanup.
   That cleanup removes the stage and what the job planned and did not place only where its existing checks allow; a displaced stage, entries the job does not own, refused bytes and removals that fail are retained and reported exactly as they are for a running job, and nothing is cleaned after a required record failed.
   The job keeps its destination reservation, its captured root and its pins until that work has returned, not merely until its row is terminal.
   Quitting the application cancels a paused job as it cancels a running one.
   A process that dies with a job paused leaves the row to ADR 0088: the restart records it as it records a job interrupted while running, which is what a parked job is to that record's question of whether any request was made, the notice reports what the records prove, and nothing resumes, because the plan, the captured root and the binding lived in the process that is gone.
5. **One job still runs at a time.**
   A paused job is not running.
   It is quiescent at a boundary where it holds nothing in flight, exactly as it is between any two of its units, so pausing one job, running another and resuming the first interleaves no more than cancelling the first, running the second and starting a new job for the rest does today.
   What a pause adds is time between a job's planning and its later units, which a long job already has.
   Every item still to be committed is checked at its own commit as before: an acknowledged replacement against a fresh read of its predecessor and of the container's versioning state, a new object by its conditional create, and a downloaded file by a placement that never replaces.
6. **What the operator sees and what is recorded.**
   The jobs drawer offers Pause on a queued, planning or running upload, download or copy.
   While the request waits for its boundary the row says the job pauses when the current batch finishes; a paused row offers Resume and Cancel beside what was done and, where the plan knows it, what remains; a resumed job waiting for the slot says so.
   The audit trail records each pause that takes effect and each resume that restarts work as applied operations on the job; a request that never took effect is not an event.
   The job's terminal record is unchanged in shape.
   The bookmark a paused job uses can be neither edited nor removed until the job ends, as with a queued or running job, and the refusal names a paused job among its reasons.
7. **Scope and inventory.**
   Upload, download and copy pause and resume.
   The deletion jobs do not: their listing passes are the boundary a cancel already honours, and a destructive job left half done on purpose is a trap for the operator.
   Mirror stays refused.
   `PauseJob` and `ResumeJob` join the scenario inventory as session rows beside `CancelJob`, with the transfers' v1 classification, through the drift route of [ADR 0069](0069-preserve-covered-scenario-cells.md).

### Why these shapes

Pausing by killing the child, as cancel does, was considered and not taken: a killed child leaves a unit's stage behind and its items unresolved, which the unit boundary exists to avoid, and the operator who pauses means to continue.

Resuming from the records, the reading decision 17 wrote down, was considered and not taken.
It needs a second way to build a run, decoding items from the ledger and re-establishing the selection authority, the sessions and a download's captured root, when the paused process already holds all of them; and it would leave two descriptions of what remains, the plan in memory and its reconstruction.
A copy that plans as it runs has no record at all of the names it has not yet listed, so its remainder could not be rebuilt from records without planning again.
That cost buys only a resume across a restart, which this decision does not offer and which needs its own answer for the authority a restart loses.

Resuming as a fresh plan, as `RerunJob` does, was considered and not taken: a fresh plan asks the overwrite question again and loses the item-level history.

A paused job that keeps the slot was considered and not taken: the usual reason to pause is to let something else run.

Showing a resumed job that waits for the slot as `queued` was considered and not taken: ADR 0088 reads `queued` as a job that had made no request, which would be false of it.

## Required proof

- Each route, at the service boundary on the mock: a pause asked mid-run takes effect after the unit in hand, whose items are executed and verified; no later unit has started and the wire shows no request for a later item; the engine child of that unit ended normally; the job is `paused` and a second job starts and completes while it is.
- Resume: the remaining items run under the same job, each executed once, with the counters continuing; an acknowledged replacement whose predecessor changed while the job was paused is refused at the fixed point and recorded with both identities; a resume after the job store has stopped starts no further unit and sends nothing.
- A pause asked while the job is queued parks it before its first unit, and a resume asked before the boundary lets the job carry on without ever being `paused`.
- A resumed job waits while another job holds the slot, stays `paused`, and runs when the slot frees.
- Cancel while paused, on each route: the terminal state the route's rules give, including `partial` for a run whose earlier unit failed with nothing landed and for a copy whose earlier name was refused for a conflict; the admitted, unstarted items recorded as not attempted and none invented for names not yet admitted or listed; and no request sent after the cancel.
- A paused download cancelled while another job, on a destination that does not overlap, holds the slot: an unchanged stage the job owns is removed with the directories it planned; a stage an existing guard refuses is retained and reported as it is for a running job; and the reservation and pins are released only when that finalisation has returned.
- The queue: a start is refused once the jobs waiting and the jobs paused fill it, a paused job that ends frees its place, and a resume asked while the queue is full is accepted and waits in the place the job already holds; a running job parks although the queue is full, which leaves the jobs waiting and paused one above the depth and no more.
- A pause asked while a delivered batch is being placed, or during a native-routed item, takes effect at the next unit head and not inside the unit; a copy whose plan ends with an engine batch still pending parks before that batch is dispatched, and a cancel there records its names as not attempted.
- A row left `paused` by a dead process is recorded as interrupted while running and refuses a resume; shutting down with a paused job returns.
- The same pause and resume of an upload with two folders passes against the mock and the owned Swift fixture.
- The drawer: the controls each state offers, the wording while a pause or a resume waits, and the paused row.

## Limits

A pause takes up to one unit to take effect, which for a download or a copy is up to 500 names or 64 GiB and for an upload one source directory; the drawer says the job pauses when the current batch finishes and does not say how long that will be.
A paused job holds a place in the queue, so a queue filled with paused jobs admits nothing new until one of them resumes or ends.
Nothing resumes across a restart or after the application quits.
A paused job holds its bookmark's binding, so that bookmark cannot be edited or removed until the job ends.
The deletion jobs and Mirror are outside this decision.
