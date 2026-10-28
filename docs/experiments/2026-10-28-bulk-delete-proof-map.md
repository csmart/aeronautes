# ADR 0095 required-proof map

[ADR 0095](../adr/0095-prefix-deletion-by-the-per-name-protocol.md) lists the proof its implementation must carry before it is accepted.
This record maps each required case to the executable test that establishes it and, where one exists, to the control that proves that test catches the fault by injecting it into the exact code and watching the test fail.
A control is cited only when its mutation changes the decision the obligation names; where no dedicated mutant is retained, the row says so rather than borrowing an unrelated one.

The evidence is of four distinct kinds, kept distinct here:

- **In-process mock tests** in `internal/app` and `internal/swiftcontrol`, driving the real services and protocol against the in-process Swift mock.
- **Real process-restart tests**: `TestSegmentDeletionSurvivesAProcessRestart` (the segment job crashed and recovered in a fresh process) and the scale test's child readback (the completed 520-member sweep read back by a fresh process).
- **Owned-Swift producers** in `internal/systemtest`, run against a real cluster, unchanged since db:925 and cited as prior evidence, not re-run here.
- **Mutant control packets**: this slice's packet `controls/adr0095-fixes-<head>` (referred to below as "this packet"), and the shared-protocol packet `controls/selected-4e63b6a0`. The sweep and the segment job call `Session.DeleteMember` and the `deleteShown` comparison protocol unchanged from the selected set, so a selected-set control carries through only where its mutation lands in that shared code and produces the same outcome; a control that mutates a selected-only caller or a different decision does not, and is not cited for this path.

## Reuse of the verified per-name protocol

The bulk sweep and the segment job delete by the same per-name protocol the selected set uses: `Session.DeleteMember` and `deleteShown`, unchanged.
`TestPrefixSweepDeletesEveryMemberExactly` and `TestSegmentJobDeletesStaticManifestAndSegments` exercise it, each asserting no segment-expanding `multipart-manifest=delete` request and no Swift bulk-delete request is sent.
There is no bulk-delete call site in this path to mutate; the per-name shape is asserted by those tests.

## Scale and retention, with a process restart

`TestPrefixSweepScaleBoundedAndDurable` sweeps 520 members (more than the 500-row admission chunk and the 200-row drawer page), with resident membership bounded to a listing page, and measures the ledger's write cost as committed write transactions through `db.WritesCommitted` (three a member: two ledger writes plus the per-member progress counter, asserted linear).
It then closes its database and a fresh child process reads all 520 members back in the drawer's pages with exact counts.

## A real process restart of the segment job

`TestSegmentDeletionSurvivesAProcessRestart` runs the real segment-delete job in a child process and kills it hard at two boundaries; a fresh process recovers. A crash while the manifest DELETE is outstanding recovers the manifest unresolved, never fabricated deleted; a crash just after the manifest's deletion is recorded leaves that record standing, the interrupted segment unresolved and the untouched one not attempted.

## Adjacency and the issued mark

`TestDeletePlainObjectIssuesNoAuthenticationBetweenComparisonAndDelete` establishes that nothing of a member's own and no authentication runs between its comparison HEAD and its DELETE, at the shared protocol.
`TestPrefixSweepHoldsPerMemberAdjacencyUnderWorkerInterleaving` establishes it under the pool: one worker is held between its HEAD and its DELETE while another completes its whole member, trace `HEAD a, HEAD b, DELETE b, HEAD b, DELETE a, HEAD a`.
`TestPrefixSweepIssuesBeforeTheComparisonHead` reads the ledger at the sweep member's comparison HEAD and finds the issued mark already written; this packet's `F-issue` moves the sweep's `MarkIssued` after `DeleteMember` and the test then fails with issued count zero.

## The concurrent recording-failure boundary

`TestPrefixSweepHeldWorkerFinishesWholeWhenAnotherRecordIsLost` uses explicit barriers so a member's outcome write fails while a second, already-issued worker is held at its comparison HEAD; that held worker then completes its whole member on the wire (comparison HEAD, DELETE, verifying read), the latch runs no postcondition and deletes no container, and of ten admitted members only the eight dispatched were issued, so the two queued members stay unissued.
The sweep's lost-record guard is over-determined with `MarkIssued`, which refuses to write to the unavailable store, so the test's "no later issuing" is enforced by that latch whether or not the guard fires; a single-line mutant on the guard alone is equivalent and no control is retained for it.

## Retained-mutant obligations, each with its test and control

| Obligation (ADR 0095 decision list) | Test | Control |
| --- | --- | --- |
| a bulk request used | `TestPrefixSweepDeletesEveryMemberExactly`, `TestSegmentJobDeletesStaticManifestAndSegments` | none retained: no bulk-delete call site exists; the tests assert per-name DELETEs only |
| the listed row ignored for a HEAD baseline on an ordinary row | `TestSelectedDeleteDeletesEveryMemberExactly` | selected S2 (shared baseline code) |
| the replacement adopted as the baseline when a comparison finds a manifest | `TestPrefixSweepRefusesAMemberChangedSinceListed` | selected S3 (shared comparison code) |
| a DELETE repeated after a lost reply | `TestPrefixSweepUnverifiedWhenReplyLost` | selected S4 (shared lost-reply code) |
| cancellation honoured inside a member | `TestPrefixSweepCancelDuringSweep` | none retained: the sweep runs a member's requests under a non-cancellable context and stops dispatch on cancel, so a single-line mutant on the member-start check is equivalent; test-covered |
| the issued mark written after the comparison HEAD | `TestPrefixSweepIssuesBeforeTheComparisonHead` | this packet `F-issue` |
| a new member issued after a lost record | `TestPrefixSweepHeldWorkerFinishesWholeWhenAnotherRecordIsLost` | none retained: `MarkIssued` refuses the unavailable store, so the latch blocks issuance independent of the sweep's guard, making a mutant on the guard equivalent; test-covered |
| the container deleted after a partial sweep | `TestDeleteContainerSurvivesAPartialSweep` | this packet `C-container-deleted-after-partial-sweep` |
| the container reported deletion on a zero-effect cancel | `TestDeleteContainerEmptyCancelledIsCancelled` | this packet `F10` |
| a non-empty postcondition with no request reported complete | `TestPrefixSweepFailsWhenScopeNonEmptyWithNoRequest` | this packet `C-postcondition-nonempty-reported-complete` |
| the empty-mode configuration change omitted during a partial | `TestEmptySweepReportsAConfigChangeAfterAPartial` | this packet `F9` |
| a refused name requested again on a later pass | `TestPrefixSweepLaterPassRequestsOnlyNewNames` | this packet `C-refused-name-reissued-on-a-later-pass` |
| a conflict recorded without its identities | `TestSegmentJobRefusesAnEqualTagGraphSwap`, `TestSelectedDeleteChangedMemberKeepsBothIdentities` | this packet `F4` (manifest baseline); selected S17/S18 (shared successor/baseline/comparison drop) |
| a member keyed by the bare name, colliding across containers | `TestSegmentJobKeysSegmentsByContainer` | this packet `F5` |
| the preview cost counting N alone | `TestDeletePreviewCountsAndDisclosures` | this packet `P` |
| the static reviewed graph resolved again at execution | `TestDeleteReviewedManifestRefusesAnEqualTagGraphSwap` | this packet `G` |
| the reviewed graph read permissively or on a partial response | `TestDeleteReviewedManifestRefusesAnIncompleteGraphResponse` | this packet `G2`, `G3` |
| the reviewed authority recorded by a later write, not the admitting row | `TestSegmentJobRecordsTheReviewedAuthorityBeforeAnyEffect` | this packet `F1` |
| the admitted segments left unsettled on a refused manifest | `TestSegmentJobSettlesAdmittedSegmentsWhenTheManifestIsNotDeleted` | this packet `F0b` |
| the segment phase started after an unverified manifest | `TestSegmentJobLeavesSegmentsOnUnverifiedManifest` | this packet `C-segment-phase-after-unverified-manifest` |
| the manifest deleted by a plain DELETE without the identity comparison | `TestDeleteReviewedManifestAppliesWhenTheGraphMatches`, `TestDeletePlainObjectRefusesWhatItWasNotShown` | none retained for the identity-comparison skip; the comparison HEAD is the shared `deleteShown` code, with the graph half covered by `G` |
| the reviewed DLO namespace resolved again at execution | `TestDeleteReviewedManifestRefusesAMovedDLONamespace`, `TestSegmentJobDeletesDynamicManifestAndNamespace` | none retained: the reviewed-DLO path is new and not in the marker/manifest packet; test-covered only |
| a deleted outcome fabricated from an issued mark | `TestSegmentJobLeavesTheJobOpenWhenTheManifestRecordIsLost`, `TestSegmentDeletionSurvivesAProcessRestart` | none retained for the shared `FailInterrupted` reopen rule; the tests assert an issued-only member recovers unresolved, never deleted |

## Bounds and outstanding receipts

This slice's packet seals fifteen controls, each with its mutation diff, named failing test, clean and mutant output and source hash; the six rows above with no retained control say so and are test-covered, two of them because the decision is over-determined by another layer (the unavailable-store latch, the non-cancellable member context) that already enforces it, so a single-line mutant is equivalent.
The shared-protocol rows cite only the selected-set controls whose mutation lands in the unchanged `DeleteMember`/`deleteShown` code and yields the same outcome; the selected-only cancellation (S5), lost-record terminal (S10), ledger-write (S8) and segment-expansion (S1) controls are not cited for this path.
The owned-Swift producers establish mock-and-cluster agreement for the ordinary deletion journeys and were last run in the db:925 round; this record does not re-run them.
The "no later issuing" case is exercised by the barriered held-worker test above with a fixture larger than the worker pool.
