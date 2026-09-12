# Deletion sweep authority after a partial bulk reply

## Scope and oracle

This experiment settles the sweep re-attempt hazard that the [bulk-delete identity ledger](2026-09-11-bulk-delete-identities.md) recorded and deliberately left open: a prefix deletion sweeps in passes, re-lists after each pass that deleted something, and re-submits whatever is still listed, so a name whose first request had an uncertain effect is requested again without any new authority.
The [deletion accounting contract](../adr/0036-delete-identity-accounting.md) made the accounting index private state and stopped admission after unindexable outcomes, and explicitly left this re-attempt unresolved.

The oracle is [K-DEL-002 and K-CON-001](../REQUIREMENTS.md) and DESIGN §32 with [ADR 0020](../adr/0020-recovery-containment.md): a recursive delete is bounded and retains exact outcomes, a listing is advisory and an object read is the only authoritative observation, a fresh HEAD is an observation and not a compare-and-mutate boundary, and uncertainty is treated conservatively.
The DS-08 and SEC-08 rows require complete per-effect outcomes and fresh authority across replacement races.
Exact typed confirmation, the optional preview count, the prefix scope, bounded batches, the checked reply validation, the unresolved and reported-failure facets and the partial outcome are preserved facts.

The first part of this record establishes the defect on both owned providers through the public service and proposes a reviewable contract; the second part, from the section "Correction delivered" onward, records the correction and its evidence.
It does not claim manual-deletion safety in general.

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

## Contract

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

## Correction delivered

The correction is one commit on top of the plan, `send one destructive request per deletion name`, under [ADR 0039](../adr/0039-delete-request-authority.md).
The private accounting index gains an issued kind beside the reported-failure and unresolved facets; `issue` in `internal/deletion/accounting.go` records every name of a batch as issued before the request is sent and returns only the names not issued before, counting and sampling the rest as not requested again, and latches the accounting error and stops admission if the record cannot be written.
The bulk branch of `deletePass` sends only the returned names and skips the request when none remain; the parallel branch issues each name before its single request.
The pass loop is unchanged: a pass that sends nothing leaves the deleted count where it was, so the sweep ends.
The delete service reports the not-requested-again count and its first name beside the unresolved facets, and the job ends partial.

## After evidence

The archived probe became the maintained regression `TestDeleteSweepAuthorityMock` and `TestDeleteSweepAuthorityRealSwift`, five cases on each provider.

| Case | Before | After |
| --- | --- | --- |
| partial_reply_replacement | two bulk requests, the blamed name issued twice, the replacement deleted | one bulk request, the blamed name issued once, the replacement survives with its exact bytes, the job partial with the unresolved facet and one name not requested again |
| partial_reply_replacement_and_late_name | not probed | two bulk requests: the second carries exactly the late name, which is deleted; the replacement at the blamed name survives; this is the open membership hazard, witnessed |
| partial_reply_no_replacement | one bulk request | one bulk request, the job partial with the unresolved facet and nothing reported as not requested again |
| issued_record_refused | not probed | no bulk request, both planned objects survive, the job stops with incomplete accounting |
| healthy_control | one bulk request, complete | one bulk request, complete |

Unit tests in `internal/deletion/issued_record_test.go` cover the issued record's admission of each name once, the failure latch that stops admission before any request, the parallel branch requesting each listed name once across passes, and the distinct counting of repeated facets at the accounting boundary; the cross-pass ledger test now proves its counts with one request instead of skipping when the sweep does not repeat.
The index-boundary helper of the accounting behaviour tests counts and refuses outcome facets only, because the issued records are a different fact and refusing them would stop the next request instead of leaving an already-sent request unindexed.
Every delete-related service family passed on both providers after the change.

## Deliberately broken controls

Each guard was removed on its own in a private copy and the unit tests and the mock regression rerun; every control failed only its own cases and compiled cleanly.

| Control | Mutation | Unit tests failing | Service cases failing on the mock |
| --- | --- | --- | --- |
| no issued filter | every name in a batch is sent whether or not this job already requested it | TestBulkLedgerCountsDistinctIdentitiesAcrossPasses, TestIssuedRecordAdmitsEachNameOnce, TestParallelSweepIssuesEachNameOnce | partial_reply_replacement, partial_reply_replacement_and_late_name |
| issue failure ignored | an unwritable issued record still sends the request instead of stopping admission | TestIssuedRecordFailureStopsBeforeRequest | issued_record_refused |

## Verification

Focused producers on the committed source, all under the race detector: the deletion package's tests (46 top-level tests passing), and the service regression on the in-process mock (5 leaves) and on the real two-account Swift fixture (5 leaves).
Every delete-related service family was rerun on both providers before the commit.
The full race suite ran once on the committed source with the pinned engine directory and the real Swift fixture configured, exit 0: 4083 distinct names, 3786 leaves of which 3785 passing, 0 failing and 1 skipped, 4082 passing events including parents, 22 packages passing.
A leaf is a recorded name that is not a slash-separated prefix of another recorded name in the same package.
The change carries gofmt, vet on Linux and Windows, a macOS build, and no module change.
The [manifest](2026-09-12-delete-sweep-authority-probes.json) records every producer command, exit, count and digest and the broken-control outcomes.

## Proposed shared-document text

For an ADR under the next free number: the contract above, with the accounting index as the issued record and the two open hazards named.
For the design sequence, after the reserved-name routing paragraph: the next bounded deletion correction gives each deletion job at most one destructive request per name and stops a sweep that lists only names it has already requested, so a valid partial reply can no longer lead to a second request that destroys a replacement; the listing-to-request race and later-listed names remain open under DS-08 and SEC-08.
For the register, the DS-08 and SEC-08 rows: the repeated-request hazard after a partial reply is witnessed on both providers and closed by the per-job issued record; the wider fresh-authority and replacement-race gates remain open.

## Remaining limits

Linux amd64 with owned fixtures is the only observed environment.
DS-08, SEC-08, P12, P13, P28 and the valuable-data hold remain open.

## Protected delivery

The delivered range is the nine logical commits between master 5a374826ddc2f69a and the reviewed head 121ce214abfe3421, cherry-picked patch for patch from three independently approved branches: this slice's three commits (the plan, the issued record that limits each name to one destructive request per job, and this record's after evidence with ADR 0039), together with the three commits of the name length rule and the three documentation commits of the release evidence records.
Independent review approved each slice on its own base and then the combined range on its final base, with every patch marked identical by range-diff and no shared document in the range.

Its check run 34676247429 completed at the first attempt with all five required jobs successful: the frontend job in one minute and thirteen seconds, the headless smoke job in one minute and fifty-three seconds, the race-detector Go job in nine minutes and nineteen seconds, the containerized Swift integration job in twenty-four minutes and thirty-eight seconds, and the aggregate job.
The run retained the two coverage badges, the 481,236-byte scenario coverage artifact, the 6,114,561-byte smoke artifact and a 6,936-byte Swift diagnostics artifact.
The retained badges report combined Go statement coverage of 88.1 per cent and frontend coverage of 91.07 per cent.
On the same head the full race suite with the real Swift fixture reconciled to 3,820 leaves with 3,819 passing, none failing and the single expected matrix skip; no frontend file is in the range.

The pull request then left draft state after thirty-one premerge guards passed and was merged by rebase pinned to the reviewed head object at 06:07:38 UTC on 12 September 2026.
Master became ca95bad8a6226746, whose tree 70f19c19a077b799 is identical to the reviewed head's tree, and each of the nine commits kept its patch identity, message and author identity.
The resulting master check run 34677323044 concluded success at the first attempt with the same five required jobs.
The pipeline exercised Linux runners only, so native acceptance on the other platforms remains unproven, and the shared design and register text for this slice is in the same integration range as this record.
