# ADR 0039 — one destructive request per deletion name

**Status:** Accepted after independent local review of the contract; the implementation's review and its protected delivery are recorded in the experiment.
**Scope:** A deletion job sends at most one destructive request for any object name, records the name as issued before the request leaves, and stops a sweep that lists only names it has already requested; the listing-to-request race and names first listed in a later pass stay open.
The before evidence was reproduced through the public service on both owned providers before this contract was proposed, and the correction's evidence is recorded in the same experiment.

## Problem and oracle

A prefix deletion sweeps in passes: it lists, deletes what it lists, and lists again while a pass still deletes something.
The [deletion accounting contract](0036-delete-identity-accounting.md) records a blamed name's reported-failure and unresolved facets and credits only a validated aggregate, but the sweep then re-submits whatever is still listed, so a name whose first request had an uncertain effect is requested again without any new authority.
The [experiment](../experiments/2026-09-12-delete-sweep-authority.md) shows on both providers that after a valid partial reply the second pass requests the blamed name again and, when a replacement object stands there, deletes that replacement while the job reports the same partial outcome; without a replacement the second pass lists nothing and sends nothing.

The oracle is [K-DEL-002 and K-CON-001](../REQUIREMENTS.md), DESIGN §32 and [ADR 0020](0020-recovery-containment.md): a recursive delete is bounded with exact outcomes, a listing is advisory and a fresh HEAD is not compare-and-mutate authority, and uncertainty is treated conservatively.
Typed confirmation, the optional preview count, the prefix scope, bounded batches, checked reply validation, the reported-failure and unresolved facets, the private accounting index and the partial outcome are preserved.

## Decision

1. One destructive request per name per job.
   Before a batch or a single request is sent, every name in it is recorded as issued in the job's private accounting index, in the same transaction discipline the outcome facets use; a name recorded as issued is never sent again by the same job, in any pass, on the bulk branch or the parallel branch.
   The record is written before the request leaves, so a request whose reply is lost still counts as issued.
2. A sweep stops when a pass issues nothing new.
   A pass that lists only names already issued ends the sweep without a request, because the deleted count does not advance; a pass that lists nothing ends it as before.
   Names still listed after this job's only request for them are counted and sampled as not requested again, reported beside the unresolved facets, and the job ends partial exactly as an unresolved outcome does.
3. Admission and accounting failures keep their behaviour.
   If the issued record cannot be written, the request is not sent and admission stops as an unindexable outcome stops it under the accounting contract.
4. What stays open and is recorded, not solved.
   A replacement written between the listing and the first request is the fresh-authority limit ADR 0020 settles; a name first listed in a later pass is still requested because the prefix scope is the operator's consent; the operator re-runs a deletion deliberately to obtain fresh authority for whatever is still listed.

## Consequences

A valid partial reply can no longer lead to a second request that destroys a replacement.
A deletion job's index now holds one issued row per name it requested, bounded by disk rather than memory, and is discarded with the job.
A sweep that lists only names it has already requested ends after that listing instead of after a bounded number of passes, and the job says which names it did not request again.
The cross-pass ledger test proves distinct counting at the accounting boundary rather than through a repeated destructive request.
DS-08, SEC-08, P12, P13, P28 and the valuable-data hold stay open.
