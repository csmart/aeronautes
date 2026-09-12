# Deletion sweep authority after a partial bulk reply

## Scope and oracle

This experiment settles the sweep re-attempt hazard that the [bulk-delete identity ledger](2026-09-11-bulk-delete-identities.md) recorded and deliberately left open: a prefix deletion sweeps in passes, re-lists after each pass that deleted something, and re-submits whatever is still listed, so a name whose first request had an uncertain effect is requested again without any new authority.
The [deletion accounting contract](../adr/0036-delete-identity-accounting.md) made the accounting index private state and stopped admission after unindexable outcomes, and explicitly left this re-attempt unresolved.

The oracle is [K-DEL-002 and K-CON-001](../REQUIREMENTS.md) and DESIGN §32 with [ADR 0020](../adr/0020-recovery-containment.md): a recursive delete is bounded and retains exact outcomes, a listing is advisory and an object read is the only authoritative observation, a fresh HEAD is an observation and not a compare-and-mutate boundary, and uncertainty is treated conservatively.
The DS-08 and SEC-08 rows require complete per-effect outcomes and fresh authority across replacement races.
Exact typed confirmation, the optional preview count, the prefix scope, bounded batches, the checked reply validation, the unresolved and reported-failure facets and the partial outcome are preserved facts.

This record establishes the defect on both owned providers through the public service and proposes a reviewable contract.
It changes no production code and does not claim manual-deletion safety in general.

## Source finding

`DeletePrefix` in `internal/deletion/deletion.go` runs up to three passes; each pass lists the prefix and deletes what it lists, and another pass starts whenever the previous pass increased the deleted count, regardless of unresolved names.
`recordBatchOutcome` in `internal/deletion/accounting.go` credits a validated partial reply's aggregate, records the blamed name's reported-failure and unresolved facets, and returns no error, so the sweep continues.
`deletePass` admits every listed name through `accounting.admit`, which checks only that accounting is healthy and the context is live; nothing checks whether this job already requested the name.
The private index deduplicates observed facets; it stores no record of names issued, so the same name can be issued in every pass.
The parallel single-request branch shares the pass loop and the same absence of an issued record.

## Deterministic boundary

The witness needs no timing: a front in front of the store forwards every request, and on the first bulk request it lets the store apply the deletions exactly as sent, installs a replacement object at the blamed name through a separate fixture client, and only then answers the sweep with a syntactically valid partial reply that credits one deletion and blames that name with a 500 row spelled exactly as the request line.
The sweep's second pass then lists the replacement under the blamed name and issues a second bulk request, which the front forwards unchanged.

## Before evidence on the service

The [archived probe](probes/2026-09-12-delete-sweep-authority_test.go.txt) drives the real service through the typed-confirmation delete request, the queued job and both owned remote namespaces, with the private accounting index under an owned cache directory, on the in-process mock and on the owned two-account real Swift fixture.
The [manifest](2026-09-12-delete-sweep-authority-probes.json) records the archive hash, the producers, their exits, every observation and the log digests.

| Case | Store after the first request | Sweep behaviour | Job |
| --- | --- | --- | --- |
| partial_reply_replacement | both planned objects deleted, then a 43-byte replacement written at the blamed name by another writer | two bulk requests; the blamed name issued twice; the replacement deleted by the second request; nothing outside the prefix, the other container or the other account touched | partial, one reported failure for the blamed name |
| partial_reply_no_replacement | both planned objects deleted | one bulk request; the second pass listed nothing and issued nothing | partial, one reported failure for the blamed name |
| healthy_control | both planned objects deleted | one bulk request | complete |

Both providers agree on every case.
The second request exists only because a new object stood at the blamed name; without a replacement the second pass lists nothing and the sweep ends.

## What the evidence establishes

After a valid partial reply the sweep requests the blamed name again, and the only thing that can be listed under that name is something the job never planned: a replacement written after the first request.
The repeated request therefore destroys exactly the object the operator did not consent to delete, and the job reports the same partial outcome it would have reported without the loss.
The hazard needs no adversary: any writer that reuses the name between the first request and the next listing is enough.

## What the evidence does not establish

A replacement written between the listing and the first request is not covered here; that race is the fresh-authority limit ADR 0020 settles and this slice does not change it.
A name first listed in a later pass, because it was created after the first listing or paged in late, is not covered; the sweep still deletes it because the prefix scope is the operator's consent, and this record says so rather than freezing the membership.
The wire helper's one-time re-authentication resend repeats the same request after a 401 and is a transport retry of one request, not a new pass; it is out of scope here.

## Proposed contract

1. One destructive request per name per job.
   Before a batch or a single request is sent, every name in it is recorded as issued in the private accounting index, in the same transaction discipline the facets use; a name already recorded as issued is never sent again by the same job, in any pass, on either the bulk or the parallel branch.
   The record is written before the request, so a request whose reply is lost still counts as issued.
2. The sweep stops when a pass issues nothing new.
   A pass that lists only names already issued ends the sweep without another request; a pass that lists nothing ends it as today.
   Names still listed after their only request are counted and named in the job as not requested again, with their unresolved or reported-failure facets, and the job ends partial exactly as an unresolved outcome does today.
3. Admission and accounting failures keep their delivered behaviour.
   If the issued record cannot be written, the request is not sent and the job stops as an unindexable admission does under the accounting contract.
4. Reporting.
   The terminal message and the audit summary state how many listed names were not requested again because this job had already requested them; the existing facets are unchanged.
5. What remains open and is recorded, not solved.
   The listing-to-first-request race and names first listed in a later pass stay as stated above; the operator re-runs the deletion deliberately to obtain fresh authority for whatever is still listed.

## Required proof

The archived probe becomes the maintained regression on both providers: the replacement survives with its exact bytes, the blamed name is issued exactly once, the job ends partial with the unresolved facet and the not-requested-again count, and the healthy control still completes with one request.
A second maintained case seeds a fresh object under the prefix between the first request and the second listing, and records that the sweep deletes it, so the open membership hazard is witnessed rather than implied.
Two deliberately broken controls, one removing the issued check and one removing the stop, must each fail only their own case.
The existing accounting, cancellation, effect-substitution and shared-segment tests must stay green; the ledger test that counted identities across passes must keep its duplicate-facet proof at the accounting boundary without depending on a repeated destructive request.
The full race suite runs with inventory reconciliation.

## Proposed shared-document text

For an ADR under the next free number: the contract above, with the accounting index as the issued record and the two open hazards named.
For the design sequence, after the reserved-name routing paragraph: the next bounded deletion correction gives each deletion job at most one destructive request per name and stops a sweep that lists only names it has already requested, so a valid partial reply can no longer lead to a second request that destroys a replacement; the listing-to-request race and later-listed names remain open under DS-08 and SEC-08.
For the register, the DS-08 and SEC-08 rows: the repeated-request hazard after a partial reply is witnessed on both providers and closed by the per-job issued record; the wider fresh-authority and replacement-race gates remain open.

## Remaining limits

This record proves the defect and proposes a contract; it delivers no correction.
Linux amd64 with owned fixtures is the only observed environment.
DS-08, SEC-08, P12, P13, P28 and the valuable-data hold remain open.
