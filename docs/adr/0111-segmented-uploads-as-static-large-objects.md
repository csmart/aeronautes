# ADR 0111 — segmented uploads as static large objects

**Status:** Accepted for 0.5.0 (12 November 2026), from finding F35 and the operator's decisions of 12 November: static large objects by default, dynamic ones still available, both tested, and the choice settable per bookmark; delivered with this record's series. Amended the same day by decisions 9 to 11, from finding F38 and the operator's decision to keep static large objects only where the store returns their manifests.
Amended on 16 November 2026 by [ADR 0122](0122-the-stores-answer-is-the-result.md) decision 3: the static manifest is written over the dynamic one the engine wrote at the file's own name, from the segment path the engine reported and the local file's chunks, with no listing of the segments; decision 9's answer is learned by reading the first static manifest back at that name, and decision 11's check manifest no longer exists.
The same decision adds one origin to decision 1: where the container keeps versions, a static choice uploads as dynamic and the job records `versioning`.
**Scope:** how an upload stores a file larger than its chunk size; the engine's size check for those uploads; the content binding of a static publication; the per-job and per-bookmark setting and the upload preview's disclosure.
**Amends:** [ADR 0005](0005-large-object-safety.md) (its sentence that rclone-executed uploads are DLOs and a native SLO composer is out of v1), [ADR 0080](0080-upload-content-binding.md) decisions 3 and 4 and the listing refusal [ADR 0042](0042-source-content-ladder.md) keeps, each for a static publication only, [ADR 0081](0081-upload-overwrite-acknowledgement.md) decision 6's declared-tag exception, requirement K-XFER-007, and DESIGN §25, §62's "SLO creation" non-goal and §63.8 for uploads.

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
   A bookmark gains a large-object setting, `slo` or `dlo`, beside its segment placement and chunk size; it is carried by the backup like them.
   A bookmark with no value, every existing one among them, reads as `slo` and stays readable, as an empty segment placement already reads as its default.
   A per-job transfer setting of the same name overrides it in the upload dialog's settings.
   Only a readable `/info` that has no static-large-object entry, or a store found under decision 9 not to return static manifests, makes an `slo` choice upload as `dlo`, and the preview says so; when the capability is unknown, because `/info` could not be read, the choice stays `slo` and the publication is the attempt, since unknown is not unsupported.
   The `/info` check uses the session's existing capability snapshot, the one the preview and the commit both read, not a read of its own; decision 9's check is the one extra read a connection may make.
   The job records, on its existing settings record, the kind it actually published, with an origin from the vocabulary it already uses: `default` for no bookmark value, `override` for the job's own choice, one new origin, `bookmark`, for the bookmark's value, and one more, `capability`, when a readable `/info` without the entry, or a store found under decision 9 not to return static manifests, turned a static choice into a dynamic upload.
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

### Amendment of 12 November 2026: static only where the store returns the manifest (F38)

Measured on the Ceph RGW cloud with an object the run created: a static large object is stored and read correctly, and its HEAD says it is static, but GET and HEAD with `?multipart-manifest=get`, with or without `format=raw`, answer 200 with an empty body.
Download and copy build a static source's content expectation from that manifest, as do the inspector's segment view and segment deletion, so on that store an uploaded static object could not be downloaded or copied by Aeronautes, though nothing is lost.
The genuine Swift cloud returns the manifest in both forms.
The operator decided on 12 November to keep static large objects where the store returns their manifests and to upload dynamic ones where it does not.

9. **Whether the store returns static manifests is learned once per connection, before the first static publication.**
   A job whose plan is static checks before its first static publication, unless its connection has already learned the answer.
   After pairing that item's segments, it writes the paired manifest at a name of its own under the job's recorded staging prefix, with the create conditional and no content tag, reads it back with the strict bounded manifest reader, and deletes it, the manifest alone.
   The store returns static manifests when that read lists exactly the entries written, in order; it does not when a complete 200 response carries an empty body, as measured.
   Any other outcome of the check, a refused check manifest among them, fails that item unresolved before any publication, with its staging kept, and leaves the question open.
   The answer is kept beside the connection's capability snapshot, as an observation of its own, until the session is forgotten; it does not replace the snapshot's source, so an unreadable `/info` stays unknown and leaves a static choice in place until this check has answered.
10. **A store that does not return them gets dynamic uploads, recorded as the capability's.**
   When the check finds the manifest not returned, the job publishes every segmented file as a dynamic large object under decision 6, and before that first publication its settings record is updated to `dlo` with the origin `capability`.
   Later previews on the connection name a dynamic upload with the same origin, and the dialog says the cluster does not offer static large objects that Aeronautes can read back.
   A preview on a connection that has not learned the answer names static, and the job's record then states what was published.
11. **The check manifest is staging.**
   It names only the item's paired segments, which stay; a check manifest a stopped job leaves behind is staging residue retained under ADR 0020, and is never deleted together with its segments.

## Consequences

- A segmented upload no longer fails at random on a store with lagging listings, and assembling what it publishes no longer depends on a listing.
- A segment replaced after the store accepted the manifest remains the window ADR 0080 decision 6 left open: the same experiment found the static tag kept the value recorded at the manifest write while the assembled read returned 409.
- Composing the manifest costs one listing of the job's own prefix, repeated only while it lags, and one read of the local file for the chunk digests.
- The engine's own size check is off for segmented runs; the per-segment pairing is a stronger check than the single size it replaces.
- Two publication shapes remain while dynamic objects stay available.
- The check costs one manifest write, one manifest read and one manifest delete per connection, and on a store that does not return static manifests the first preview of a connection can name static while its job publishes dynamic; the job's record and the next preview say so.

## Tests

- Go: chunks of a file split by the effective chunk size, a short last chunk included; a manifest composed in index order from paired segments; refused before publication: an unexpected name, a missing index after the bound, and a segment whose size or tag differs from its chunk; the bounded wait succeeding when the mock's listing lag ends within it and failing unresolved when it does not; the write declaring no content tag and the live tag checked as the MD5 of the concatenated hexadecimal segment tags; the dynamic path passing under the same lag; an overwrite of an existing large object; segment deletion of a published static object refused without the static-object proof; an existing bookmark without the value reading as `slo`; the bookmark value and the job override with their recorded origins; a readable `/info` without the entry publishing `dlo` with the `capability` origin, and an unreadable `/info` keeping `slo` rather than downgrading silently; the preview naming the kind.
- System, against SAIO: a segmented upload as a static and as a dynamic object, each downloaded and compared byte for byte, and the live object's class.
- Real-cloud journeys: the copy journey uploads its segmented file once as a static and once as a dynamic object on both clouds and downloads each back.
- For decisions 9 to 11, Go against a mock that answers the manifest read with an empty body: the check writes, reads and deletes one manifest and leaves the segments; the job publishes dynamic and its record says `dlo` from `capability`; the connection remembers, so a second job checks nothing and its preview names dynamic from `capability`; a store that returns the manifest publishes static and a second job checks nothing; a check that fails otherwise publishes nothing. Real-cloud journeys: a static choice publishes static on the Swift cloud and dynamic from `capability` on the Ceph RGW cloud, and the copy and download back succeed on both.
