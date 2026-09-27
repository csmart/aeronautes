# How a large object's replacement removes its predecessor — 27 September 2026

The oracle is the pinned engine and the protocol, measured rather than assumed, for the question the operator's decision of 27 September opens: replacing a large object must account for its fragments, so what mechanism is available to do it with, and what does the engine itself do?

The [overwrite-orphan measurements](2026-09-26-overwrite-orphans.md) of the previous day established the outcome, that the engine removes its predecessor's segments and a publication at the live name does not. This record establishes the mechanism and the exceptions, because a contract has to name the request it makes and the cases it declines to make it in.

The operator also offered two readings with the decision, both of which turned out to matter: that the store's `?multipart-manifest=delete` might be the right instrument, and that the engine might not use it.

Two probes, because one fixture could not answer everything. The evidence is retained privately in `.aeronautes-work/20260927-overwrite-mechanism/` and `.aeronautes-work/20260927-overwrite-recheck/`, each with `SHA256SUMS` and a `HOST.md` that says what its probes can and cannot be trusted for. The recheck packet holds the re-measurements this record's figures come from.

- The mechanism comes from the engine's OWN request dump, with nothing interposed.
- The two failure paths and the protocol questions come from a proxy that can refuse a chosen request.
- Fixture: the owned Swift, engine reporting `rclone v1.74.3`, chunking forced to 1 MiB so a 3 MiB object becomes three segments.

## What the engine does, and when it declines to

Replacing a 3 MiB object with another, in the order the requests were made:

    HEAD the live object                     the manifest is detected here
    HEAD the container
    list the OLD segment prefix              the predecessor's set is recorded here
    PUT the three new segments                a fresh run directory
    PUT the new manifest                      the replacement is live from here
    HEAD the container                        the versioning headers are read here
    DELETE ?bulk-delete=1                     the OLD segments go here
    HEAD the live object

| Container | Predecessor's segments afterwards | Run prefixes left |
| --- | --- | --- |
| no versioning | 0 of 3 | 1 |
| `X-History-Location` set | 3 of 3 | 2 |

Those figures come from the re-measurement, not from the first trace. The first trace reused a container that still held segments from a discarded attempt, so its versioned row counted nine whole-container entries and called them the predecessor's three parts. The conclusion it drew was right and its arithmetic was not, which is the sort of thing a reused fixture does quietly.

Two things in that order are worth naming, and one thing is worth not claiming. The predecessor's set is recorded **before** anything is written, because once the manifest is replaced the set cannot be recovered. The removal happens **after** the replacement is published, so a failure before the manifest lands leaves the predecessor whole. With versioning enabled the removal is skipped entirely, and the segments belong to the retained version rather than to nothing.

What the trace does **not** show is removal after verification. In the retained `dumps/off.log` the manifest write is at line 285, the bulk delete at line 335, the final live HEAD at line 358 and the size check at line 380. So the engine cleans up between publishing and verifying. An earlier version of this record said otherwise and attributed to the engine an order that is this contract's own choice; the correction matters because corroboration and decision are different things, and only one of them needs a test.

## The instrument the store offers, and why it is not the one to use

| Request | Status | What the store reported | What an independent check found |
| --- | --- | --- | --- |
| `DELETE ?multipart-manifest=delete` on a static manifest | 200 | `Number Deleted: 4` | manifest HEAD 404, no segment remains |
| `DELETE ?multipart-manifest=delete` on a dynamic manifest | 200 | `Number Deleted: 0`, `Not an SLO manifest` | manifest HEAD 200, a GET returns all 3072 bytes and they assemble to exactly its segments, all three remain |

The right-hand column is deliberately a separate observation rather than a reading of the left. The static row's four deletions are corroborated by a HEAD that answers 404 and by an empty segment prefix, rather than believed. The dynamic row's survival is established by reading the object's body and comparing it with the bytes its segments hold, rather than by a HEAD alone: an earlier version of this record said the manifest was "still readable" on the strength of a HEAD, which is a weaker observation than the word implies.

So the operator's first reading is right for a static manifest: one request removes the manifest and its segments together. Their second reading is right too, and the reason is in the second row. On a dynamic manifest the request does nothing at all, leaves the manifest readable, and reports its refusal **inside a 200 response body** with an embedded `Response Status: 400 Bad Request`. A client that checked the status code would record a success that never happened.

That is why this record does not reach for it. A replacement has already replaced the manifest, so what is left to remove is a set of segments rather than a manifest and its segments, and `DELETE ?bulk-delete=1` removes exactly that set for either class. The lesson generalises beyond this instrument: a bulk endpoint's transport status is not its outcome, and the per-object report is the only thing that says what happened.

## What a failed upload leaves

| `leave_parts_on_error` | New segments afterwards | Live object |
| --- | --- | --- |
| false, the default | 0 of 3, removed by `DELETE ?bulk-delete=1` | absent |
| true | 3 of 3 | absent |

The fixture injected a 500 into the manifest write after the three segments had been stored. So the engine's default tidies up after its own failure, and the flag that leaves parts behind is for resuming rather than for tidying: turning it on is what creates orphans. This project sets neither value today, so it runs on the default.

## Creation provenance is not authority to delete

The run directory's random component tells you who created those segments. It does not tell you who references them now, and the two were conflated in the contract this record was written for.

The prefix is published in the live manifest's own header, so any client that can read the object can read the prefix. It can then create a dynamic manifest over the same prefix, or a static manifest naming one of those segment objects, without predicting the random component and without touching the name this job selected. The upstream interface permits shared segment references. So a set of segments observed as a predecessor's may also be some other live object's backing data, and removing it would destroy that object after this job's own replacement had been verified.

The project reached this conclusion before and from a reproduction rather than from a naming scheme: the remediation plan of 5 September records that residue must be preserved, that explicit cleanup stays force-gated, and that reclamation must not rest on a generated prefix. A maintained test already creates outside dynamic and static aliases over representative generated and staging prefixes and checks that recovery retains them. Nothing measured here weakens any of that.

Two further limits belong with it. A top-level segment list is not a complete backing-object graph: an inline entry has no remote part at all, a ranged entry references a containing object rather than a private one, and a nested static manifest names a manifest rather than its descendants. And observing that a container has no versioning configured now does not establish that no archive references those segments.

**What the operator decided about all of it, on 28 September.** This section was written as the reason a contract should not reclaim, and it was read that way. The operator's answer was that the engine's behaviour measured above IS the standard practice and that reclamation is what they asked for, so ADR 0081 decision 5 now reclaims by default under an option. Nothing in this section is retracted: the exposure is exactly as described, and a reclaimed segment that another object referenced destroys that object's backing data. What changed is that the exposure is accepted by the person entitled to accept it, on the same terms the reference implementation already ships. This record's job is to state the risk precisely, which it does; deciding it was never this record's job, and an earlier version of the contract that used this section to overrule the operator had the relationship the wrong way round.

## What this does not establish

What the engine does when the removal of the old segments is itself refused is **not measured**, for two independent reasons, either of which alone would be enough. The fault-injecting proxy interferes with the engine's own post-manifest verification in a way this session did not explain, so the predecessor that case needs is never created. And the fixture's fault selector matched a POST carrying a bulk-delete query, while the engine issues a DELETE, so the refusal it meant to inject would not have fired even against a valid baseline. The claim that the engine logs the failure and carries on is not verified here, and the contract decides that case on its own reasoning rather than by copying behaviour nobody measured.

It measures one engine version against one store. It says nothing about whether a cluster reclaims orphaned segments by a policy of its own, nothing about expiring segments, and nothing about what a listing omits on a store that is eventually consistent, which is the reason the contract treats a dynamic manifest's set as a lower bound rather than as the whole of it.
