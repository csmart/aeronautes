# Verify plain-source content before DLO publication

This is the first Copy consumer of [ADR 0037](../adr/0037-staged-content-read.md), following the separately reviewed [native reader prerequisite](2026-09-11-native-content-read.md).
It addresses one DS-04/P16 boundary: a non-symlink plain source whose engine output is staged as a dynamic large object.
DS-04 and the valuable-data hold remain open.

## Behaviour and cost

After the existing source/stage HEAD and segment-listing length checks, Copy reads the assembled staged object once through `Session.OpenObjectContent`.
The reader requests identity encoding, requires an un-ranged HTTP 200, and owns its request, body and watchdog lifetime under ADR 0037.
Copy requires a nonnegative source length and a 32-hex MD5 expectation from the existing post-engine source HEAD.
It consumes a fixed 64 KiB buffer until EOF and compares both the byte count and MD5, accepting bytes returned together with EOF.
Subtraction before addition bounds the count without overflow; a reader repeatedly returning no bytes and no error is refused.
Matching bytes do not override a read error, a joined EOF/error, cancellation or failed Close.

The check occurs before live destination re-HEAD, committing intent and conditional publication.
Failure retains the staged manifest, its backing segments and captured recovery facts, adds no committing or committed fact, and performs no live PUT or new cleanup for that name.
The existing item-recording checks still govern whether later names or verification may proceed.
Successful content comparison preserves the existing conditional creation and post-publication path.
The source need not share the staged object's segment boundaries.

The added work is one complete staged read for each covered candidate, including candidates whose content will be refused.
It uses the existing execution concurrency, with no whole-object allocation, temporary content file, per-chunk worker or overall object deadline.
The owned-service workload below reads a 2 MiB staged subject composed of two 1 MiB segments, and the valid resegmentation control reads the same bytes across four 512 KiB segments.
The final eight-case real-Swift race run took 41.357 seconds, including fixture setup, independent snapshots, controlled mutations, public assertions and cleanup.
That duration is an observation of this workload, not isolated guard throughput, a resource ceiling or large-production-object acceptance.

## Maintained service scenarios

`copy_staged_content_test.go` and `copy_staged_content_assertions_test.go` exercise the actual `TransferService.StartCopy` route against both owned mock HTTP and two-account Swift with pinned rclone.
Each case holds the first live 19-byte control PUT after its effect, with both stages already admitted and independently readable.
Only that small PUT uses the response-capture barrier; large uploads and assembled GETs stream outside its 64 KiB capture limit.
The subject starts as distinct A and B segments, so equal byte counts cannot conceal a content substitution from the independent oracle.
Complete owned-namespace snapshots and exact request traces include unselected source and destination objects, another job's stage, and shared segments referenced by DLO and SLO aliases.

| Scenario | Required result |
| --- | --- |
| Unchanged content | Both names publish with the expected bytes and metadata. |
| Same-size corruption | The modified subject remains staged and never becomes live. |
| Permuted segments | Reversed assembled content is refused despite unchanged total length. |
| Missing segment plus duplicate | Removing A and appending a new B preserves the list sum but must refuse. |
| Valid resegmentation | Four different segment boundaries preserve content and permit publication. |
| Cancellation during the final staged GET | The subject retains a staging/cancelled/unresolved execution observation and no live publication. |
| Failed execution-record acknowledgement | The first effect remains; the subject and a third staged name gain no invented execution facts or later publication. |
| Failed terminal job/audit transaction | Acknowledged item facts remain, no terminal transaction partially publishes, and original public job services report unavailable. |

The three wrong-content cases and both persistence cases fail before the production guard against both providers because the corrupt subject becomes live.
The unchanged and resegmented controls pass before and after.
Cancellation has no credited before run because the earlier implementation never issues the new staged GET.
All eight after cases pass against each provider, including the final assertion that exactly one subject staged GET precedes any successful subject publication.
That request-count assertion was added after the recorded before and removed-safeguard runs; their exact earlier assertion sources remain preserved.

For an ordinary two-name refusal, the saved and original public Get/List/History/items/totals agree on partial, copied 1, verified 1, failed 1 and copied bytes 19.
The two item identities remain planned and admitted; failed, unresolved and problems are each 1, while Unverified is 0 because that facet counts copied items without verified comparison.
The refused execution is staging/unverified/unresolved; the existing later comparison is verification/unverified/not-attempted because the live subject is absent.
The exact terminal audit is `transfer.copy.partial` with `verify_mismatch` and the existing item-outcome summary.
The tests inspect these records after the body and bookmark pins finish, and assert that public reads do not change the saved facts.

Final-name cancellation also reaches the existing later read-only verification and finishes partial with the same counters.
It retains the cancellation cause in the execution diagnostic; this does not claim a broader stop-on-cancellation policy.
Failed required recording stops later publication and verification, preserves the first copied effect, and exposes unavailable original job services without inventing a saved failure observation.
A new StartCopy is refused before creating a job or mutating storage, but its existing connection/engine preparation can perform read-only destination HEAD and info requests before the unavailable-store check.
RerunJob refuses from the unavailable or unresolved prior facts before new storage traffic.

## Unit proof and removed safeguards

The maintained executor regression `TestWrongStagedBytesRefusedBeforePublication` first failed with copied 1, copied bytes 8, no failure and a committed prefix.
That run stops at its first report assertion; the separate service runs establish the wrong live bytes independently.
The new outcome assertion requires retained stage and segments, unresolved staging failure, captured-only recovery, and no live PUT or DELETE.
Existing unchanged-stamp, valid-resegmentation, segmented-source, materialised-plain and plain-pair controls remain in the full package run.
`TestStaleAggregateObservationStillSkipsGap` remains an explicit unresolved later-observation limitation.

The final Copy race run has 163 passing named tests: 56 top-level tests and 149 leaves, excluding every executed ancestor.
There are no skips or failed package events.
The new table-driven helper tests have 24 comparison cases and eight expectation cases, including length disagreements whose digest otherwise matches, malformed expectations before GET, uppercase hex, bounded-buffer content, EOF and distinct read/close/cancellation causes.

Six isolated variants remove length comparison, stop at matching bytes before EOF, accept EOF joined with another failure, ignore required Close, ignore cancellation during final read/close, or remove the no-progress stop.
They produce nine intended failed leaves; the matching positive suite passes.
The no-progress variant reaches the test caller's one-second deadline after more than thirteen million empty reads and fails its diagnostic, retained cause and progress-bound assertions; it is not credited merely for a process timeout.
These private variants use synthetic readers and establish comparison sensitivity, not socket or native platform lifetime.

Four separate actual-Swift variants remove the executor guard, ignore its digest result, delete the failed stage, or ignore the failed execution acknowledgement.
The first two fail because wrong content becomes live while healthy and valid-resegmentation controls still pass.
Deleting the failed stage loses its retained backing evidence; ignoring the failed acknowledgement loses the later unstarted stage before the test reaches its later live-name assertion.
Each changes one declared product file and fails its intended application assertion, not compilation, setup or a process timeout.
Their retained source precedes the final request-count/order assertion addition, so that later assertion is not credited to those runs.

## Representation compatibility gate

The separate actual-Swift compatibility probe exercises an unencoded object, two distinct equal-length valid gzip representations of the same decoded payload, deliberately malformed gzip, and an unknown content coding.
Unencoded and unknown-coding cases preserve their bytes and publish through the new staged read.
The three gzip-labelled cases fail during engine staging before the held first live publication or new content check.
The valid gzip diagnostics record an engine segment upload with ContentLength 524444 but only 524288 body bytes, while the source GET carries rclone 1.74.3's Accept-Encoding gzip.
The 156-byte difference equals the stored gzip representation's excess over its decoded payload.
The unchanged-baseline replay reproduces all three gzip failures, and the exact pinned source maps the child's automatic decoding to its upload sizing from the original metadata.
The [separate representation record](2026-09-12-copy-representations.md) retains the source attribution, dirty-binary limitation, required-success failures and earlier engine-abort cleanup separately from this consumer's refusal path.
This recorded run is a failed ADR 0037 compatibility gate, not accepted encoded-content behaviour.
The independently reviewed [ADR 0038 plan](../adr/0038-copy-representations.md) adds a separate Copy-only transport correction after this consumer's local review.
The subsequent [locally reviewed representation correction](2026-09-12-copy-representations.md) passes all five original DLO positive expectations and four empty/small plain cases through the actual service and pinned child.
The DLO cases observe the new staged identity GET before publication; plain cases retain the existing native publication read.
Independent omission and local-scope controls fail when the fixed Copy policy is absent or escapes its intended constructors.
Those later results preserve this failed gate and do not expand it to encoded listings, Range retries or native fallback policy.
The new reader, comparison and representation correction remain local pending combined current-base verification, independent range review and all five PR/resulting-master checks.

## Evidence boundaries

All producers retain exact arguments, source archives and hashes, pinned tool identities, complete Go JSON streams, stderr, timestamps, actual exits and source-stability checks.
The first nine service runs did not record their producer-script hash at execution; retained historical versions do not retroactively supply that observation.
The final service replay reproduces all 737 effective source files, and independent reconciliation matches all 18 raw run lifecycles/exits, historical test versions and selected baseline/consumer diagnostic projections.
Those reconciliation assertions are evidence checks, not new application test coverage or byte equality of the entire changing wire history.
The initial mock attempt failed while opening a sandbox-denied listener and receives no regression credit.
An early control run used the wrong recovery-row expectation: committing facts name the live object, while committed facts have an empty Object.
Another after run wrongly required zero read-only setup traffic for a refused new StartCopy.
Both failed attempts remain recorded; their test expectations were corrected against the existing implementation and contract without changing production policy.

The owned fixture uses the pinned SAIO manifest and rclone 1.74.3 with two separate test accounts.
Its Linux loopback observations do not establish native desktop, other-platform, process-crash or power-loss behaviour.
The source HEAD is not a pinned version of the engine's original source read, and the staged GET does not freeze later segment or manifest changes through publication.
MD5 supplies Swift's existing honest-provider integrity expectation, not authenticity against forged or colliding evidence.
Segmented sources, symlinks, other staged classes, local transfers, metadata/replication fidelity, final publication binding, stale later listings and legacy strength labels remain separate work.
This change grants no overwrite, Mirror start, source deletion, automatic segment cleanup or credential reclamation authority.
