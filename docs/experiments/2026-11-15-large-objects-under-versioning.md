# What deleting a large object does where the container keeps versions — 15 November 2026

Measured for [ADR 0120](../adr/0120-segments-go-by-default-and-versions-keep-them.md), after the operator asked whether deleting a versioned large object is handled correctly.
The [27 October measurement](2026-10-27-delete-under-versioning.md) recorded what a delete does to a plain object under each legacy mode; this one records what it does to a manifest and its segments, through the product's own deletion routes.

## Method

Target: the owned SAIO fixture on loopback, Swift 2.38, account `test`, with `versioned_writes` advertising both legacy modes.
Producer: a Go probe in `internal/systemtest`, run once for a history location and once for a versions location, kept with its output in the private packet `20261111-release-0-5-0/versioned-segments/`.
Each run made three containers of its own: a live container whose versioning header names an archive, that archive, and a container of segments.
The live container held a plain object, a static large object of two segments and a dynamic large object of three, written once each.
The probe then ran three deletions in order and listed all three containers after each, reading every listed name's kind with a HEAD:

1. the one-object deletion with segments (`DeleteManifestWithSegments`) of the static large object;
2. the deletion of the live container, without the segments option;
3. the deletion of the archive container, with the segments option.

## Results

| Mode | Deletion | Outcome | The archive afterwards | The segments afterwards |
| --- | --- | --- | --- | --- |
| history location | 1: one static large object, with segments | job complete | the manifest, still a static large object of the same length, and a zero-byte deletion marker | **both of its segments deleted** |
| history location | 2: the live container | job complete, container gone | as above, and for the dynamic large object and the plain object each: the object, the dynamic one still a manifest naming the same prefix, and a marker | the dynamic large object's three segments unchanged |
| history location | 3: the archive, with segments | job partial: 9 of 11 members deleted, 2 refused | empty | none left |
| versions location | 1: one static large object, with segments | job complete | empty: a delete archives nothing | both of its segments deleted |
| versions location | 2: the live container | job complete, container gone | empty | the dynamic large object's three segments unchanged |
| versions location | 3: the archive, with segments | job complete, container gone | gone | unchanged: no archived manifest named them |

## What this means

- **A history location keeps a deleted manifest as a manifest.**
  The archived copy is a static or dynamic large object in its own right and names the same segments, so it can be read, and the object restored from it, only while those segments exist.
- **The one-object deletion with segments does not look at versioning.**
  Under a history location it completed and deleted the two segments the archived manifest still names, which leaves a version that cannot be read.
  That is a defect: the bulk deletion's option is refused in such a container ([ADR 0119](../adr/0119-a-bulk-deletion-may-take-segments.md) decision 3) for exactly this reason, and this route was not given the same refusal.
  The two members refused in deletion 3 are those two segments: the archived manifest named them and they were already gone.
- **Deleting the live container moves what it held into the archive and deletes the container.**
  Nothing the container kept as history is deleted with it; the archive is another container and stays.
- **Deleting the archive with the segments option deletes the versions and their segments.**
  Each archived manifest is deleted as a large object, with the segments it names, and each marker as a plain object.
- **A versions location archives nothing on delete,** as the October record found, so after one write and one delete there is no version to protect.
  With an archived predecessor the name would revert to it; if that predecessor names the same segments, deleting them breaks it.
  This run did not make a predecessor.

## A second measurement, on a real Swift, through the window

The browser journey ADR 0120 asks for ran the same walk on a real Swift 2.35 on 15 November, with the objects uploaded by this application: a small file and a segmented file by the static choice, and a segmented file by the dynamic choice.
It deleted the small file, then the static large object manifest-only, then the container, and each deleted object arrived in the archive beside a marker with every segment still in place, as the table above leads one to expect.
Its last deletion, of the archive with the segments option, ended partial: 17 of 18 members deleted, 1 changed since listed.
The job's own records, kept in the same private packet, show why.

- The archive held twelve names, not six.
  Beside the version and the marker of each of the three deleted objects it held a version and a marker of each object's **staged** name, under `.aeronautes/stage/`: the store had archived each staged object when the upload removed it.
  The staged version of the small file is a second copy of its bytes.
  The staged version of each segmented file is a dynamic manifest that names the same segments as the published object, whichever kind was published.
- The member left was the staged dynamic manifest of the file published as a static large object.
  The job had deleted the archived static manifest and queued its three segments; the dynamic manifest was read with one of those segments still standing and compared when none was, and was refused as changed.

So an upload into a container that keeps history costs the archive a copy of what was uploaded, and leaves there a manifest that names the live object's segments.
And a deletion that takes segments must not read or compare a dynamic manifest while it is deleting segments that manifest may name.

## Limits

One Swift build, the SAIO fixture, with `versioned_writes` only; modern object versioning was not measured.
Each object was written once, so no run had a predecessor in a versions location or two versions sharing segments.
The archived manifests' bodies were not read back; their kind and length come from a HEAD of each.
