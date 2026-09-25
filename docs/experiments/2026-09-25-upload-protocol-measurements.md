# Upload protocol measurements — 25 September 2026

The oracle is the protocol, measured rather than assumed, for the three upload questions the next release opens: whether a client can bind a publication to the bytes it sent, what an overwrite does to what was there, and what an overwrite does to a large object's segments.
The operator decided on 25 September 2026 that the next release is upload and that overwrite has to be supported, so these facts decide what its contracts can promise.

No application code ran. Three shell probes spoke the protocol directly against the owned Swift, each in its own namespace, each removing what it created.
The evidence is retained privately in `.aeronautes-work/20260925-upload-overwrite/` with `SHA256SUMS` and `HOST.md`: the three scripts, their logs, and the fixture's `/info`.

- Fixture: SAIO, Swift `2.38.2.dev23`, one storage policy `1replica`, SLO available with a thousand-segment manifest limit, `versioned_writes` advertising `x-versions-location` and `x-history-location`.
- Every figure below is a status code or a body read from `saio-probe.log`, `saio-etag-probe.log` or `saio-overwrite-safety.log` in that packet.

## A declared digest is enforced, and a rejected write leaves nothing behind

| Request | Status | State afterwards |
| --- | --- | --- |
| PUT with a correct `ETag` | 201 | the object holds the sent bytes |
| PUT with a wrong `ETag`, body of the same length | 422 | the name does not exist |
| PUT with a correct `ETag` and a same-length corrupted body | 422 | the name does not exist |
| PUT with `ETag` and `If-None-Match: *` together, absent name | 201 | the object holds the sent bytes |

So the store will not publish bytes that disagree with the digest the client declared, and a rejected publication leaves no object at that name.
This is stronger than [DESIGN §65.11](../DESIGN.md) anticipated for a route that publishes at the live name in one request: it assumed such a route could only report an unresolved effect with retained residue, because a fingerprint dropped from the PUT cannot be added afterwards through a POST that rewrites headers.
The fingerprint does not have to be added afterwards. It can be declared in the request.

An SLO manifest is verified the same way, per segment: a manifest naming a segment whose `etag` does not match is refused with 400 and the body `Etag Mismatch`, and the manifest does not exist.
That is recorded as evidence that the store is where a digest belongs, not as a decision to change which manifest kind an upload publishes.

## Create is enforceable; replace-if-unchanged is not

| Request | Status | Body afterwards |
| --- | --- | --- |
| `If-None-Match: *` on an existing name | 412 | unchanged |
| `If-None-Match: *` on an absent name | 201 | the sent bytes |
| `If-Match` with the object's current entity tag | 201 | the sent bytes |
| `If-Match` with a wrong entity tag | 201 | **the sent bytes** |
| PUT with no precondition on an existing name | 201 | the sent bytes |

`If-None-Match: *` is honoured, so a create can be made race-free: the first writer wins and the second is told so.
`If-Match` is ignored, so a replace cannot be made conditional on the object the operator looked at.
This is the same result [ADR 0046](../adr/0046-metadata-write-window.md) measured for an object POST and [ADR 0054](../adr/0054-versioning-preflight-authority.md) measured for a container write, now measured for an object PUT.
It is the fact behind [ADR 0020](../adr/0020-recovery-containment.md)'s refusal of a transfer overwrite "until an enforceable protocol is implemented": for replacement there is no such protocol on this store, and nothing in this record supplies one.

## What a refused or interrupted overwrite does to the prior object

The overwrite path drops the conditional create, so these are the cases that decide whether an overwrite can destroy data by accident rather than by intent.

| Request against an existing object | Status | Body afterwards |
| --- | --- | --- |
| overwrite with a wrong declared `ETag` | 422 | the prior bytes |
| overwrite with a correct declared `ETag` | 201 | the new bytes |
| overwrite abandoned mid-request, described below | no response observed | the prior bytes |

The first two are complete observations: the store answered, and the prior object was read back afterwards.
A digest the body contradicts is refused and the object that was there is still there.

The third is weaker than it looks, and this record states it exactly rather than as a guarantee.
The probe sent a PUT declaring `Content-Length: 4096` with one byte of body and no declared entity tag, and gave up after three seconds; `curl` exited 28, its timeout, so **no HTTP response was observed at all**, and a later read returned the prior bytes.
That is one instance of an incomplete request not replacing.
It is not evidence about a request whose body the store received completely and whose response was then lost, which can still have replaced, and which [ADR 0020](../adr/0020-recovery-containment.md) and the item outcome contract deliberately keep as uncertain.
A transport failure and a confirmed refusal are different observations and this record does not merge them.

So two exposures remain rather than one.
Another writer's object landing between the operator's preflight and the PUT is replaced silently, because the precondition that would catch it is ignored.
And a request whose outcome is not observed leaves what is at that name unknown until it is read again.

## What an overwrite does under versioning, and to a large object

On a container created with `X-History-Location` pointing at an archive container, two writes to one name left the live object holding the second body, the archive container's object count at one, and its listing carrying the archived name with a timestamp suffix.
That is listing evidence that an archive entry appeared.
The probe did not read that entry's bytes or restore it, so this record does not claim that the previous version is recoverable, only that the store recorded one where the mode was set.
Whether the mode observed before a write is still the mode at the write is a separate question that [ADR 0054](../adr/0054-versioning-preflight-authority.md) already keeps open.

Overwriting a large object's manifest with a plain object succeeded and **left every segment listed in the segments container**.
The segments are then referenced by nothing the application knows of, they still consume the account's quota, and they are the class of object [ADR 0020](../adr/0020-recovery-containment.md) forbids deleting on inference, because an outside manifest may reference the same prefix.

## What this establishes and what it does not

It establishes the protocol facts above on this fixture at this version, and it supplies the measured basis for [ADR 0080](../adr/0080-upload-content-binding.md) and [ADR 0081](../adr/0081-upload-overwrite-acknowledgement.md).

It establishes nothing about the application: no upload route ran, and the routes' present behaviour is stated in those records from their source rather than from this one.
It does not measure a deployed cluster, a second storage policy, a modern-versioned container, or an SLO of more than one segment.
It measures no case where the store received a complete body and its response was lost, so it supports no claim about that case.
It does not lift any refusal by itself, and it does not close finish-line item L05 or the valuable-data release hold.
