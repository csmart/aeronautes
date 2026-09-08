# ADR 0028 — durable logical transfer item records

**Status:** Accepted after independent local design review on 8 September 2026.
**Scope:** The first shared P12/P13 storage contract, followed by separately reviewed Copy and local-transfer caller migrations.

## Problem and oracle

The [current-master actual-service reproduction](../experiments/2026-09-08-copy-failure-identities.md) records one, 201 and 401 failed source names as two, 400 and 400 failures on both providers.
Execution and verification each cap their failure sample at 200, and Copy adds the two sample lengths.
Only the first name appears in existing public job/audit summaries.
Independent review reproduced those values and confirmed the absence of a complete per-identity query.
DS-08, SEC-08, DESIGN §§46/47/50/51 and the [valuable-data remediation order](../audits/2026-09-05/remediation-plan.md#execution-order-for-valuable-data-safety) require retained identities and exact failed-name accounting independently of display samples.

The initial shared contract records logical item admission, execution and verification facts.
It does not define a general workflow engine or claim a complete per-HTTP-effect journal.
Copy is the first intended caller; local upload and download need the same persistent identities beyond their current 200-record samples and will migrate separately.
Existing callers remain unchanged by the storage foundation alone.

## Identity and storage shape

A logical item is uniquely keyed by its job ID and exact item key.
The job identifies one logical operation attempt; a user-requested rerun is another job and cannot replace earlier facts.
Internal engine or transport retries do not create fictitious extra logical objects in failed-name counts.
Their per-request effect records and retry authority remain separate work.

For Copy, the item key is the source object name relative to the captured source prefix.
Keep full source, destination and any planned staging identities separately, using the typed location below rather than parsing display locators.
The locations describe the caller's captured logical scope; bookmark references are not immutable account versions or recovery authority.

| Location kind | Required fields | Forbidden fields |
| --- | --- | --- |
| `swift` | Bookmark ID, container and full object name | Local path, credentials, URL or headers |
| `local` | Absolute filesystem path as captured by the backend | Bookmark ID, container and object name |

All location fields and item keys must be valid UTF-8 and preserved verbatim.
Do not trim, clean paths, fold case, normalise Unicode or drop an empty relative key when the full object name is valid.
An identity that cannot be represented losslessly must be refused before its dependent effect; this record layer does not add support for filesystem names outside that contract.
Local path identity, parent symlinks, case collisions and native picker authority still require their P18/P19/P11 checks.
A stored path, bookmark, ETag or generated staging name is not a remote version identity, native approval, exclusive ownership or permission to retry.

Add two tables through a new migration, leaving historical jobs without item records unchanged:

| Table | Immutable data and keys |
| --- | --- |
| `job_items` | Integer row ID; job foreign key; exact item key with binary comparison and a unique `(job_id, item_key)` constraint; typed source/destination/optional staging JSON; planned/admitted flags; advisory listed bytes; creation time |
| `job_item_observations` | Item foreign key; phase; result; effect knowledge; typed diagnostic stage/code/status; reported copied bytes; creation time; primary key `(item_id, phase)` |

The migration also adds `item_records_version` to jobs, defaulting to zero for existing and unmigrated callers.
Only a new Copy, upload or download job may opt into version 1 at creation; the mode cannot be enabled, disabled or changed later.
Ordinary existing job creation keeps version zero until its caller migration is reviewed.
All item writes and item-derived completion require version 1.
Reads for a version-zero job return the distinct non-secret result that item records were not captured for that job, not an available empty ledger and not a claim that recording failed.
A valid version-one job with no selected items can have an available empty ledger.
This distinction must survive reopening and be exposed by the later service/UI adapter.

Use explicit non-null values for identity fields, including an allowed empty relative key; the existing helper that turns an empty string into SQL NULL is unsuitable for those keys.
Validate the typed location shape and encoding before serialisation, and never store arbitrary caller-supplied JSON.
The integer row ID supplies a stable pagination cursor independently of names containing newlines, slash distinctions, non-ASCII text or embedded zero characters.
Job and item foreign keys restrict deletion; no cascading discard of outcome facts.
The storage implementation must also make terminal-job pruning explicitly retain jobs with item rows.

## Admission and observations

The storage layer exposes the following bounded operations through the existing job store:

| Operation | Contract |
| --- | --- |
| Admit items | Atomically insert between one and 500 new item intents for one non-terminal job before dependent dispatch; reject an existing item key, conflicting identity or invalid input and roll back the entire batch |
| Record execution | Require an existing item; append its one execution observation atomically, checking the phase/result/effect rules below |
| Record comparison | Append a comparison or verification observation, creating a non-admitted identity row when a fresh comparison encounters a name without an execution record; never upgrade it to an admitted write |
| Read items | Return one job's records and their at-most-two observations, ordered after a row-ID cursor; default 100, permitted page size 1–200; distinguish unavailable data from an available empty page |
| Read totals | Compute distinct failed names and unresolved/unverified facets from durable rows, independently of any sample; return errors rather than partial totals |
| Finish from items | Derive the failed-name total and check unresolved facts within the existing terminal job/audit transaction; preserve the caller's established inputs for other counters until their own migrations |

The 500-item admission bound matches the current Copy engine batch bound.
Callers must split larger local selections into bounded admissions without collecting another full namespace in memory.
No network or filesystem operation may run inside a database transaction.
An existing admission is evidence of a prior attempt, not an idempotent dispatch token; returning an existing key must never authorise another external effect.
Identical repeated recording of an observation is idempotent local persistence.
A conflicting observation for the same item and phase is rejected and surfaced, retaining the first fact.
This does not permit overwriting the earlier observation to conceal a failed attempt.
Comparison recording against an existing key must match its stored source and destination identities; it cannot rewrite either identity or the admission flags.
Missing jobs and a nonzero cursor belonging to another job are errors, not empty pages.
Validate pagination arguments without latching a storage failure for a bad read request.

`planned` records whether the existing planner counted this name in its planned-object total; it is distinct from `admitted`, which records permission from this local recording boundary to attempt an effect.
A changed-destination refusal can therefore be planned without being admitted, and a comparison-only name can be neither.
Listed bytes remain explicitly advisory and cannot be renamed logical bytes; the existing DLO planned-byte defect remains a separate correction.

| Phase | Allowed results | Meaning |
| --- | --- | --- |
| Execution | `copied`, `failed`, `conflict`, `refused`, `same`, `not_attempted` | The caller's final logical-item execution observation, with independent effect knowledge |
| Verification | `verified`, `failed` | The verification observation for that same logical identity, including comparison-only identities |

Execution of a non-admitted row is limited to `same`, `refused` or `not_attempted` comparisons.
The record layer refuses `copied` or an effectful result without a prior admission.
Verification cannot credit an execution-time overwrite refusal as verified.
The recorder must enforce that incompatibility whichever observation is presented first.
Only `copied` may carry a nonzero reported copied-byte value, and all byte values must be nonnegative.
Reject unrecognised phases, results and diagnostic codes rather than accepting arbitrary strings into the record contract.

Effect knowledge is separately `not_attempted`, `reported_applied` or `unresolved`.
`not_attempted` requires the caller to know it did not invoke a dependent external mutation; an arbitrary error does not establish that fact.
`reported_applied` accompanies a reported copy, not a stronger content or concurrency proof.
An interrupted, ambiguous or failed effect remains `unresolved` unless the caller has the specific evidence required to say no mutation was attempted.
An admitted item without an execution observation also remains unresolved.
A verification observation cannot erase that uncertainty.
The caller migration must review the mapping at each real execution boundary; the store does not infer it from an HTTP aggregate count, error text, object HEAD or exit status.
`same`, `refused` and `not_attempted` have effect knowledge `not_attempted`.
`copied` may have `reported_applied` or `unresolved` effect knowledge; `failed` and `conflict` may have `not_attempted` or `unresolved` knowledge, never an invented applied copy.
An observation without a diagnostic uses explicit absent diagnostic fields, distinct from an actual problem with the recognised `unknown` code.
Diagnostic stage and code must be either both absent or both recognised values; an optional HTTP status requires a diagnostic and must be between 100 and 599.
Execution `failed`, `conflict` and `refused` observations and verification `failed` observations require a diagnostic, using the explicit unknown classification when needed.
Verification records have zero copied bytes and `not_attempted` effect knowledge because comparison itself does not authorise a mutation; they do not replace the execution observation's independent effect knowledge.
A copied item may carry a typed post-commit diagnostic and unresolved effect knowledge while retaining its reported copied bytes, as required by ADR 0023's successful-copy/post-commit-issue distinction.
That diagnostic is an item problem, not a failed copy, and it blocks unqualified completion.
This initial final-item diagnostic is not a complete history of per-effect or multiple post-commit issues; a local caller migration must preserve its existing exact issue totals and extend the record contract with its concrete issue evidence before claiming that wider history complete.

## Exact counts and completion

A logical name contributes exactly once to the failed-name total if its execution or verification observation has result `failed`.
Keep both observations and their reasons for inspection.
An overwrite refusal remains separate and never becomes a failed or verified copy solely because it exists in the ledger.
Conflicts are separately queryable and do not disappear behind a generic partial label.
Names never admitted or observed do not acquire failures merely because listing stopped.

Expose unresolved execution and unverified reported copies as separate facets of the same records.
A reported copy without a successful verification observation remains unverified, including when the later verification stream ends early.
These facets can overlap a failed-name total; they are not disjoint object sets whose counts may be added together.
Do not call a partial result proof of a successful write or a safe retry.

The first caller migration replaces sample-derived `ObjectsFailed` with the durable distinct-name total in the same transaction as terminal state, time and audit.
Other counters retain their current report inputs until separately migrated; this decision does not silently certify all byte or performance counters.
`complete` is forbidden when admitted execution is unresolved, reported copies remain unverified, a failure, conflict or post-commit diagnostic exists, or an overwrite was refused.
An illegal or unavailable completion must preserve the old job and item records and surface unavailability through the existing job-store mechanism.
The caller must select an honest partial/failed/cancelled state and reason before requesting completion; the store must not silently repair a contradictory success request.

## Recording failure and durability

Use the existing serialised `WriteTx` path and verified WAL/FULL connections.
An acknowledged transaction is the existing durability prerequisite; this ADR does not claim fsync, crash or power-loss validation from SQL aborts or orderly reopening.
Check every required admission and observation write.
A rejected admission prevents the affected dispatch.
A failed post-effect observation retains the previously committed item intent and recovery facts, marks record availability uncertain and stops that caller from starting further dependent effects.
The caller must stop or join outstanding work and report the exact witnessed limit; a process-local latch is not a global mutation interlock or a native child-quiescence guarantee.

Use a constant non-secret availability error for required item-record write failures.
New admissions refuse that latch, while already admitted work may still save its remaining observations and terminal summary if storage becomes writable.
Public item queries and totals report unavailability rather than presenting incomplete facts as an empty or complete ledger.
Do not replace the existing terminal-completion diagnostic or relax its admission guards.

Restart retains item rows and observations unchanged, including missing outcomes.
The storage foundation must protect them through `FailInterrupted` and pruning.
Before exposing a migrated caller, its separate implementation must project retained failures and unresolved facts into the interrupted job and UI, without claiming that reopening settles remote acceptance or stops an orphan child.
It must also prevent a rerun from treating unresolved item effects as a safe retry opportunity.
Full-app restart, process kill, native lifetime, lost-suffix history and physical durability remain explicit P12/P30 gates.

## Diagnostics, access and retention

The initial diagnostic contains only an enumerated stage, an enumerated failure code and an optional numeric HTTP status.
Stages cover planning, source read, engine, staging, commit, metadata, verification, local read/placement, cleanup, recording and unknown.
Codes cover cancellation, timeout, authentication, forbidden, missing, conflict, unsupported, I/O, incomplete, unverified and unknown.
An unclassified error retains the identity with the explicit unknown code; it is not silently dropped.
There is no free-form provider message, HTTP header/body, URL, token, credential environment or arbitrary error string in this new record format.
The caller adapter must derive codes from typed errors and known operation stages, with hostile-payload tests proving that only the allowed fields reach SQLite and DTOs.
This does not retroactively certify the existing job-error or audit redaction paths.
Permitted transaction identifiers can be added with their own typed capture and validation rather than an unrestricted string field now.

Bounded pagination must make every retained identity accessible, including every failure beyond 200, with no duplicates or omitted names.
Active records may acquire later observations; the UI must identify an in-progress view and refresh it at termination rather than present a live page walk as an immutable snapshot.
Terminal records cannot acquire new observations through the ordinary recorder.
The first caller/UI migration must distinguish available-empty, unavailable and unresolved results and display execution and verification under the same logical name.
A bounded summary is not complete failure access.

Do not automatically discard these item records through the current audit cap or terminal-job pruning.
Retain their owning jobs until a separate reviewed export/retention policy permits removal.
Storage growth, full disks and write failures are visible admission constraints, not reasons to truncate identities.
No archival/export guarantee is claimed by this initial retention rule.

## Implementation and required evidence

1. Review this storage contract and the already reviewed current-master reproduction before changing schema or APIs.
2. Add the migration, typed item/observation values, immutable bounded writes, complete paginated reads, distinct-name totals, guarded completion and retention protection in one storage-foundation commit.
   Test 1/201/401 and larger page-spanning sets, recorded-empty versus historical-unrecorded jobs, immutable version opt-in, the same key in different jobs/accounts, exact empty/newline/Unicode/zero-containing keys, conflicting duplicates, invalid phase/effect combinations, copied items with post-commit diagnostics, rejected admission/observation/finish transactions, invalid UTF-8, missing jobs, cross-job cursors, read failure, unavailable-state handling, late writes, pruning and orderly reopen.
   Independently remove uniqueness, admission, transaction, phase, failure-deduplication, pagination and retention guards and require the appropriate assertions to fail.
   Existing Copy remains unfixed by this foundation alone; keep the archived current-master reproduction as the migration oracle.
3. Specify and review the concrete Copy adapter and UI migration against these storage operations before changing its execution hooks.
   Preserve the actual 1/201/401-name fixtures, add SQL refusal before dispatch and after a witnessed effect, interruption with missing outcomes, comparison-only verification identities, refusal separation, complete page access, restart projection and unresolved-rerun refusal.
   Verify exact allowed effects and unchanged owned collateral and shared aliases, source audit authority, destination recovery and all retained identities.
4. Reproduce and migrate local upload and download separately, retaining their established local-placement and completion contracts and bringing missing filesystem or authority prerequisites alongside their dependent effects.
5. Extend this logical record foundation with each concrete per-effect, destructive, bulk-response, spill, crash and lost-history caller; do not infer any of those guarantees from this initial implementation.

Every implementation commit requires independent local review and its relevant verification, then all five latest PR checks on current master, protected merge and all five resulting master checks.
Mirror starts, changed existing Copy overwrites and automatic recovery reclamation remain refused.
No accepted feature or platform scope is removed, and the valuable-data release hold remains in force.
