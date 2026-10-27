# What a DELETE does under each container versioning mode — 27 October 2026

Measured for [ADR 0092](../adr/0092-exact-object-deletion.md), whose dialog must tell the operator what deleting an object does to the content at that name.
The repository held no record of this before; the behaviour below is a measurement against a real Swift, not a reading of its documentation.

## Method

Target: the owned SAIO fixture on loopback, Swift `2.38.2.dev23`, account `test`, through the TempAuth v1 endpoint, with `versioned_writes` advertising `x-versions-location` and `x-history-location`.
Producer: a standard-library Python probe, retained beside its output and the cluster's `/info` document with a `SHA256SUMS` manifest in the private packet `20261027-release-plan/delete-under-versioning/`.
For each of three containers, one with no versioning, one with `X-Versions-Location` and one with `X-History-Location`, each pointing at its own fresh archive container:

- **Case A**, an object written once and then deleted; the name and the archive are read after the delete.
- **Case B**, an object written twice, the second write longer than the first so the two contents differ in length and entity tag, then deleted three times; the name and the archive contents are read after every delete.

Every object body in the archive was read back, not counted, so what the archive holds is stated from content.
All containers were purged afterwards.

## Results

| Mode | Case | DELETE | The name afterwards | The archive afterwards |
| --- | --- | --- | --- | --- |
| none | A: written once | 204 | 404 | no archive |
| none | B: written twice | 204, then 404, 404 | 404 after the first delete | no archive |
| versions location | A: written once | 204 | 404 | empty: the deleted content was not archived |
| versions location | B: written twice | 204 | holds the **first** content again, by entity tag and by body | **empty**: the archived predecessor moved back to the name, and the second content exists nowhere |
| versions location | B: second delete | 204 | 404 | empty |
| versions location | B: third delete | 404 | 404 | empty |
| history location | A: written once | 204 | 404 | the deleted content, plus a zero-byte deletion marker |
| history location | B: written twice | 204 | 404 | the first content, the second content and a zero-byte marker |
| history location | B: second and third deletes | 404, 404 | 404 | one more zero-byte marker per delete, although each delete answered 404 |

## What this means for the dialog

- **No versioning.** The content at the name is destroyed by the delete; nothing holds it afterwards.
- **A versions location.** This is the case an operator will misread.
  The archive keeps copies made on **overwrite**, never on delete: deleting the current object destroys its content, and if the archive holds a predecessor that predecessor is moved back to the name and leaves the archive.
  A delete therefore reverts the name to its previous version while destroying the version the operator was looking at, and a second delete destroys the restored one.
  The dialog must say that the current content is destroyed and, when a predecessor is archived, that the name will revert to it.
- **A history location.** The current content is moved to the archive and a deletion marker is written there; the name stops resolving.
  The content is recoverable from the archive by an operator who knows where to look, and the dialog may say so, naming the archive container.
  Deleting a name that no longer exists still writes a marker into the archive even though the request answers 404.

The inspector already knows the container's mode and archive (`versioningMode` and `versioningArchive` on the container detail), so the dialog can state the right sentence without another request.

## Limits

One Swift build, the SAIO fixture, with `versioned_writes` only; the modern `object_versioning` mode (`X-Versions-Enabled`) was not measured and [ADR 0054](../adr/0054-versioning-preflight-authority.md) keeps it read-only.
Case B used two versions; a longer history under a versions location would revert one step per delete, which this measurement did not walk beyond the second delete.
Nothing here measures a delete racing a concurrent write; [ADR 0085](../adr/0085-reobserved-destructive-identity.md) states that exposure.
