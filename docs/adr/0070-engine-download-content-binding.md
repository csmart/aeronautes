# ADR 0070: bind engine downloads to the source they were planned from

**Status:** Accepted; implemented on 23 September 2026.
The contract was reviewed independently before implementation; the [engine-binding record](../experiments/2026-09-22-engine-download-binding.md) carries the maintained proof and the mutants.
This closes the engine-route gap that [ADR 0045](0045-route-content-binding.md) recorded and [ADR 0060](0060-native-download-content-binding.md) left unbound.
Amended on 16 November 2026 by [ADR 0122](0122-the-stores-answer-is-the-result.md) decision 5: the engine writes each file in the chosen folder and its own line is the file's outcome; the staged comparison and placement of this record are no longer made.

## Context

A download selects its objects with `PreflightDownload`, which captures a source identity for every planned item: the listing's length and entity tag, or the HEAD's class, length, entity tag and manifest for a zero-byte row, and the SLO entity tag when the listing carries one.
Items routed natively use that identity: the GET is matched against it, the body is digested along the source's content ladder as it streams into the temporary, and publication holds the verified inode until a final confirmation (ADR 0060, ADR 0061).

Items routed through the pinned engine do not.
The child copies them into the held stage, and `placeDownloaded` checks only that each staged entry is a regular file of the planned size before linking it into place and reporting it verified.
The [engine-binding record](../experiments/2026-09-22-engine-download-binding.md) reruns the retained assessment probe on current master and shows what that means: for plain, dynamic large, static large and symlinked objects, on both the mock and the owned Swift fixture, the engine route reports every item verified after same-size corruption of the staged bytes, a same-size permutation or a missing-with-duplicate substitution of a large object's runs, replacement of the staged entry, replacement of the source object after planning, and replacement of the published file.
For the five content and source faults the published digest differs from the planned one; for published replacement the probe replaced the published file itself and makes no digest comparison, and the item is still reported verified.
Only a short body is caught, by the size check.

ADR 0045 deferred this comparison for an empirical reason: on the engine and fixtures of the time an altered body ended with nothing staged, so a comparison could not be shown to fire.
The rerun supplies the fixture that reason asked for.

## Decision

### The engine route verifies staged bytes against the planned source before publishing

1. The identity captured at `PreflightDownload` binds engine items exactly as it binds native items.
   Nothing about capture changes: it already covers every planned object.

2. After the child returns and the stage is confirmed undisplaced, and before any placement, each engine item is verified from the held stage.
   The verifier opens the staged entry through the held stage root and keeps that descriptor open until the item is placed or refused.
   It builds the expectation from the captured identity: a plain object's ladder is its captured length and entity tag; a dynamic or static large object's ladder is built from the live source the way the native route builds it, after a live HEAD that must match the captured identity.
   It digests the staged bytes along that ladder, with the same comparison and the same `incomplete` classification for an expectation that cannot be built.

3. A staged entry whose bytes do not match is not published.
   The item reports `unverified` with effect `unresolved`, its staged entry is retained for the operator, and the stage cleanup that [ADR 0048](0048-cancelled-stage-removal.md) authorises exempts it, as that decision anticipated.
   A source whose live HEAD no longer matches the captured identity is source substitution: the item reports `unverified` with effect `unresolved` without a digest, and its staged entry is retained the same way.

4. Publication is bound to the verified inode.
   The link is made from the descriptor that was digested, not from the staged name, and after linking, the published name is confirmed to identify that inode.
   A confirmation failure reports `unverified` with effect `unresolved`, publishes nothing further for that name, and does not unlink the occupant, which is the ADR 0045 cleanup rule.

5. The item's execution and verification records keep their placement timing, and a separate final-publication fact records the confirmation for engine placements as ADR 0061 records it for native ones.

6. The size check stays; it is now the first of the comparisons rather than the only one.

### Cost

One local read of every engine-downloaded file, and one HEAD plus one segment listing or manifest read for each planned large object.
No object bytes are added to the network.
The read is bounded by the staged size the child already wrote.

## Consequences

An engine download can no longer report a file verified whose bytes differ from the object that was planned, whether the difference arose at the source after planning, in the stage, or at publication.
A refused item keeps its staged bytes where the operator can inspect them, which changes what a completed job with refusals leaves in the stage.

The mock Swift fixture listed a static large object's manifest bytes rather than its logical size, so the probe's SLO control failed at planning; the implementation corrects the fixture's manifest PUT so its listing, HEAD, GET and manifest read describe what a deployed Swift reports.

## Maintained proof

The retained probe's cases become maintained system tests on both fixtures: for plain, dynamic large, static large and symlinked objects, a matching control publishes; corruption, permutation, missing-with-duplicate, staged replacement, source replacement and published replacement each refuse before publishing with the staged entry retained; a short body still refuses.
Before the correction the six refusal cases fail as false verifications; after it they pass.
Compiled mutants must catch a deleted comparison, a deleted publication confirmation and a deleted cleanup exemption.
An instrumentation or compilation failure is a failed run, never mutant credit.

## Limits

This binds the engine download route only.
Copy, upload and staged upload commit remain the named deferred gap.
A symlinked object is bound to the content its listing or HEAD identity describes, as today.
No native, hosted or provider acceptance follows, and the valuable-data hold stays on.
