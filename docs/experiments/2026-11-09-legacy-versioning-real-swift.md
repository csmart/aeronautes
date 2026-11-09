# The legacy versioning matrix on a genuine Swift — 9 November 2026

Measured for P26, whose acceptance asks that the legacy archive paths have real Swift evidence.
The [27 October record](2026-10-27-delete-under-versioning.md) measured the SAIO fixture with a raw probe, and the delete dialogs ([ADR 0092](../adr/0092-exact-object-deletion.md)) and the overwrite question ([ADR 0081](../adr/0081-upload-overwrite-acknowledgement.md)) state that matrix as fact.
This run shows a genuine OpenStack Swift agreeing with it, through the product's own routes rather than a raw probe.

## Method

Producer: `TestLegacyVersioningOracle` in `internal/swiftcontrol/realcloud_versioning_test.go`, run with `make oracle CLOUDS=<clouds.yaml> CLOUD=swift` from the repository root at this commit.
Target: the `aeronautes-swift` backend of the real-cloud oracle, an OpenStack Swift `2.35.4` whose `/info` allows `x-versions-location` and `x-history-location`, reached through the product's Keystone v3 path.
For each mode the test creates a fresh owner container and a fresh archive container with random names, sets the mode through `Session.UpdateContainer`, the container editor's route, writes `probe-object` twice with bodies of different lengths through `Session.PutObject`, and deletes it through `Session.DeletePlainObject`, the protocol every exact-object delete uses.
After every step the name is read by HEAD and by GET, and every object in the archive is listed and read back, so what the archive holds is stated from content.
The test asserts the fixture's matrix; a store that differs fails the run.
Both runs passed, and a listing of the account afterwards showed no probe container.

## Results

| Mode | Step | The name afterwards | The archive afterwards |
| --- | --- | --- | --- |
| versions location | written twice | the second body | the first body |
| versions location | first delete | the **first** body again, which the delete route reported as the successor at the name | **empty** |
| versions location | second delete | 404; the route reported absence | empty |
| history location | written twice | the second body | the first body |
| history location | delete | 404; the route reported absence | the first body, the second body and one zero-byte marker |

The container editor's route applied each mode with `applied` and read the mode and archive back; the delete route answered `applied` with `Accepted` on every delete.

## What this means

- The real Swift behaves as the fixture did in every cell the dialogs describe: under a versions location a delete destroys the current content and reverts the name to the archived predecessor, which leaves the archive; under a history location the content moves to the archive with a deletion marker.
- The delete protocol's literal read afterwards reports the reverted predecessor as a successor, so the product sees the revert happen rather than inferring it.
- The Ceph RGW backend of the same oracle advertises no `versioned_writes`, so the test skips both modes there, as the editor offers neither.

## Limits

One real Swift build, two bodies per name, one delete under a history location.
The test runs only with credentials and never in the required checks ([CI](../CI.md#non-blocking-checks)); this record is the evidence the acceptance asked for, and the test is how to renew it.
Nothing here measures the modern mode, which neither real cloud offers; the [fixture measurement](2026-11-09-modern-versioning.md) covers it.
