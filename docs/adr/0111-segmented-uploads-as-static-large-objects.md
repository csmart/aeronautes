# ADR 0111 — segmented uploads as static large objects

**Status:** Proposed on 12 November 2026, from finding F35 and the operator's decision of 12 November: static large objects by default, dynamic ones still available, and both tested.
**Scope:** how an upload stores a file larger than its chunk size; the engine's size check for those uploads; the per-job settings and the upload preview's disclosure.
**Amends:** [ADR 0005](0005-large-object-safety.md) ("rclone-executed uploads are DLOs; a native SLO composer is out of v1"), requirement K-XFER-007, and DESIGN §25 and §63.8 for uploads.

## Context

Swift stores at most 5 GiB in one object, so a larger file is split into segments.
Aeronautes leaves the chunk size at the engine's default of 5 GiB unless a job or a bookmark sets a smaller one, so only files above that size are segmented.
The engine can write segments only as a dynamic large object (DLO): a manifest naming a segment prefix, whose size and content the store computes from a listing of that prefix on every read.
Listings are eventually consistent.

The first real-cloud journey run (F35) uploaded a 3 MiB file in 1 MiB segments to a genuine OpenStack Swift cloud, and the engine failed it with "corrupted on transfer: sizes differ 3145728 vs 2097152": straight after the upload, the store's listing did not yet show the last segment, and the engine's own size check, run once, read the short size.
One upload in four to that cloud failed this way, and none to the Ceph RGW cloud, whose listings are consistent.
Against the Swift mock with a listing that leaves out a DLO's last segment for its first reads, the pinned engine fails the same way, and with `--ignore-size` it completes, so that flag is what turns its size check off.
A DLO's reads keep that dependence for its whole life: a download soon after an upload can return short content on such a store.

A static large object (SLO) is a manifest listing each segment's path, entity tag and size; the store checks every segment against it when the manifest is written, and reads follow the list, not a listing.

## Decision

1. **Segmented uploads are static large objects by default.**
   A new per-job transfer setting, `largeObjects`, chooses `slo` (the default) or `dlo`; it sits beside the chunk size in the upload dialog's settings, is recorded with the job's effective settings, and is ignored when no file is segmented.
   The upload preview's disclosure names the kind: a segmented file becomes a static large object, or, with `dlo`, a dynamic one whose reads depend on listing consistency.
2. **The engine moves the bytes; Aeronautes composes the manifest at the commit.**
   The engine uploads the segments into the job's own segment prefix in `<container>_segments` and stages its DLO manifest, as it does today, with ownership and recovery records as today.
   An engine run that segments a file is given `--ignore-size`, because its single size check reads through the lagging listing; Aeronautes' checks below replace it.
   At the commit, Aeronautes lists the job's segment prefix, repeating with a bounded wait, until it holds exactly as many segments as the file has chunks and each segment's size and entity tag equal the size and MD5 of the matching chunk of the local file.
   It then writes an SLO manifest at the live name naming those segments in order, under the existing fixed-point and conditional rules of the commit (ADR 0042, ADR 0085), and the store accepts it only if every segment matches.
   The live object is then verified as a static large object of the file's size whose entity tag is the MD5 of the concatenated segment tags, computed from the local chunks.
   The staged DLO manifest is removed as staging, and the segments belong to the published object exactly as a published DLO's do today.
   A listing that never settles within the bound, or a segment that does not match its local chunk, fails the item as unresolved before any publication, with its segments kept and recorded.
3. **Dynamic large objects stay available, and stop failing on a lagging listing.**
   With `largeObjects: dlo`, the commit publishes the DLO manifest as today, the engine run is given `--ignore-size` too, and the comparison of the assembled staged content with the local file repeats with the same bounded wait before it fails.
4. **Everything else about an upload is unchanged**: overwrite acknowledgement and the fixed point (ADR 0081, ADR 0085), reclaiming a replaced object's own segments, staging, recovery and the evidence records.
5. **Copies follow in a second step under this record.**
   A copy that segments at its destination still writes a DLO until then, and the copy preview keeps saying so.

## Consequences

- A segmented upload no longer fails at random on a store with lagging listings, and what it publishes reads the same on every store as soon as it exists.
- Composing the manifest from the listing costs one more listing of the job's own prefix, repeated only while it lags, and one read of the local file for the chunk digests.
- The engine's own size check is off for segmented runs; the per-segment comparison with the local file is a stronger check than the single size it replaces.

## Tests

- Go: a manifest composed from segment rows and local chunks, in order, with their tags and sizes; a segment whose tag differs from its local chunk refused before publication; the bounded wait succeeding when the mock's DLO listing lag ends within it and failing unresolved when it does not; the DLO path passing under the same lag; the published object verifying as an SLO; an overwrite of an existing large object; the setting recorded with the job and the preview naming the kind.
- System, against SAIO: a segmented upload as an SLO and as a DLO, each downloaded and compared byte for byte, and the live object's class.
- Real-cloud journeys: the copy journey uploads its segmented file once as an SLO and once as a DLO on both clouds and downloads each back.
