# ADR 0095 required-proof map

[ADR 0095](../adr/0095-prefix-deletion-by-the-per-name-protocol.md) lists the proof its implementation must carry before it is accepted.
This record maps each required case to the executable result that establishes it, so a reader can run the named test and watch the behaviour rather than take the claim on trust.

The evidence is of four distinct kinds, and this map keeps them distinct rather than treating one as another:

- **In-process mock tests** in `internal/app` and `internal/swiftcontrol`, which drive the real services and protocol against the in-process Swift mock.
- **A real process-restart test**, `internal/app` `TestSegmentDeletionSurvivesAProcessRestart`, which runs the real job in a child process, kills that process at a chosen boundary, and recovers in a fresh process.
- **Owned-Swift producers** in `internal/systemtest`, which run the deletion journeys against a real cluster; these are unchanged since db:925 and are cited here as prior evidence, not re-run for this record.
- **The executable mutant packet** under `.aeronautes-work/20261027-release-plan/controls/adr0095-fixes-<head>`, which injects a **representative subset** of the retained-mutant faults into the source and records, for each, the named test that then fails. It is not the complete ADR retained-mutant list; the table below names the test for every obligation, and the packet seals the subset it actually ran.

## Reuse of the verified per-name protocol

The bulk sweep and the segment job delete by the same per-name protocol the selected set uses, over a listing pass or a reviewed graph rather than a chosen set, so the rules ADR 0085 and ADR 0093 settled are not re-decided.
`TestPrefixSweepDeletesEveryMemberExactly` and `TestSelectedDeleteDeletesEveryMemberExactly` exercise the shared member path.
`TestSegmentJobDeletesStaticManifestAndSegments` and `TestSegmentJobDeletesDynamicManifestAndNamespace` exercise it under the segment job, each asserting no segment-expanding `multipart-manifest=delete` request is ever sent.

## Scale and retention: ledger page, drawer page, measured write cost

`TestPrefixSweepScaleBoundedAndDurable` sweeps 520 members, more than the 500-row admission chunk and the 200-row drawer page.
Its resident membership stays bounded to a listing page, because the sweep admits and settles a page at a time rather than holding the whole scope.
It measures the ledger's write cost as committed write transactions, the work actually done, read from `db.WritesCommitted`, not inferred from the rows that survive; it is linear at three a member, being the two ledger writes ADR 0095 adds (the issued mark and the outcome) plus the one per-member progress counter every job writes, with the admitted pages and the job's lifecycle on top.
It reads every member's record in the drawer's pages while the process is alive, then closes the database and reopens it on its file and reads them again: an in-process durability readback, not a process restart, which the next case supplies.

## A real process restart, including the boundary immediately after the manifest deletion

`TestSegmentDeletionSurvivesAProcessRestart` runs the real segment-delete job in a child process and kills that process hard, with no cleanup and no clean database close, at two boundaries; a genuinely fresh process then reopens the database on its file, runs startup recovery, and reads back the members.
A crash while the manifest DELETE is still outstanding leaves the manifest issued with no outcome, recovered as unresolved and never fabricated into a deleted outcome.
A crash at the first segment, immediately after the manifest's deletion has been recorded, leaves that deleted record standing, the interrupted segment unresolved, and the untouched segment not attempted, with exact totals.
Together these establish the issued-only versus durably-deleted manifest distinction across an actual process boundary, which the in-process close/reopen above does not.

## Adjacency: nothing of a member's own between its HEAD and its DELETE

`TestDeletePlainObjectIssuesNoAuthenticationBetweenComparisonAndDelete` and `TestDeletePlainObjectRefusedTokenOnTheDeleteIsARefusal` establish the per-member guarantee at the protocol: no request of the member's own and no authentication runs between its comparison HEAD and its DELETE, and a token refused on the DELETE is a refusal, never a cue to authenticate and delete again.
`TestPrefixSweepHoldsPerMemberAdjacencyUnderWorkerInterleaving` establishes it under the bounded worker pool: one worker is held between its HEAD and its DELETE while another worker completes its whole member on the wire — its comparison HEAD, its DELETE and its verifying read — and the recorded order, `HEAD a, HEAD b, DELETE b, HEAD b, DELETE a, HEAD a`, shows the held member's DELETE still immediately after that member's own HEAD, with only the other member's complete member in between.

## Preview and dialog

`TestDeletePreviewCountsAndDisclosures` states the counts, the `2N + Z` request cost and the per-name strategy before the job.
The frontend `previewBinding.test.ts` shows the cost sentence and the later-baseline rows, the typed-container confirmation, and that Start waits for the versioning facts to settle before it enables or accepts the destructive request.
The segment job's reviewed membership is shown by `TestSegmentJobDeletesStaticManifestAndSegments`, and its namespace boundary by `TestSegmentJobRefusesAnOutsideNamespaceName`.

## Segment job: admission, settlement and the two lost-record boundaries

`TestSegmentJobSettlesAdmittedSegmentsWhenTheManifestIsNotDeleted` shows that when the manifest is refused at execution, the segments admitted before any effect are each settled not attempted, leaving no phantom unresolved effect the terminal job can no longer settle.
Boundary one — the manifest's own outcome lost after its DELETE — is `TestSegmentJobLeavesTheJobOpenWhenTheManifestRecordIsLost`: only the issued mark survives, no segment is attempted, the job is unavailable, and the manifest settles unresolved on reopen with no deleted outcome anywhere.
Boundary two — a later write failing after the manifest's deleted outcome committed — is `TestSegmentJobDisclosesTheDeletedManifestWhenALaterWriteFails`: the manifest's deleted record stands, the job is unavailable with the deleted manifest disclosed, and the segment is unresolved.

## Admission before effect

`TestSegmentJobRecordsTheReviewedAuthorityBeforeAnyEffect` refuses the admitting write that carries the reviewed identity and scope and shows nothing is deleted, then shows the ordinary path records that authority with the job's own row.
`TestDeleteReviewedManifestRefusesAnIncompleteGraphResponse` shows the live graph that authorises a DELETE is read strictly: an HTTP 206, a Content-Range 200, a truncated body and a valid entry list followed by other bytes are each refused with nothing deleted.

## Retained-mutant obligations, each failing a case above

The packet seals the controls marked (packet); the rest are named by their failing test and not yet mechanically mutated.

| Obligation (ADR 0095 decision list) | Executable result |
| --- | --- |
| a bulk request used | `TestPrefixSweepDeletesEveryMemberExactly`, `TestSegmentJobDeletesStaticManifestAndSegments` (no `multipart-manifest=delete`) |
| the listed row ignored for a HEAD baseline on an ordinary row | `TestSelectedDeleteDeletesEveryMemberExactly`, `TestSelectedDeleteAdoptsALaterBaselineForARowThatCannotSay` |
| the replacement adopted as the baseline when a comparison finds a manifest | `TestPrefixSweepRefusesAMemberChangedSinceListed`, `TestSelectedDeleteTreatsALinkAtAnOrdinaryMemberAsChanged` |
| a DELETE repeated after a lost reply | `TestPrefixSweepUnverifiedWhenReplyLost`, `TestSelectedDeleteLostReplyIsUnverifiedAndPartial` |
| cancellation honoured inside a member | `TestPrefixSweepCancelDuringSweep`, `TestSelectedDeleteCancelsBetweenMembers` |
| the issued mark written after the HEAD | `TestSegmentDeletionSurvivesAProcessRestart` (the interrupted member keeps an issued mark, so it recovers unresolved rather than not attempted) |
| a refused name requested again in a later pass | `TestPrefixSweepLaterPassRequestsOnlyNewNames` |
| a new member issued after a lost record | `TestPrefixSweepLeavesTheJobOpenWhenARecordIsLost`, `TestSelectedDeleteLeavesTheJobOpenWhenARecordIsLost` |
| the container deleted after a partial sweep | `TestDeleteContainerSurvivesAPartialSweep` (packet) |
| the container reported complete-or-partial deletion on a zero-effect cancel | `TestDeleteContainerEmptyCancelledIsCancelled` (packet) |
| a non-empty postcondition with no request reported complete | `TestPrefixSweepFailsWhenScopeNonEmptyWithNoRequest` |
| the empty-mode configuration change omitted during a partial | `TestEmptySweepReportsAConfigChangeAfterAPartial` (packet) |
| a conflict recorded without its identities | `TestSelectedDeleteChangedMemberKeepsBothIdentities`, `TestSegmentJobRefusesAnEqualTagGraphSwap` (packet, manifest baseline) |
| a member keyed by the bare name, colliding across containers | `TestSegmentJobKeysSegmentsByContainer` (packet) |
| the preview cost counting N alone | `TestDeletePreviewCountsAndDisclosures` (packet) |
| the manifest deleted by a plain DELETE without the comparison | `TestDeleteReviewedManifestAppliesWhenTheGraphMatches`, `TestDeleteReviewedManifestRefusesAnEqualTagGraphSwap` (packet) |
| the graph resolved again at execution instead of the reviewed membership | `TestSegmentJobRefusesAnEqualTagGraphSwap`, `TestDeleteReviewedManifestRefusesAMovedDLONamespace` |
| the reviewed graph read permissively or on a partial response | `TestDeleteReviewedManifestRefusesAnIncompleteGraphResponse` (packet, two controls) |
| the reviewed authority recorded by a later write, not the admitting row | `TestSegmentJobRecordsTheReviewedAuthorityBeforeAnyEffect` (packet) |
| the admitted segments left unsettled on a refused manifest | `TestSegmentJobSettlesAdmittedSegmentsWhenTheManifestIsNotDeleted` (packet) |
| the segment phase started after an unverified manifest | `TestSegmentJobLeavesSegmentsOnUnverifiedManifest` |
| a deleted outcome fabricated from the manifest's issued mark | `TestSegmentJobLeavesTheJobOpenWhenTheManifestRecordIsLost`, `TestSegmentDeletionSurvivesAProcessRestart` |

## Bounds and outstanding receipts

The mutant packet is a representative subset, currently eleven controls, each sealed with its mutation diff, the named failing test, the clean and mutant output, and a source hash in `SHA256SUMS`; it is not the exhaustive retained-mutant list, and the table above is the authoritative map of every obligation to its test.
The owned-Swift producers establish the mock-and-real-cluster agreement for the ordinary deletion journeys and were last run in the db:925 round; this record does not re-run them and does not claim a fresh hosted run.
The process-restart evidence is the child-process test named above; the scale test's close/reopen is an in-process durability readback and is described as such, not as a process restart.
