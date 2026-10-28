# ADR 0095 required-proof map

[ADR 0095](../adr/0095-prefix-deletion-by-the-per-name-protocol.md) lists the proof its implementation must carry before it is accepted.
This record maps each required case to the executable result that establishes it, so a reader can run the named test and watch the behaviour rather than take the claim on trust.
The tests run on the in-process mock; the owned-Swift producers in `internal/systemtest` run the same deletion journeys against a real cluster.

## Reuse of the verified per-name protocol

The bulk sweep and the segment job delete by the same per-name protocol the selected set uses, over a listing pass or a reviewed graph rather than a chosen set, so the rules ADR 0085 and ADR 0093 settled are not re-decided.
`TestPrefixSweepDeletesEveryMemberExactly` and `TestSelectedDeleteDeletesEveryMemberExactly` exercise the shared member path.
`TestSegmentJobDeletesStaticManifestAndSegments` and `TestSegmentJobDeletesDynamicManifestAndNamespace` exercise it under the segment job, each asserting no segment-expanding `multipart-manifest=delete` request is ever sent.

## Scale and retention: ledger page, drawer page, measured write cost, real restart

`TestPrefixSweepScaleBoundedAndDurable` sweeps 520 members, more than the 500-row admission chunk and the 200-row drawer page.
Its resident membership stays bounded to a listing page, because the sweep admits and settles a page at a time rather than holding the whole scope.
It measures the ledger's write cost as committed write transactions — the work actually done, read from `db.WritesCommitted`, not inferred from the rows that survive — and asserts the cost is linear: three a member, being the two ledger writes ADR 0095 adds (the issued mark and the outcome) plus the one per-member progress counter every job already writes, on top of the admitted pages and the job's lifecycle.
It reads every member's record in the drawer's pages, then closes the process's database, reopens it on its file, and reads every member's deleted record again from durable storage in the drawer's pages, with the job's counts exact — a real restart, not a second handle over the still-open original.

## Adjacency: nothing of a member's own between its HEAD and its DELETE

`TestDeletePlainObjectIssuesNoAuthenticationBetweenComparisonAndDelete` and `TestDeletePlainObjectRefusedTokenOnTheDeleteIsARefusal` establish the per-member guarantee at the protocol: no request of the member's own and no authentication runs between its comparison HEAD and its DELETE, and a token refused on the DELETE is a refusal, never a cue to authenticate and delete again.
`TestPrefixSweepHoldsPerMemberAdjacencyUnderWorkerInterleaving` establishes it under the bounded worker pool: one worker is deliberately held between its HEAD and its DELETE while another completes its member, and the recorded order shows the held member's DELETE still immediately after that member's own HEAD, with only the other member's requests in between.

## Preview and dialog

`TestDeletePreviewCountsAndDisclosures` states the counts, the `2N + Z` request cost and the per-name strategy before the job.
The frontend `previewBinding.test.ts` shows the cost sentence and the later-baseline rows, the typed-container confirmation, and that Start waits for the versioning facts to settle before it enables or accepts the destructive request.
The segment job's reviewed membership is shown by `TestSegmentJobDeletesStaticManifestAndSegments`, and its namespace boundary by `TestSegmentJobRefusesAnOutsideNamespaceName`.

## Segment job: the two lost-record boundaries

Boundary one — the manifest's own outcome lost after its DELETE — is `TestSegmentJobLeavesTheJobOpenWhenTheManifestRecordIsLost`: only the issued mark survives, no segment is attempted, the job is unavailable, and the manifest settles unresolved on reopen with no deleted outcome anywhere.
Boundary two — a later write failing after the manifest's deleted outcome committed — is `TestSegmentJobDisclosesTheDeletedManifestWhenALaterWriteFails`: the manifest's deleted record stands, the job is unavailable with the deleted manifest disclosed, and the segment is unresolved.

## Admission before effect

`TestSegmentJobRecordsTheReviewedAuthorityBeforeAnyEffect` refuses the admitting write that carries the reviewed identity and scope and shows nothing is deleted, then shows the ordinary path records that authority with the job's own row.
`TestDeleteReviewedManifestRefusesAnIncompleteGraphResponse` shows the live graph that authorises a DELETE is read strictly: an HTTP 206, a Content-Range 200, a truncated body and a valid entry list followed by other bytes are each refused with nothing deleted.

## Retained-mutant obligations, each failing a case above

| Obligation (ADR 0095 decision list) | Executable result |
| --- | --- |
| a bulk request used | `TestPrefixSweepDeletesEveryMemberExactly`, `TestSegmentJobDeletesStaticManifestAndSegments` (no `multipart-manifest=delete`) |
| the listed row ignored for a HEAD baseline on an ordinary row | `TestSelectedDeleteDeletesEveryMemberExactly`, `TestSelectedDeleteAdoptsALaterBaselineForARowThatCannotSay` |
| the replacement adopted as the baseline when a comparison finds a manifest | `TestPrefixSweepRefusesAMemberChangedSinceListed`, `TestSelectedDeleteTreatsALinkAtAnOrdinaryMemberAsChanged` |
| a DELETE repeated after a lost reply | `TestPrefixSweepUnverifiedWhenReplyLost`, `TestSelectedDeleteLostReplyIsUnverifiedAndPartial` |
| cancellation honoured inside a member | `TestPrefixSweepCancelDuringSweep`, `TestSelectedDeleteCancelsBetweenMembers` |
| a refused name requested again in a later pass | `TestPrefixSweepLaterPassRequestsOnlyNewNames` |
| a new member issued after a lost record | `TestPrefixSweepLeavesTheJobOpenWhenARecordIsLost`, `TestSelectedDeleteLeavesTheJobOpenWhenARecordIsLost` |
| the container deleted after a partial sweep | `TestDeleteContainerEmptyCancelledIsCancelled` |
| a non-empty postcondition with no request reported complete | `TestPrefixSweepFailsWhenScopeNonEmptyWithNoRequest` |
| a conflict recorded without its identities | `TestSelectedDeleteChangedMemberKeepsBothIdentities` |
| the preview cost counting N alone | `TestDeletePreviewCountsAndDisclosures` |
| the manifest deleted by a plain DELETE without the comparison | `TestDeleteReviewedManifestAppliesWhenTheGraphMatches`, `TestDeleteReviewedManifestRefusesAnEqualTagGraphSwap` |
| the graph resolved again at execution instead of the reviewed membership | `TestSegmentJobRefusesAnEqualTagGraphSwap`, `TestDeleteReviewedManifestRefusesAMovedDLONamespace` |
| the segment phase started after an unverified manifest | `TestSegmentJobLeavesSegmentsOnUnverifiedManifest` |
| a deleted outcome fabricated from the manifest's issued mark | `TestSegmentJobLeavesTheJobOpenWhenTheManifestRecordIsLost`, `TestSegmentJobDisclosesTheDeletedManifestWhenALaterWriteFails` |

The executable mutant packet under `.aeronautes-work/20261027-release-plan/controls/adr0095-fixes-<head>` injects a representative subset of these faults into the source and records, for each, the named test that then fails.
It is the retained evidence that these tests catch the fault rather than only asserting the fixed behaviour; its `summary.json` names each control, its verdict and the catching test, and `SHA256SUMS` fixes the source and reports it was built from.
