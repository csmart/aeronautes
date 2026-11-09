# What modern object versioning does on the fixture — 9 November 2026

Measured for P26 against the owned SAIO fixture on loopback, Swift `2.38.2.dev23`, account `test`, through the TempAuth v1 endpoint, whose `/info` advertises `versioned_writes` (`x-versions-location`, `x-history-location`) and `object_versioning`.
The repository held no measurement of the modern mode before; [ADR 0054](../adr/0054-versioning-preflight-authority.md) refuses to rewrite a modern-versioned container into a legacy shape on the strength of what the documentation says, and this record is what the store does.
Producers: three standard-library Python probes retained beside their JSON outputs with a `SHA256SUMS` manifest in the private packet `20261109-versioning-p26/modern-versioning/`.
Every container was created with a unique suffix and deleted afterwards, versions included; the fixture holds no probe container now.

## Results

| Step | Status | What was observed |
| --- | --- | --- |
| PUT container with `X-Versions-Enabled: true` | 201 | HEAD reports `X-Versions-Enabled: True` |
| PUT the same name twice | 201, 201 | each reply carries `X-Object-Version-Id`; the name serves the second body |
| GET `?versions` | 200 | both versions listed with `version_id`, `is_latest`, bytes and content type |
| GET `?version-id=<older>` | 200 | the first body, readable by id |
| DELETE without a version id | 204 | a delete marker version appears (`application/x-deleted;swift_versions_deleted=1`), the name answers 404, both earlier versions stay listed and readable |
| POST `X-Versions-Enabled: false` with versions present | 204 | versions stay listed and readable by id |
| PUT after disabling | 201 | the listing gains a current entry with `version_id` `null`; the earlier versions stay |
| PUT again while disabled | 201 | the `null` entry is overwritten in place (same row, new length); nothing is archived |
| POST `X-Versions-Enabled: true` again, then PUT | 204, 201 | the former `null` entry gets a version id and the new write becomes the latest version |
| POST `X-Versions-Location` onto the modern container | 204 | the header lands: HEAD shows both `X-Versions-Enabled: True` and the location |
| POST `X-History-Location` onto the modern container | 204 | likewise; the history location replaces the versions location |
| Any later POST `X-Versions-Enabled: true` or `false` on that container | 400 | "Cannot enable object versioning on a container that is already using the legacy versioned writes feature." |
| Follow-up probe, history location set before any write: PUT a name the container does not hold | 201 | the first write of a new name lands and is listed as a version with an id |
| Follow-up probe, history location set before any write: PUT that name again, DELETE it, or DELETE its version by id | 412 | Precondition Failed; nothing stored, nothing removed, the version still listed |
| Follow-up probe: DELETE the container while that version exists | 409 | "Delete all versions before deleting container." |
| First probe, versions location set over five versions written before it: DELETE each version by id, then the container | 204 each, then 204 | every version deleted and the container gone, although the disable POST between them answered 400 |
| Cleanup probe, after the follow-up: POST `X-Remove-History-Location` and `X-Remove-Versions-Location` | 204 | the container is operable again: the version deletable by id, versioning disables, the container deletes |
| POST `X-Versions-Enabled: true` onto a container with `X-Versions-Location` | 400 | the same refusal: the two models exclude each other, enforced in this direction only |

## What this means

- Modern versioning keeps every version in the same container, a delete writes a marker and keeps the content, and disabling keeps what exists while later writes overwrite one unversioned current entry; re-enabling versions that entry too.
  Nothing is lost by enabling, disabling or deleting through the modern model; a version is removed only by a DELETE that names its `version-id`.
- The hazard ADR 0054 refuses is real: writing a legacy location onto a modern-versioned container is accepted by the store, and every later change to `X-Versions-Enabled` answers 400 until the header is removed again.
  What happens to the objects underneath differed between the two sequences measured: with a history location set before the first write, the container refused to replace or delete that name, by name or by version id, with 412, while a first write of a name it did not hold landed and was versioned; with a versions location set over five versions written before it, each version was deleted by id with 204 and the container with it.
  Whether the header kind or the writing order decides was not measured, so this record states both sequences and infers no rule beyond the refusal of versioning changes.
  The container editor's modern-mode refusal is a necessary guard either way, and removing the legacy header restored the follow-up's container.
- The reverse direction is refused by the store (400), so a legacy container cannot be moved to the modern model by a header alone.

## Limits

One Swift build on the SAIO fixture.
The two real clouds the [real-cloud oracle](../CI.md#non-blocking-checks) reaches advertise no `object_versioning`, so nothing here applies to them; the OpenStack Swift backend advertises only the legacy flags and the Ceph RGW backend none.
Not measured: listings with `?versions` beyond one name, copying an older version back to the name, expiring versions, or any race with a concurrent writer.
The two mixed-header sequences differ in both the header kind and when the versions were written, and nothing here separates the two causes.
