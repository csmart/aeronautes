# ADR 0095 required-proof map

[ADR 0095](../adr/0095-prefix-deletion-by-the-per-name-protocol.md) lists the proof its implementation must carry before it is accepted.
This record maps each required case to the executable test that establishes it and, where one exists, to the control that proves that test catches the fault by injecting it into the exact code and watching the test fail.
A control is cited only when its mutation changes the decision the obligation names; where no dedicated mutant is retained, the row says so rather than borrowing an unrelated one.

The evidence is of four distinct kinds, kept distinct here:

- **In-process mock tests** in `internal/app` and `internal/swiftcontrol`, driving the real services and protocol against the in-process Swift mock.
- **Real process-restart tests**: `TestSegmentDeletionSurvivesAProcessRestart` (the segment job crashed and recovered in a fresh process) and the scale test's child readback (the completed 520-member sweep read back by a fresh process).
- **Owned-Swift producers** in `internal/systemtest`, run against a real cluster. The segment-deletion producers were brought to the ADR 0095 job model, so the request carries the reviewed graph and the job's own recheck catches a move during the preflight, and were re-run against a real cluster at this head; the prefix and selected-set producers are unchanged.
- **Mutant control packets**: this slice's packet `controls/adr0095-fixes-<head>` (referred to below as "this packet"), and the shared-protocol packet `controls/selected-4e63b6a0`. The sweep and the segment job call `Session.DeleteMember` and the `deleteShown` comparison protocol unchanged from the selected set, so a selected-set control carries through only where its mutation lands in that shared code and produces the same outcome; a control that mutates a selected-only caller or a different decision does not, and is not cited for this path.

## Reuse of the verified per-name protocol

The bulk sweep and the segment job delete by the same per-name protocol the selected set uses: `Session.DeleteMember` and `deleteShown`, unchanged.
`TestPrefixSweepDeletesEveryMemberExactly` and `TestSegmentJobDeletesStaticManifestAndSegments` exercise it, each asserting no segment-expanding `multipart-manifest=delete` request and no Swift bulk-delete request is sent.
A bulk-delete call site can be injected rather than mutated: this packet's `C-bulk-api` adds a `Session.BulkDelete` call after the member protocol, and `TestPrefixSweepDeletesEveryMemberExactly` fails on the resulting account-level `POST /v1/AUTH_test?bulk-delete`. The per-name shape those tests require is what forbids it.

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
The sweep's lost-record guard is over-determined with `MarkIssued`, which refuses to write to the unavailable store, so the test's "no later issuing" is enforced by that latch whether or not the guard fires: removing the guard alone is an equivalent mutant. The retained control `C-request-after-lost-record` is therefore not a guard removal; it injects the forbidden request directly, and the fixture fails on the forbidden `f6` HEAD.

## Retained-mutant obligations, each with its test and control

| Obligation (ADR 0095 decision list) | Test | Control |
| --- | --- | --- |
| a bulk request used (the Swift bulk-delete API) | `TestPrefixSweepDeletesEveryMemberExactly`, `TestSegmentJobDeletesStaticManifestAndSegments` | this packet `C-bulk-api` (adds a `Session.BulkDelete` call after the member protocol; the test fails on `POST /v1/AUTH_test?bulk-delete`). S1 is a different fault, the per-object segment-expanding form, covered by the no-`multipart-manifest=delete` assertion above |
| the listed row ignored for a HEAD baseline on an ordinary row | `TestSelectedDeleteDeletesEveryMemberExactly` | selected S2 (shared baseline code) |
| the replacement adopted as the baseline when a comparison finds a manifest | `TestPrefixSweepRefusesAMemberChangedSinceListed` | selected S3 (shared comparison code) |
| a DELETE repeated after a lost reply | `TestPrefixSweepUnverifiedWhenReplyLost` | selected S4 (shared lost-reply code) |
| cancellation honoured inside a member | `TestPrefixSweepCancelDuringSweep` | this packet `C-cancel-inside-member` (the member DELETE moved to the cancellable context, so a cancellation reaches inside) |
| the issued mark written after the comparison HEAD | `TestPrefixSweepIssuesBeforeTheComparisonHead` | this packet `F-issue` |
| a new member issued after a lost record | `TestPrefixSweepHeldWorkerFinishesWholeWhenAnotherRecordIsLost` | this packet `C-request-after-lost-record` (requests a queued member in the lost-record branch; the fixture fails on the forbidden `f6` HEAD). It injects the forbidden request directly, because the sweep's own two guards, the member-start lost-reason check and `MarkIssued` refusing the unavailable store, are each individually redundant |
| the container deleted after a partial sweep | `TestDeleteContainerSurvivesAPartialSweep` | this packet `C-container-deleted-after-partial-sweep` |
| the container reported deletion on a zero-effect cancel | `TestDeleteContainerEmptyCancelledIsCancelled` | this packet `F10` |
| a non-empty postcondition with no request reported complete | `TestPrefixSweepFailsWhenScopeNonEmptyWithNoRequest` | this packet `C-postcondition-nonempty-reported-complete` |
| the empty-mode configuration change omitted during a partial | `TestEmptySweepReportsAConfigChangeAfterAPartial` | this packet `F9` |
| a refused name requested again on a later pass | `TestPrefixSweepLaterPassRequestsOnlyNewNames` | this packet `C-held-name-request` (requests the held row while keeping its exclusion from new admission, so a settled name is requested again on a later pass; the test reports the refused link's three HEADs) |
| a conflict recorded without its identities | `TestSegmentJobRefusesAnEqualTagGraphSwap`, `TestSelectedDeleteChangedMemberKeepsBothIdentities` | this packet `F4` (manifest baseline); selected S17/S18 (shared successor/baseline/comparison drop) |
| a member keyed by the bare name, colliding across containers | `TestSegmentJobKeysSegmentsByContainer` | this packet `F5` |
| the preview cost counting N alone | `TestDeletePreviewCountsAndDisclosures` | this packet `P` |
| the static reviewed graph resolved again at execution | `TestDeleteReviewedManifestRefusesAnEqualTagGraphSwap` | this packet `G` |
| the reviewed graph read permissively or on a partial response | `TestDeleteReviewedManifestRefusesAnIncompleteGraphResponse` | this packet `G2`, `G3` |
| the reviewed authority recorded by a later write, not the admitting row | `TestSegmentJobRecordsTheReviewedAuthorityBeforeAnyEffect` | this packet `F1` |
| the admitted segments left unsettled on a refused manifest | `TestSegmentJobSettlesAdmittedSegmentsWhenTheManifestIsNotDeleted` | this packet `F0b` |
| the segment phase started after an unverified manifest | `TestSegmentJobLeavesSegmentsOnUnverifiedManifest` | this packet `C-segment-phase-after-unverified-manifest` |
| the manifest deleted by a plain DELETE without the identity comparison | `TestDeletePlainObjectRefusesWhatItWasNotShown`, `TestDeleteReviewedManifestRefusesAnEqualTagGraphSwap` | this packet `C-identity-comparison` (disables the shared fingerprint comparison; both identity-change cases fail at `deleteobject_test.go:117`). `G` covers only the graph half of the comparison |
| the reviewed DLO namespace resolved again at execution | `TestDeleteReviewedManifestRefusesAMovedDLONamespace` | this packet `C-dlo-namespace-reresolved` (the move is to an equal-content second namespace, so the identity comparison passes and the namespace guard itself refuses) |
| a deleted outcome fabricated from an issued mark | `TestSegmentJobLeavesTheJobOpenWhenTheManifestRecordIsLost`, `TestSegmentDeletionSurvivesAProcessRestart` | this packet `C-fabricate-deleted-from-issued-mark` (reopen gives an issued-but-unrecorded member a deleted outcome) |

## Bounds and outstanding receipts

This slice's packet seals twenty-one controls, each with its mutation diff, named failing test, clean and mutant output and source hash. The bulk-API, later-pass, lost-record and identity-comparison controls each inject the actual forbidden effect: a bulk-delete POST, a request for the held row, a request after a lost record, and the fingerprint comparison disabled. Only the lost-record control faces redundant guards: its member-start check and issued-mark latch each independently prevent a later request, so removing either alone would not have failed a test, and the control injects the request directly instead. The identity-comparison control is the opposite case: disabling its one shared comparison is a single removal that does fail. S1 is cited only for the per-object segment-expanding form, not the account-level bulk API.
The shared-protocol rows cite only the selected-set controls whose mutation lands in the unchanged `DeleteMember`/`deleteShown` code and yields the same outcome; the selected-only cancellation (S5), lost-record terminal (S10) and ledger-write (S8) controls are not cited for this path. S1 is cited, as the shared per-object segment-expansion coverage that the bulk-API row keeps distinct from the account-level bulk-delete API.
The owned-Swift producers establish mock-and-cluster agreement for the deletion journeys; the segment-deletion producers were re-run against a real cluster at this head after being brought to the job model, and the others are unchanged.
The "no later issuing" case is exercised by the barriered held-worker test above with a fixture larger than the worker pool.
