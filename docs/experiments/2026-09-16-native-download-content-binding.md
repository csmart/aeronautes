# Native download remainder of L05 — 16 September 2026

[ADR 0045](../adr/0045-route-content-binding.md) delivered same-size binding on the native download route and left three L05 cases named rather than closed.
This record is the source-derived remainder for that route.
It is the evidence for [ADR 0060](../adr/0060-native-download-content-binding.md).
It does not claim a new Swift run.

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
Package tests against the in-process mock cover a two-run GET permutation, a missing run replaced by a duplicate, a short body at a run boundary, a same-size source replacement after the transfer set is built, and a published name replaced before verification is recorded.
The engine path is unchanged.
This still closes neither L05 nor DS-04, and the valuable-data hold is unchanged.
