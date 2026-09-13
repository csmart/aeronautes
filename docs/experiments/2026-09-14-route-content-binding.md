# Route content binding: what the native copy, upload and download routes accept

Follow-up to the [staged copy content record](2026-09-11-staged-copy-content.md), which bound one route and named the three it left open.
This record reproduces what those three accept, and is the evidence for [ADR 0045](../adr/0045-route-content-binding.md).

## What was asked

Finish-line item L05 requires that the enabled Copy, upload and download routes reject same-size corruption, missing or reordered segments and source or publication substitutions, or report an explicit unverified or refused outcome with uncertain residue retained.
[ADR 0042](../adr/0042-source-content-ladder.md) delivered that for the staged copy route only, and said so.
The question here is what the other three do.

## Method

A probe ran each route end to end through the transfer service against the two-account fixture on loopback port 18080, with a proxy in front of the account under test.
The proxy alters one body — the destination PUT for the two writing routes, the source GET for the download — by inverting every byte.
Length is preserved exactly, so every length comparison on the route still passes.
Nothing else about the route, the engine or the store is changed.

The proxy stands in for any cause of a same-size difference between what a route believed it moved and what the far side holds: a store that stored something else, a middlebox, a refilled buffer.
It is deliberately not a timing race: the reproduction is deterministic, and it does not depend on a listing lagging its object.

Names carrying an engine-reserved rune plan onto the native route, which is how the native cases are reached.

Three of the four cases carry a healthy control: the same proxy, in the same place, passing bytes through unchanged.
A control that failed would mean the fixture breaks transfers, and the corrupting case would then say nothing about the route.
The staged upload commit case has no control, so it is reported as a reproduction without one rather than as a controlled result.

An earlier draft of the probe compared the namespace oracle's SHA-256 object digest against an MD5 of the expected bytes.
Those two values can never be equal, so the guard that was supposed to prove the fixture had taken effect could not fire, and it would have reported success whether or not the bytes differed.
It was corrected before the recorded run: snapshot comparisons use the oracle's digest, and MD5 appears only where a Swift entity tag is meant.
The reason the other controls here can be trusted is that this one was found and is stated.

## What reproduced

Four intended failures, three controls passing, one run, exit 1.

| Case | Observed |
| --- | --- |
| Native copy of a dynamic large object source | destination holds different bytes of the same 8192, counted 1 copied |
| Native upload | store holds different bytes of the same 9728, counted 1 copied and 1 verified |
| Native download | placed file holds different bytes of the same 10240, counted 1 verified |
| Staged upload commit | store holds different bytes of the same 10240, counted 1 verified |

| Control | Observed |
| --- | --- |
| Native copy through the identity proxy | destination digest equals the source, 1 copied |
| Native upload through the identity proxy | stored digest equals the local file, 1 copied |
| Native download through the identity proxy | placed bytes equal the object, 1 verified |

## Why each one passes today

`nativeCopy` calls `verifyCopied`, which compares logical length always but compares entity tags only when the source and the destination are both plain.
A segmented source under the single-PUT limit is streamed into a plain destination, so the classes differ and the tag comparison is skipped.
The name is also stamped with a fingerprint derived from the source HEAD in the same PUT, before any content check, and later equality and skip decisions read that stamp back.

`verifyUpload` compares the live object's logical length against the local file's recorded size.
No digest of the local file is taken anywhere on that route.
`commitStagedUpload` admits the staged object on its logical length alone before copying it to the live name, so a difference introduced between staging and publication is not examined either.

The download route compares size on both paths and nothing else: `finishNativeDownload` checks the bytes written by `io.Copy` against the object's size, and `placeDownloaded` checks the staged file's size for a file the engine wrote.
Publication itself is a no-replace link through the held directory, and `publishVerified` already requires the published entry to name the same file that was checked; what is missing is that the check is only a size.

## Where the bytes already are

The staged copy route had to read the object back over the network, because by the time it could compare, the bytes were only in the store.
Most of these cases are not in that position: the native copy body, the native upload body and the native download body all stream through the process, and an engine-staged download file is already on local disk.
Those comparisons cost no object bytes on the network.

One case is in that position.
A staged upload that publishes an engine-segmented live object cannot be settled from a remote segment listing recombined against the live aggregate: a same-size remote segment replacement can leave the listing row and the derived aggregate unchanged, so the local file agrees with both while the remote bytes differ.
That is the counterexample ADR 0042 already disproved, and it does not become sound on a different route.
That case needs the same complete read, or an explicit unverified result.

Cheapness is not the same as sufficiency, and the fourth reproduction shows where the two part company.
`commitStagedUpload` reads the stage and then sends a separate live PUT, and the probe alters that PUT while leaving the stage intact.
A comparison that stops at the staged object therefore answers a question nobody asked: the stage was fine.
Whatever a route accumulates or digests, the value it must be compared against is the one the published name reports.

## The asymmetry that cannot be engineered away here

The download stages and publishes by a no-replace link, so it can compare before publishing and refuse with the staged file retained.

Native copy and native upload publish with a single PUT at the live name.
A store that did not keep what was sent can only be detected after that PUT.
Under [ADR 0020](../adr/0020-recovery-containment.md) nothing is deleted or rewritten to repair it, so the honest outcome is an unresolved effect with the live name reported as residue.
Detecting a mismatch while the body is still being sent permits abandoning the request, but abandonment does not prove that no object was created, so only a refusal decided before the first byte can report that nothing was attempted.
Routing those two paths behind a staged name would close the gap; that is recommended separately and is not part of the proposed decision.

## What this record does not establish

It does not measure the engine's own checksumming, and does not rely on it.
Whatever the pinned engine verifies internally is a property of a third-party binary rather than evidence this project produced, and it does not cover the native routes or the commit copy at all.

Taking a fresh expectation at execution time is not a defence against a source that changed since preflight: these local preflight items retain no prior content identity, so there is nothing for a fresh observation to disagree with.

It reproduces same-size corruption only.
Missing and reordered segments, and whole-job source and publication substitution, are named by L05 and are not reproduced here; they remain open, and this record closes neither L05 nor DS-04.

No implementation is proposed by this record; ADR 0045 carries the decision, and no code changed.

## Evidence

Durable private packet `~/code/.aeronautes-work/20260913-claude/l05b/`, with `SHA256SUMS` beside the files.

| File | SHA-256 |
| --- | --- |
| `probe-before-verbose.log`, the run that produced the tables above including the control passes | `187bd272b58023e54e3d11bfb989af13ffd035d59f226c8fb1c2f88a5b29e73b` |
| `route_content_binding_probe_test.go.txt`, the inert probe source | `aea3910c2011ff61bd6ae9f160216963e235b1fc8ffd681dd60893d38ca31b61` |

The probe is archived rather than kept in the repository because it asserts behaviour that does not exist yet.
The two evidence files are cited by their own digests rather than by the packet's checksum file, because that file also covers the working state note and changes whenever it does.
