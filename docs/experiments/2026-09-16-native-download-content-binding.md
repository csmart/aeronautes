# Native download remainder of L05 — 16 September 2026

[ADR 0045](../adr/0045-route-content-binding.md) delivered same-size binding on the native download route and left three L05 cases named rather than closed.
The original 16 September sections describe the source-derived remainder for that route at `e770f8a9`.
It is the evidence for [ADR 0060](../adr/0060-native-download-content-binding.md).
Those original sections do not claim a new Swift run; the later revalidation sections describe subsequent corrections and evidence.

## What L05 still names on download

Finish-line item L05 requires enabled routes to reject same-size corruption, missing or reordered segments, and source or publication substitutions, or to report an explicit unverified or refused outcome with uncertain residue retained.
On master `e770f8a98c8ad67bc70e2137f66a598271be8195` the native download route refuses a same-size inverted GET and retains the temporary.
The maintained cases in `internal/systemtest/download_content_binding_test.go` are a plain object with an engine-reserved rune, plus a matching-content engine control.
They do not cover a segmented source, a source replaced after the transfer set is built, or a published name replaced after the no-replace link.

## What the native helper actually compares

`nativeDownload` opens the object, builds the ADR 0042 ladder from that HEAD, and `finishNativeDownload` digests the GET along the ladder before `publishVerified`.
A two-run ladder would refuse a GET whose runs are swapped or whose missing run is replaced by a duplicate of another, because `LadderDigest` compares each run's length and MD5 in order.
That comparison is untested on this route.
The helper tests stand in a one-run `PlainLadder` of the served bytes.
A native DLO or SLO download is not in the maintained system cases at all.

If the source itself is reordered or replaced before `OpenObject`, the execution-time ladder is the new source, the GET matches it, and the item is verified.
`DownloadItem` carries `Name`, `RelPath`, `Size`, `Route` and `Dir`.
There is no captured entity tag, class or manifest from the Start preflight, so there is nothing for that fresh HEAD to disagree with.
That is the sentence ADR 0045 already used to defer whole-job source substitution.

`publishVerified` requires the linked target to `os.SameFile` the verified inode, and a substitute at the temporary is retained rather than unlinked.
Verification is recorded in the same `observe` call, and `job_item_observations` accepts one verification row: a second different verification is `ErrItemRecordConflict`.
A published name replaced after that row is written cannot become unverified.
The job-end path does not re-Lstat published names.

## Engine path

ADR 0045 left the engine download path uncompared because a comparison written there could not be shown to do work: deleting it left the altered case still refusing.
`placeDownloaded` still checks size only.
The engine matching-content case still only asserts that matching bytes arrive.
This record does not promote the earlier engine observation into a guarantee, and it does not add a comparison without a fixture that can prove the comparison.

## What this record does not establish

It does not reproduce the remaining cases on the fixture.
The gaps are in the source and in the tests that are not there.
Copy, native upload and staged upload commit stay the named deferred gap ADR 0045 already recorded.
L07 cancellation boundaries are not this slice.
This record closes neither L05 nor DS-04, and the valuable-data hold is unchanged.

The native remainder is now delivered under ADR 0060.
Package tests against the in-process mock cover a two-run GET permutation, a missing run replaced by a duplicate, a short body at a run boundary, a same-size source replacement after the transfer set is built, and a published name replaced before final confirmation.
The engine path is unchanged.
This still closes neither L05 nor DS-04, and the valuable-data hold is unchanged.

## 21 September revalidation

Revalidation of `bf4a91ed` found that the published-name comparison used a saved `FileInfo` after the writer closed and the temporary was unlinked.
An unlink and recreate at the published name could reuse the freed inode number and be falsely recorded as verified.
A probe with a filesystem flush and a 50 ms pause between unlink and recreate reproduced that failure on XFS.
The immediate remove-and-recreate regression remains useful, but a passing run of that case alone does not establish that the original inode is still held.

The correction holds a second descriptor from before writer closure through publication confirmation, comparing the published name against the live descriptor.
The deterministic Linux regression observes that an open descriptor still references the original inode after both names have been removed, then proves that confirmation releases it.
Separate cases check normal completion, admission/execution/verification recording refusals, cancellation, close and publication failures, and a substitute encountered while acquiring the pin.
The pin does not retain an extra directory entry or change the existing temporary and directory cleanup rules.
It does add one descriptor per pending native publication, and arbitrary-workload resource bounds remain unproven.

At `acda6c3c`, broader mock revalidation also exposed two required-record ordering failures and two source-close fixture failures.
Those failures blocked the range until the separate fixture and ADR 0061 corrections below.

### Source-close fixture correction

The source-close proxy removed GET Content-Length, then returned a post-GET HEAD length one byte larger than the listing.
ADR 0060 correctly refused that changed identity before creating a temporary, so the fixture stopped exercising streamed-byte retention and source closure.
The corrected fixture preserves the real source listing, entity tag and HEAD length, and ends only the chunked GET one byte early.
Both the in-flight comparison and the independent Swift reader still reject the short body, including `ObjectCorrupted` on Close.
The existing cases retain their exact served-byte, no-publication, durable item, audit, reopen and no-collateral assertions on the mock and the owned two-account Swift fixture.
Planned and listed bytes continue to describe the full source, while retained bytes describe the shorter response that was actually served.
Production source admission and closure handling are unchanged.

### Separate final-publication facts

ADR 0061 restores immediate execution and verification acknowledgement, then records final publication in a separate immutable row.
The mock and two-account Swift cases witness that the next native GET sees the earlier acknowledged verification, a substituted publication keeps its copied bytes but loses final verification, and later admission refusal or cancellation preserves completed final facts.
A SQL refusal of the final fact prevents queued engine GETs and staging while allowing read-only confirmation of other already acknowledged native publications.
Reopen preserves missing required facts, the early observations, exact totals and audit without contacting Swift.
The original required-record refusal tests retain their exact GET and filesystem delta assertions.

The item flag defaults to false for historical rows.
Upgrade cases start from each item-ledger migration present before 0013 and check that historical terminal state, counters and observations survive without invented publication evidence.
Distinct totals cover more than a page, overlap with earlier cleanup problems, and refuse complete when required evidence is absent.
The local report retains copied counts independently of the verified count, including more failures than its bounded display samples.
The generated DTO and jobs view show the early and final observations separately, including missing required, not reached and not required states.

The independent close-fault witness, deterministic live-descriptor witness and original remove/recreate sequence remain maintained.
The descriptor still costs one file handle per pending native item; the final write adds one small transaction per successful native item.
No workload ceiling, cancellation deadline, cleanup decision or valuable-data release gate changes.
