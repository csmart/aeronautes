# ADR 0095 required-proof map

[ADR 0095](../adr/0095-prefix-deletion-by-the-per-name-protocol.md) lists the proof its implementation must carry before it is accepted.
This record maps each required case to the executable result that establishes it and to the control that proves that result actually catches the fault, so a reader can run the named test, run the named mutation, and see the test fail on it.
A passing test name alone is not treated as discharging an obligation.

The evidence is of four distinct kinds, kept distinct here:

- **In-process mock tests** in `internal/app` and `internal/swiftcontrol`, driving the real services and protocol against the in-process Swift mock.
- **Real process-restart tests**, which run the real job or sweep, cross an actual process boundary, and recover or read back in a fresh process: `TestSegmentDeletionSurvivesAProcessRestart` and the scale test's child readback.
- **Owned-Swift producers** in `internal/systemtest`, run against a real cluster; these are unchanged since db:925 and are cited as prior evidence, not re-run for this record.
- **Mutant control packets**: this slice's packet under `controls/adr0095-fixes-<head>`, and the shared-protocol packets `controls/selected-4e63b6a0` and `controls/marker-manifest-b1151283`. The sweep and the segment job call `Session.DeleteMember` and the `deleteShown` protocol unchanged from the selected set and the marker/manifest work, so the controls that mutate that exact code in those packets establish the shared-protocol obligations for this path too. This slice's packet adds only the bulk and segment-specific controls.

## Reuse of the verified per-name protocol

The bulk sweep and the segment job delete by the same per-name protocol the selected set uses.
`TestPrefixSweepDeletesEveryMemberExactly` and `TestSegmentJobDeletesStaticManifestAndSegments` exercise it, each asserting no segment-expanding `multipart-manifest=delete` request is sent; control `selected-4e63b6a0` S1 mutates the shared member DELETE into the expanding form.

## Scale and retention: ledger page, drawer page, measured write cost, process restart

`TestPrefixSweepScaleBoundedAndDurable` sweeps 520 members, more than the 500-row admission chunk and the 200-row drawer page, with resident membership bounded to a listing page.
It measures the ledger's write cost as committed write transactions through `db.WritesCommitted`, the work done rather than the rows retained: three a member, the two ledger writes ADR 0095 adds (the issued mark and the outcome) plus the one per-member progress counter every job writes, asserted linear.
It then closes its database and a genuinely fresh child process opens the file and reads every one of the 520 members back in the drawer's pages, with exact deleted and admitted counts: a real process boundary, not an in-process handle reopen.

## A real process restart of the segment job, including the boundary after the manifest deletion

`TestSegmentDeletionSurvivesAProcessRestart` runs the real segment-delete job in a child process and kills it hard at two boundaries; a fresh process reopens the database and runs startup recovery.
A crash while the manifest DELETE is outstanding recovers the manifest as unresolved, never fabricated deleted; a crash at the first segment, just after the manifest's deletion is recorded, leaves that deleted record standing, the interrupted segment unresolved and the untouched one not attempted.

## Adjacency: nothing of a member's own between its HEAD and its DELETE

`TestDeletePlainObjectIssuesNoAuthenticationBetweenComparisonAndDelete` establishes the per-member protocol guarantee; control `selected-4e63b6a0` S5 mutates a cancellation to reach inside a member between its HEAD and DELETE.
`TestPrefixSweepHoldsPerMemberAdjacencyUnderWorkerInterleaving` establishes it under the bounded worker pool: one worker is held between its HEAD and its DELETE while another completes its whole member on the wire — comparison HEAD, DELETE and verifying read — with the recorded order `HEAD a, HEAD b, DELETE b, HEAD b, DELETE a, HEAD a`.

## The issued mark precedes the sweep's comparison HEAD

`TestPrefixSweepIssuesBeforeTheComparisonHead` reads the ledger at the sweep member's comparison HEAD and finds the issued mark already written, exercising the sweep's own member path rather than the selected set's or the static segment loop's.
This slice's control moves the sweep's `MarkIssued` after `DeleteMember`, and the test then fails with issued count zero at the HEAD; the selected set's own path is covered separately by `selected-4e63b6a0` S11.

## The concurrent recording-failure boundary

`TestPrefixSweepHeldWorkerFinishesWholeWhenAnotherRecordIsLost` holds one already-issued worker at its comparison HEAD while another member's outcome write fails and latches the ledger unavailable; the held worker then completes its own DELETE whole, the latch runs no postcondition and deletes no container, and exactly the admitted members were issued.
A member in flight is never interrupted inside its own requests (ADR 0095 decision 4, line 119).
Bound: with the two admitted members both issued before the latch, "no later issuing" is shown as the issued count equal to the admitted set; a member admitted only in a later listing pass is prevented from issuing by the member path's lost-reason guard, which is structural and not separately mutated here.

## Preview, dialog, segment job and admission

`TestDeletePreviewCountsAndDisclosures` states the counts, the `2N + Z` cost and the strategy; control dd0d packet P mutates the cost to count N alone.
The frontend `previewBinding.test.ts` shows the cost sentence, the typed confirmation, and that Start waits for the versioning facts.
`TestSegmentJobSettlesAdmittedSegmentsWhenTheManifestIsNotDeleted`, `TestSegmentJobLeavesTheJobOpenWhenTheManifestRecordIsLost` and `TestSegmentJobDisclosesTheDeletedManifestWhenALaterWriteFails` cover admission settlement and the two lost-record boundaries.
`TestSegmentJobRecordsTheReviewedAuthorityBeforeAnyEffect` and `TestDeleteReviewedManifestRefusesAnIncompleteGraphResponse` cover admission before effect and the strict graph read.

## Retained-mutant obligations, each with its test and control

| Obligation (ADR 0095 decision list) | Test | Control |
| --- | --- | --- |
| a bulk request used | `TestPrefixSweepDeletesEveryMemberExactly` | selected S1 (shared DELETE form) |
| the listed row ignored for a HEAD baseline on an ordinary row | `TestSelectedDeleteDeletesEveryMemberExactly` | selected S2 |
| the replacement adopted as the baseline when a comparison finds a manifest | `TestPrefixSweepRefusesAMemberChangedSinceListed` | selected S3 |
| a DELETE repeated after a lost reply | `TestPrefixSweepUnverifiedWhenReplyLost` | selected S4 |
| cancellation honoured inside a member | `TestPrefixSweepCancelDuringSweep` | selected S5 |
| the issued mark written after the comparison HEAD | `TestPrefixSweepIssuesBeforeTheComparisonHead` | dd0d F-issue (sweep path); selected S11 (selected path) |
| a new member issued after a lost record / concurrent record failure | `TestPrefixSweepHeldWorkerFinishesWholeWhenAnotherRecordIsLost`, `TestPrefixSweepLeavesTheJobOpenWhenARecordIsLost` | dd0d F10/F9 family; selected S10 (terminal after lost record) |
| the container deleted after a partial sweep | `TestDeleteContainerSurvivesAPartialSweep` | dd0d C |
| the container reported deletion on a zero-effect cancel | `TestDeleteContainerEmptyCancelledIsCancelled` | dd0d F10 |
| a non-empty postcondition with no request reported complete | `TestPrefixSweepFailsWhenScopeNonEmptyWithNoRequest` | selected S10 (shared terminal rule) |
| the empty-mode configuration change omitted during a partial | `TestEmptySweepReportsAConfigChangeAfterAPartial` | dd0d F9 |
| a conflict recorded without its identities | `TestSegmentJobRefusesAnEqualTagGraphSwap` | dd0d F4; selected S8/S17/S18 |
| a member keyed by the bare name, colliding across containers | `TestSegmentJobKeysSegmentsByContainer` | dd0d F5 |
| the preview cost counting N alone | `TestDeletePreviewCountsAndDisclosures` | dd0d P |
| the manifest deleted by a plain DELETE without the comparison | `TestDeleteReviewedManifestRefusesAnEqualTagGraphSwap` | dd0d G |
| the graph resolved again at execution instead of the reviewed membership | `TestDeleteReviewedManifestRefusesAMovedDLONamespace` | dd0d G (static); marker/manifest packet (dynamic) |
| the reviewed graph read permissively or on a partial response | `TestDeleteReviewedManifestRefusesAnIncompleteGraphResponse` | dd0d G2, G3 |
| the reviewed authority recorded by a later write, not the admitting row | `TestSegmentJobRecordsTheReviewedAuthorityBeforeAnyEffect` | dd0d F1 |
| the admitted segments left unsettled on a refused manifest | `TestSegmentJobSettlesAdmittedSegmentsWhenTheManifestIsNotDeleted` | dd0d F0b |
| the segment phase started after an unverified manifest | `TestSegmentJobLeavesSegmentsOnUnverifiedManifest` | dd0d F4 family |
| a deleted outcome fabricated from the manifest's issued mark | `TestSegmentJobLeavesTheJobOpenWhenTheManifestRecordIsLost`, `TestSegmentDeletionSurvivesAProcessRestart` | dd0d F0b family; process restart |

"dd0d" is this slice's packet `controls/adr0095-fixes-<head>`; "selected" is `controls/selected-4e63b6a0`, whose controls mutate the shared `DeleteMember`/`deleteShown` code this path reuses unchanged.

## Bounds and outstanding receipts

This slice's packet seals twelve controls, each with its mutation diff, named failing test, clean and mutant output, and source hash; the shared-protocol obligations are backed by the selected-set and marker/manifest packets, which mutate the same unchanged code.
The owned-Swift producers establish mock-and-cluster agreement for the ordinary deletion journeys and were last run in the db:925 round; this record does not re-run them and claims no fresh hosted run.
The process-restart evidence is the two child-process tests named above; the scale test's readback is now a fresh child process, not an in-process reopen.
The "no later issuing" bound stated above is the one case exercised by count rather than by a held un-issued member.
