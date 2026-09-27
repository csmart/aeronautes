# What an overwrite leaves behind — 26 September 2026

The oracle is the protocol and the pinned engine, measured rather than assumed, for the requirement the operator added on 26 September 2026 when they accepted the overwrite exposure: an overwritten large object must not leave orphaned fragments eating the user's space.
They also offered a reading with it, that the engine has a native way to handle this. It is half right, and which half decides whose work the accounting is.

The [upload protocol measurements](2026-09-25-upload-protocol-measurements.md) of the previous day already recorded that overwriting a dynamic manifest with a plain PUT leaves its segments behind. What was not measured there is what the engine does when the engine itself replaces a large object, which is the part the operator's reading turns on.

No application code ran. One shell probe drove the pinned engine and the protocol directly against the owned Swift, in its own namespace, removing what it created.
The evidence is retained privately in `.aeronautes-work/20260926-overwrite-orphans/` with `SHA256SUMS` and `HOST.md`: the script and its log.

- Fixture: the owned Swift, with the engine reporting `rclone v1.74.3`, chunking forced to 1 MiB so a 3 MiB object becomes a three-segment dynamic large object.
- The probe ran twice and agreed in every case. The retained log is the second run; `HOST.md` says why the first was discarded and confirms its results matched.

## The engine cleans up after itself; a publication at the live name does not

| Action on a three-segment object | Segments of the predecessor left |
| --- | --- |
| the engine replaces it with another 3 MiB object | 0 of 3 |
| the engine replaces it with a small object, so no manifest remains | 0 of 3 |
| the engine deletes it | 0 of 3 |
| a plain PUT over the manifest | 3 of 3 |

So the operator's reading is correct about the engine and does not reach this application's routes.
Neither upload route lets the engine replace the live object. The direct route publishes at the live name itself, in one request, which is the last row of that table. The staged route has the engine write into a staging prefix and then publishes the manifest itself, reusing the prefix the engine created, so the engine is never asked to replace anything.
The engine's cleanup therefore never runs for an aeronautes upload, and the accounting belongs to whichever contract lifts the overwrite refusal.

## How the engine names a run, and what that does not prove

The segments of each run were stored under a name of this shape, read from the manifest header and the segment container's listing:

    <container>_segments/<object>/<timestamp>-<random>/<part number>

Two runs of the same object shared their **timestamp** and differed only in the random component, which the [mechanism record](2026-09-27-overwrite-mechanism.md) confirms. Each manifest's `X-Object-Manifest` named its own directory.

An earlier version of this record drew a conclusion from that which does not follow, and it is corrected here rather than quietly removed, because the error is instructive. It said that a prefix the engine wrote belongs to exactly one upload, so sweeping it could not reach an object another manifest still references. The first half is true of who **created** the segments. The second half does not follow from it, and the reviewer of the contract that rested on it supplied the counterexample: the prefix is not a secret. It is published in the live manifest's own header, so any client that can read the object can read the prefix, and can then create a dynamic manifest over the same prefix, or a static manifest naming one of those segment objects, without predicting the random component and without touching the name this job selected. Deleting the set this job observed would then destroy the backing data of an object nobody here selected. The upstream large-object interface permits shared segment references, so this is a supported use of the store rather than an abuse of it.

Creation provenance is therefore not authority to delete. That distinction is ADR 0020's, and the project's remediation plan of 5 September already stated the consequence in as many words, that residue must be preserved and reclamation must not rest on a generated prefix. A recorded ownership row does not close the gap either: the store's own comment says it proves this prefix was claimed by exactly one of this application's jobs, and the live check for references from outside remains deferred.

A static manifest differs in one respect only. It lists its segments exactly, so the set is a list rather than a prefix, and no sweep is needed to obtain it. It says nothing about who else references those objects.

## What this does not establish

It does not decide what to do with a predecessor's segments; that belongs to [ADR 0081](../adr/0081-upload-overwrite-acknowledgement.md), and the choice between reclaiming only a provable prefix and sweeping every prefix is the operator's.
It measures one engine version against one store. It says nothing about whether a cluster reclaims orphaned segments by some policy of its own, and nothing about expiring segments.
It did not read back an archived version, so it adds nothing to what the previous record says about versioning.
