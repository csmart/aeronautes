# ADR 0030 — Copy item recording and complete access

**Status:** The initial contract passed independent local review on 8 September 2026; the history-access and effect clarifications require review before implementation.
**Scope:** The first Copy caller and item-access migration under [ADR 0028](0028-transfer-item-records.md), with explicit interruption and rerun handling.

## Problem and oracle

The [archived actual-service failure-identity reproduction](../experiments/2026-09-08-copy-failure-identities.md) requires one, 201 and 401 failed source names to count once each and remain individually accessible.
The current Copy caller still reports two, 400 and 400, retains only bounded samples and has no item query in the job drawer.
[ADR 0028](0028-transfer-item-records.md) supplies immutable logical records and guarded completion; [ADR 0029](0029-listing-identity-decoding.md) supplies lossless native listing identities.
Neither foundation enables recording in an actual transfer.
The oracle is the existing actual-service fixture and its independent namespace, audit, recovery and reopening assertions, together with ADR 0028 and [DESIGN](../DESIGN.md) §§46/47/50/51.
The same eight-case archived probe was replayed on master `1d4bd66f4f152ae05d277780e4b2f815745ad478` after PR59: six intended failures, two passing controls, no skips or unexpected diagnostics and unchanged application/overlay hashes.

The next implementation preserves every failed identity and both execution and verification facts under that identity.
It refuses dependent effects when required recording fails, exposes retained uncertainty and prevents the built-in rerun action from treating absent or unresolved records as retry permission.
The complete per-effect journal, remote identity and authority, stronger verification and physical durability remain separate gates.

## Checked execution boundary

Add a small checked recording contract to the Copy executor alongside its existing advisory progress events.
Admission, final execution and verification callbacks return errors; progress events cannot stand in for these acknowledgements.
Callbacks carry typed domain results and typed diagnostic fields, with the application adapter mapping them to the existing job-store values.
The executor must reject a partially configured recording contract; the actual version-one caller must supply the complete contract.
Existing unrecorded executor consumers retain their current mode until explicitly migrated.

Construct each record from the captured request, exact relative key and executor's actual route.
Source and destination locations contain their own bookmark, container and full object name.
An engine candidate's staging object is the executor's staging prefix plus `batch/` plus the exact key; a direct native candidate has no staging object.
An empty relative key is valid only when both full object locations are valid.
Validate all constructed values without a database write before attempting admission, including UTF-8, nonempty full names and nonnegative advisory listed bytes.
Refuse an invalid representation before its dependent effect without deliberately tripping the shared persistence latch.
Expose the foundation's existing identity/intent validation as a pure operation and use the same implementation at admission; do not duplicate those rules in the caller.
The strict listing decoder remains required because checking a string after lossy decoding cannot recover its original identity.

The first implementation admits each selected source item synchronously before buffering it or entering its execution path.
One-item transactions satisfy the existing bounded admission contract and ensure every name in a dispatched engine batch was acknowledged beforehand.
This adds transaction overhead; do not claim a throughput improvement or silently weaken acknowledgement to improve it.
An unchanged name also has separate admission, execution and verification writes, potentially three acknowledged transactions per name.
Measure both that recording cost and an actual unchanged-scope Copy on the owned fixture, reporting the test environment and scope instead of extrapolating a production throughput claim.
Batching those acknowledgements is a separate measured optimisation that must preserve the same barrier and bounded memory.
The engine batch itself retains its current count and byte limits and does not collect the complete namespace.

| Initial verdict | Planned | Admitted | Execution handling |
| --- | --- | --- | --- |
| New or Unknown | Yes | Yes | Record before any execution or buffering; a later HEAD may still establish no attempted mutation |
| Changed | Yes | No | Record an overwrite refusal and preserve the existing exact refusal exclusion |
| Same | No | No | Record the comparison as `same`; the fresh verification pass remains independent |
| Destination only | No record | No | Continue ignoring it for Copy; do not invent a source identity |

The executor remembers a required-recording failure and returns it through `Handle`, batch flushing, `Finish` and verification.
No later call to `Finish` may dispatch a buffered batch after that failure.
Already admitted, never-dispatched buffered names may receive `not_attempted` observations while storage remains writable.
Names whose engine call started retain unresolved effects when their final outcome is missing.
Internal authentication retries stay within the same logical admissions and do not create extra failed names or new retry authority.

Every required item-write refusal latches the shared job store for this process, including validation errors, duplicates, conflicting or late observations, missing rows and SQL failures.
That latch refuses new jobs of every family using the store, not only the affected Copy.
Already admitted work may save observations and finish when storage becomes writable; public record reads still report unavailability during the latch.
This is not a global mutation interlock: other admitted workers and external/native processes are not proven stopped by a process-local diagnostic.
The affected Copy executor stops its own further dependent work and retains the exact previously acknowledged facts.

## Execution effects and diagnostics

Return a typed final outcome from native copying, staged commit and symlink copying instead of emitting an irreversible final callback inside their nested helpers.
The enclosing item operation records its observation after required post-copy actions have completed or been refused.
In particular, staged symlink cleanup must not occur after an already-recorded successful final outcome.
Classify effects from the witnessed execution boundary, never from an arbitrary error string, a final HEAD or an engine exit alone.

| Witnessed boundary | Result and effect knowledge |
| --- | --- |
| Source read/size failure before any destination mutation | `failed`, `not_attempted` |
| The existing native symlink re-HEAD refuses an occupied destination before any destination mutation | `conflict`, `not_attempted`; do not add a HEAD to the ordinary native copy path |
| A destination PUT was invoked, including a conditional 412 refusal, a lost response or subsequent verification failure | `failed` or `conflict`, `unresolved` |
| Engine invocation started, then failed or left an item without a final observation | Failure or missing observation with unresolved effects, including possible staging writes |
| Later staged source/read/commit refusal after engine invocation | Retain unresolved staging effects even if the live PUT was never invoked |
| Reported copy and existing verification succeeded, with no known later problem | `copied`, `reported_applied`, retaining the reported copied-byte value |
| A reported copy has a required recovery-record, stream-close or cleanup failure | Retain `copied` and copied bytes with a typed problem and unresolved effects; do not relabel the successful copy as a failed copy |
| Buffered item cancelled before its engine invocation | `not_attempted`, `not_attempted`; no child or destination mutation |

A committed-record write failure after a live DLO PUT prevents the affected staging cleanup and further dependent commits.
Retain the earlier committing intent and captured prefixes; neither is cleanup authority.
Check staging-delete and relevant stream-close errors instead of silently losing them.
The final item stores one typed diagnostic; simultaneous issues must also remain in the job's overall problem result, and this limited record is not advertised as a complete per-effect issue history.
A missing final observation after a post-effect recording failure remains unresolved through the durable admission.
The conservative issued-PUT rule follows ADR 0028's requirement that `not_attempted` means no dependent mutation was invoked.
An HTTP refusal must not silently change that meaning; finer acceptance knowledge requires its own future contract.

Diagnostic stage and code come from known operation branches, typed context/network errors and typed Swift status errors.
Unknown errors use the explicit unknown code.
No provider body, header, URL, credential, raw engine output or arbitrary error string enters a new item record or its DTO.
Hostile-payload fixtures must inspect the persisted record and public DTO, not just a display formatter.
Existing legacy job-error/audit text still needs the wider SEC-08 treatment; this migration must not claim that those paths are certified by safe item diagnostics.

## Verification and exact completion

Persist each fresh comparison result before advancing the verification stream.
The callback builds source and destination locations from the same captured request and exact relative name as execution.
When execution already recorded the key, `RecordComparison` must match both identities and append the verification observation without changing admission.
When a new source name appears only during verification, create a non-admitted comparison identity through that API.
It can receive `verified` or verification `failed`; it is never invented as an execution failure, conflict or successful copy.
Same and HEAD-proven equal Unknown pairs verify; New, Changed and unproven/error Unknown pairs fail verification with typed reasons.
Destination-only names remain outside Copy, and execution-time overwrite refusals remain excluded from verification credit.

Verification returns its accumulated report alongside any stream, refusal-record or recording error.
Persisted earlier observations survive a later error, and an unvisited reported copy remains visibly unverified.
Preserve simultaneous planning and executor-finish errors and explicitly complete refusal-record closure before saving the terminal result.
Reconciliation or closure problems cannot disappear behind an unqualified complete result.

Opt new `StartCopy` jobs into record version one only after the service/UI, interruption and checked-executor prerequisites below are implemented and reviewed.
Queued cancellation and failed startup must use the same item-derived terminal completion as executed jobs.
Use durable facets to choose an honest failed, partial or cancelled result; failures, conflicts, refusals, unresolved effects, unverified copies or typed problems forbid complete.
`FinishFromItems` derives distinct failed names inside the terminal job/audit transaction, rather than adding capped execution and verification samples.
Append the failure/facet summary to the terminal audit from those same transaction-local totals; a separate earlier read cannot certify that the audit and saved counter agree.
Preserve source bookmark/resource authority for the terminal audit and destination authority for recovery records.
Other live counters continue to use their established report inputs; advisory listing bytes do not become verified logical lengths.

## Complete access and interrupted records

Add bounded item-page and item-total methods to `JobService`, with explicit DTO mapping and regenerated Wails bindings.
Also add bounded job-history access through the store and service, because the current drawer loads only fifty recent jobs and cannot otherwise discover older retained records.
Keep the live recent-job poll and its active-job behaviour separate from an explicitly paginated history view.
History pages preserve the account filter, order by creation time and job ID together, and use the preceding page's last job ID as a keyset cursor resolved inside the read transaction.
Validate that a nonempty cursor still names a job in that filter; a missing or mismatched cursor is an error requiring a fresh page, not an empty history.
Use a default of fifty history jobs and a maximum of one hundred, return a next cursor only when another row exists and refuse read failures or a recording latch without partial results.
New jobs arriving ahead of the cursor must not duplicate or hide older retained jobs; jobs with identical creation timestamps require the ID tie-breaker.
Expose each item's source, destination, optional staging location, planned/admitted flags and separate execution/verification observations.
Use decimal strings for row identifiers and cursors across the JavaScript boundary, validating them before the existing store query, so large SQLite identifiers cannot lose precision.
The methods distinguish a historical job with records not captured, a valid recorded-empty result, available records and an unavailable query.
A failed query returns no partial page or fabricated zero totals.

The drawer offers item details for active and terminal jobs, shows one bounded page at a time and provides navigation to every retained identity beyond 200, including those in older history pages.
Bound both displayed job and item pages, expose first/next navigation with clear position and loading/error states, and reset dependent item selection when a history page or account changes.
Show both observations under the same name, and label missing execution, unresolved effects, unverified copies, refusals and conflicts separately.
Facet counts may overlap and must not be added as disjoint failures.
Render exact identities as escaped text with an accessible way to inspect the full value; control characters, bidi controls, zero characters and markup-shaped names must not become indistinguishable or executable.
Retain raw values in the DTO without normalising or parsing their display representation.
An active view is explicitly incomplete; refresh the first page and totals when the job reaches a terminal state.
Ignore stale responses after switching jobs, pages or accounts, and never retain a previous job's rows under a new job heading.

Within `FailInterrupted`'s existing transaction, project the durable distinct failed-name count for version-one jobs before setting the interrupted terminal state.
Keep all item observations and recovery records unchanged; missing execution remains unresolved and missing verification remains unverified.
Other interrupted progress counters can be incomplete and must be labelled as the last saved progress, with the retained item facts available independently.
A decoding/query/write error aborts the whole projection instead of presenting zero failures.
Latch job-store unavailability on that failure so `Get`, `List` and new-job admission cannot report stale interrupted work as current; the existing startup log alone is insufficient.
The interrupted reason explicitly labels other counters as the last saved progress, avoiding a display heuristic that infers recovery state from an unrelated failure.
Historical version-zero jobs retain their explicit records-not-captured status; reopening cannot fabricate missing history.
No new successful terminal audit is invented by this startup sweep.

The backend rerun guard executes before job creation, bookmark pinning, authentication or any remote call.
Refuse unavailable or uncaptured item records, unresolved effects and unverified reported copies with a constant explanation directing the operator to the retained facts.
The uncaptured-record refusal takes effect with the interruption/rerun slice, before caller opt-in.
It intentionally removes one-click rerun for every historical Copy and for new unrecorded jobs created between those slices; an explicit fresh Copy through the normal dialog remains available.
Do not infer that a historical complete state proves missing item effects were recorded.
The UI displays the same reason and cannot bypass the backend guard.
A permitted explicitly requested rerun still starts a new job and runs normal request validation and current planning; it cannot replay an old admission.
This guard does not certify the existing display-locator reconstruction, immutable bookmark identity, native approval or protection against all concurrent writers; their P10/P11/P14 gates remain open.

## Reviewed implementation sequence

1. Review this concrete contract and the current-master replay of the archived eight-case reproduction before changing application hooks or enabling a caller.
2. Add the bounded job-history store/service, typed item-page/totals service and complete bounded drawer view, with backend identity/cursor/error tests and frontend pagination, stale-response, identity-rendering and availability tests.
   Existing actual transfers still create version-zero jobs; demonstrate version-one access using deliberately constructed store fixtures and state that limit.
3. Add the interrupted-record projection and backend/UI rerun refusal, preserving historical jobs and testing transaction rollback and absence of job creation or network effects on refusal.
   Replay records through an orderly child database reopen; do not call it a full app restart or physical durability test.
4. Implement checked Copy admission, execution and verification hooks with typed effects and post-copy issue handling, then wire version-one job creation and item-derived completion only when all prerequisite slices are reviewed.
   Keep this caller migration together with its actual-service regression evidence so partially configured recording cannot ship.
5. Reproduce and migrate local upload and download separately under ADR 0028, bringing their filesystem, authority and post-placement issue prerequisites alongside those effects.

Every commit passes independent local review before the next implementation slice or push.
Each delivered PR needs all five latest actual checks with current master incorporated, a protected merge of the verified head and all five resulting master checks.

## Required migration evidence and limits

Maintain the actual mock and two-account SAIO 1/201/401-name fixtures, with one successful engine-routed object and exact native failure identities.
After migration each failed key must have both relevant observations, every page must be reachable exactly once, job and audit failure totals must agree and all original namespace/recovery/reopen assertions must still run.
Before caller opt-in, constructed store/service fixtures must also reach records in jobs beyond the newest fifty and through multiple history pages, including tied timestamps, new concurrent jobs, account filters, invalid/pruned cursors and unavailable reads.
Frontend tests must navigate from those older jobs through more than 200 item identities while retaining bounded displayed rows and rejecting stale page/account responses.
Add comparison-only identities, same/refused/conflicting names, empty/newline/Unicode/zero-containing keys and preserved valid replacement characters.
Exercise the real admission barrier before engine and native dispatch, and a SQL observation refusal after an independently witnessed destination effect.
Require the affected executor to start no subsequent dependent mutation, retain acknowledged intent/recovery and expose record unavailability and the shared new-job latch.
Exercise queued cancellation, cancellation of a buffered unstarted batch, cancellation between staged commits, verification stream failure after prior failures and simultaneous plan/finish problems.
Check committed-record refusal, staging cleanup failure, native/staged/symlink post-copy ordering and typed hostile diagnostics.
Use acknowledged barriers where ordering matters, not sleeps that assume work reached a boundary.

Each fault case must state its exact permitted effects and inspect the complete owned namespaces, unselected sentinels, another job's staging data, shared DLO/SLO aliases and segments, audit authority and retained recovery facts.
Missing observations and unresolved facets must survive orderly child reopening without remote traffic or automatic cleanup.
Deliberately remove admission/observation checking, distinct counting, complete pagination, refusal exclusion, typed diagnostic mapping, interrupted projection and rerun refusal; the matching behavioural assertions must fail.
Run the relevant full race suite, frontend checks, native build, binding drift, source provenance and real-Swift checks before delivery.

Do not award new scenario coverage or close a finding without its own maintained evidence.
Engine parser identity, stronger content/metadata fidelity, per-request effects, cancellation quiescence, lost-suffix history, native child lifetime, full application restart, fsync/power loss, export/retention policy and all six platform targets remain explicit wider work.
Mirror starts, changed Copy overwrites and automatic recovery reclamation remain refused.
The valuable-data release hold remains in force.
