# ADR 0111 — segmented uploads as static large objects

**Status:** Proposed on 12 November 2026, from finding F35 and the operator's decisions of 12 November: static large objects by default, dynamic ones still available, both tested, and the choice settable per bookmark.
**Scope:** how an upload stores a file larger than its chunk size; the engine's size check for those uploads; the content binding of a static publication; the per-job and per-bookmark setting and the upload preview's disclosure.
**Amends:** [ADR 0005](0005-large-object-safety.md) (its sentence that rclone-executed uploads are DLOs and a native SLO composer is out of v1), [ADR 0080](0080-upload-content-binding.md) decisions 3 and 4 and the listing refusal [ADR 0042](0042-source-content-ladder.md) keeps, each for a static publication only, [ADR 0081](0081-upload-overwrite-acknowledgement.md) decision 6's declared-tag exception, requirement K-XFER-007, and DESIGN §25 and §63.8 for uploads.

## Context

Swift stores at most 5 GiB in one object, so a larger file is split into segments.
Aeronautes leaves the chunk size at the engine's default of 5 GiB unless a job or a bookmark sets a smaller one, so only files above that size are segmented.
The engine can write segments only as a dynamic large object (DLO): a manifest naming a segment prefix, whose size and content the store computes from a listing of that prefix on every read.
Listings are eventually consistent.

The first real-cloud journey run (F35) uploaded a 3 MiB file in 1 MiB segments to a genuine OpenStack Swift cloud, and the engine failed it with "corrupted on transfer: sizes differ 3145728 vs 2097152": straight after the upload, the store's listing did not yet show the last segment, and the engine's own size check, run once, read the short size.
One upload in four to that cloud failed this way, and none to the Ceph RGW cloud, whose listings are consistent.
Against the Swift mock with a listing that leaves out a DLO's last segment for its first reads, the pinned engine fails the same way, and with `--ignore-size` it completes, so that flag is what turns its size check off.
Today's commit of a dynamic upload reads the assembled staged bytes once (ADR 0080 decisions 3 and 4), and that read goes through the same listing, so on such a store the failure would only move from the engine to the commit.

A static large object (SLO) is a manifest listing each segment's path, entity tag and size; the store checks every named segment against it when the manifest is written and refuses the whole manifest, storing nothing, when one does not match (the 400 ADR 0080's context measured), and reads follow the list, not a listing.

## Decision

1. **Segmented uploads are static large objects by default, settable per bookmark and per job.**
   A bookmark gains a large-object setting, `slo` (the default) or `dlo`, beside its segment placement and chunk size; it is carried by the backup like them.
   A per-job transfer setting of the same name overrides it in the upload dialog's settings, and the job records the effective value and where it came from (default, bookmark or job), as it does for its other settings.
   When the destination's `/info` does not advertise static large objects, an `slo` choice uploads as `dlo` and the preview says so.
   The upload preview's disclosure names the kind: a segmented file becomes a static large object, or a dynamic one whose reads depend on listing consistency.
   The setting is ignored when no file is segmented.
2. **The engine moves the bytes; Aeronautes composes the static manifest at the commit.**
   The engine uploads the segments into the job's own recorded segment prefix, naming them with its zero-padded index (`00000000` upwards), and stages its dynamic manifest, as it does today, with ownership and recovery records as today.
   An engine run that segments a file is given `--ignore-size`, because its single size check reads through the lagging listing; the pairing below replaces it.
3. **Pairing segments with the local file, before any publication.**
   The local file is split by the same effective chunk size the engine was given, the last chunk short when the size is not a multiple of it.
   Segment `0000000i` under the job's prefix must have the size of chunk `i` and an entity tag equal to the hexadecimal MD5 of chunk `i`.
   Aeronautes lists the job's prefix, repeating with a bounded wait while the listing lags, until every index from 0 to the last is present with a matching size and tag and no other name is.
   A name the pairing does not expect, a missing index after the bound, or a size or tag that does not match fails the item as unresolved before any publication, with its segments kept and recorded.
4. **The static publication's binding.**
   For a static publication only, this pairing stands in for ADR 0080 decisions 3 and 4's single read of the assembled staged bytes, and a listed size and entity tag are accepted where ADR 0042 refuses a listing: they may be, because the manifest names each segment's tag and size and the store refuses the manifest, storing nothing, when a named segment does not match it.
   The manifest names only whole segments under the job's recorded prefix, in index order, with no ranged, inline or nested entries.
   Its write declares no content entity tag: the file's MD5 would bind the JSON body, not the segments, so ADR 0081 decision 6's exception for a dynamic manifest's empty body is restated for this non-empty one.
   The conditional create, or the acknowledged overwrite, and the fixed-point read stay as ADR 0081 and ADR 0085 set them: the listing wait is preparation, and the fresh read of the live name comes immediately before the publishing request.
   After the write, the live object is verified as a static large object of the file's size whose entity tag is the MD5 of the concatenated hexadecimal segment tags in index order, computed from the local chunks.
   The staged dynamic manifest is then removed as staging.
5. **Segments stay under the existing proof.**
   A published static object's segments are deleted only on ADR 0005's static-object proof: manifest-only, unless every entry sits under a prefix this job recorded; never by a prefix sweep copied from the dynamic path.
   Ownership and recovery records stay as they are, which is what makes that proof possible.
6. **Dynamic large objects stay available, and stop failing on a lagging listing.**
   With `dlo`, the commit publishes the dynamic manifest as today, the engine run is given `--ignore-size` too, and today's read of the assembled staged content repeats with the same bounded wait before it fails; that repeated read is preparation, before the fixed point.
7. **ADR 0005's sentence is replaced:** the engine still uploads segments, and for a segmented upload Aeronautes composes the static manifest at the commit; a native static composer is in scope for that and nothing else.
8. **Copies follow in a second step under this record**: a copy that segments at its destination still writes a dynamic object until then, and the copy preview keeps saying so.

## Consequences

- A segmented upload no longer fails at random on a store with lagging listings, and assembling what it publishes no longer depends on a listing.
- A segment replaced after the store accepted the manifest remains the window ADR 0080 decision 6 left open: the same experiment found the static tag kept the value recorded at the manifest write while the assembled read returned 409.
- Composing the manifest costs one listing of the job's own prefix, repeated only while it lags, and one read of the local file for the chunk digests.
- The engine's own size check is off for segmented runs; the per-segment pairing is a stronger check than the single size it replaces.
- Two publication shapes remain while dynamic objects stay available.

## Tests

- Go: chunks of a file split by the effective chunk size, a short last chunk included; a manifest composed in index order from paired segments; refused before publication: an unexpected name, a missing index after the bound, and a segment whose size or tag differs from its chunk; the bounded wait succeeding when the mock's listing lag ends within it and failing unresolved when it does not; the write declaring no content tag and the live tag checked as the MD5 of the concatenated hexadecimal segment tags; the dynamic path passing under the same lag; an overwrite of an existing large object; segment deletion of a published static object refused without the static-object proof; the bookmark default, the job override and the recorded origin; the `/info` fallback and the preview naming the kind.
- System, against SAIO: a segmented upload as a static and as a dynamic object, each downloaded and compared byte for byte, and the live object's class.
- Real-cloud journeys: the copy journey uploads its segmented file once as a static and once as a dynamic object on both clouds and downloads each back.
