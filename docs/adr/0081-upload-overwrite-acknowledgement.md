# ADR 0081: upload replaces an existing object only on an acknowledged, bounded overwrite

**Status:** Proposed on 25 September 2026, revised the same day after review. The operator's requirement is recorded: upload must be able to replace an existing object, in their words "we have to support overwrites, that's legitimate use of object storage systems, and if there is verison turned on ... it will replace and increment the version. am i missing something?". **What this record proposes beyond that requirement is not yet accepted**: the operator asked a question, and the limits below are the answer to it. The residual exposure in "The risk this proposes to accept" needs the operator's acceptance, now that those limits are stated, before this decision is implemented.
**Scope:** The upload route's publications in `internal/transfer/local` and their admission in `internal/app`. It proposes to lift the [ADR 0020](0020-recovery-containment.md) transfer-overwrite refusal for that route alone. Mirror, recovery reclamation and every other ADR 0020 refusal are untouched.

## What happens today

Upload never replaces anything, and not by accident. Every live publication carries `If-None-Match: *`, so the store refuses when the name exists and the first writer wins. The preflight also reports a live name as a rejecting problem of kind `exists` whose detail reads "already live at the destination; v1 upload never overwrites". That conditional is what satisfies ADR 0020's refusal of a transfer overwrite "until an enforceable protocol is implemented".

## What the protocol allows, measured

The [upload protocol measurements](../experiments/2026-09-25-upload-protocol-measurements.md) of 25 September 2026 settle part of this and deliberately leave the rest open.

- **Replace-if-unchanged is not available.** `If-Match` carrying a wrong entity tag was ignored and the write applied. There is no precondition on this store that binds a replacement to the object the operator looked at, so ADR 0020's enforceable protocol does not exist and this record does not supply one.
- **Create-if-absent is available.** `If-None-Match: *` was honoured with 412 and the body unchanged, so every publication that is not an acknowledged overwrite stays race-free.
- **A corrupted overwrite is refused and the prior object survives it.** An overwrite whose declared `ETag` disagreed with the body was answered 422 and the prior bytes were read back afterwards.
- **An unobserved outcome stays unknown.** The one abandoned-request observation produced no HTTP response at all, so it says nothing about a request whose body the store received completely and whose response was then lost. That case can still have replaced, and this record keeps it uncertain rather than claiming otherwise.
- **A versioned container recorded an archive entry.** On a container carrying `X-History-Location`, an archive entry with a timestamped name appeared after the second write. Its bytes were not read, so what is recorded is that the store noted a previous version, not that one is recoverable.
- **Overwriting a large object's manifest orphans its segments.** Every segment stayed listed in the segments container, referenced by nothing the application knows of.

## Decision

1. **An overwrite happens only when the operator asked for one.** `UploadRequestDTO` gains an acknowledgement field. Without it the route behaves exactly as it does today: conditional create, `exists` rejects the preflight, and a collision at commit is a conflict.
2. **The acknowledgement that authorises the replacement is raised by the backend, not supplied by the page.** As in [ADR 0073](0073-selected-resource-acknowledgement.md), the field in the request protects against an operator's accident, not against a hostile webview, because a compromised page can set it itself. Before dropping any conditional, the backend raises one native confirmation through the same seam shape as the public-ACL confirmer, wired by a package function under [ADR 0011](0011-typed-service-surface.md) and never reachable as a bridge action. No confirmation, no replacement.
3. **The confirmation names the account the backend resolved, not only the count.** ADR 0073's counterexample survives otherwise: a page can connect account B while displaying account A, every backend check legitimately binds B, and a same-named container with the same number of collisions produces an identical question. So the confirmation names the bookmark and its account suffix as the backend resolved them, the container, and the prefix, in the shape the download chooser's title already uses. The upload picker is titled the same way, which it is not today: `PickUploadFiles` still titles it "Choose files to upload".
4. **The answer authorises one plan, not a later one.** The confirmation is bound to the collision set the preflight computed and to the captured authority it was computed under. A start whose scope, account, generation or collision set differs from the one confirmed is refused rather than carried by the earlier answer.
5. **A collision whose live object is a large object is refused, acknowledged or not.** Replacing its manifest leaves its segments listed and referenced by nothing, and nothing in this release can remove them safely: they are exactly the class ADR 0020 forbids deleting on inference. The preflight reports those names under their own problem kind, and they stay rejecting.
6. **The replacement is still bound to its content.** The acknowledged publication drops `If-None-Match: *` and keeps the declared `ETag` of [ADR 0080](0080-upload-content-binding.md) wherever that route declares one, so a corrupted overwrite is answered 422 with the prior object intact, as measured. On a staged large object the manifest PUT carries an empty body, so an entity tag declared there would constrain that empty body and not the segments it names; none is declared, and the binding is the staged content comparison ADR 0080 decision 3 makes.
7. **What was observed is recorded as what was observed.** Before the publication the commit reads the live object and records its entity tag, length and last-modified as the **observed predecessor**. In the race this record admits, the object replaced may not be that one, so the ledger names what was seen rather than claiming to name what was replaced.
8. **The confirmation describes the container's observed versioning mode without promising recovery.** It says which mode the backend observed and which archive container it named, and it does not state that a previous version will be recoverable: the mode observed before a write is not bound through the write, which [ADR 0054](0054-versioning-preflight-authority.md) already keeps open, and no archive entry's bytes have been read by anything in this project. A container whose mode is unknown or unsupported is refused for overwrite, as ADR 0054 refuses editing it.
9. **Nothing else changes.** A name with no collision is published exactly as today. An unacknowledged job is published exactly as today. Mirror stays refused under ADR 0020 independently of this decision and of the capability gate.

## The risk this proposes to accept

Between the preflight that showed the operator a collision and the PUT that replaces it, another client can write that name, and this route will replace that write without noticing. The store offers no precondition that would catch it. Separately, a publication whose response is lost leaves what is at that name unknown until it is read again.

Neither exposure is reduced by any measurement in this record, and neither is carried by the operator's question of 25 September, which asked for overwrite support and ended by asking what it was missing. **This record is the answer to that question, and the acceptance is owed before implementation.**

## Required proof before implementation is accepted

Tier 1 under [DESIGN §65.12](../DESIGN.md), on the mock and the owned Swift, through the public service.

- An unacknowledged upload onto a live name refuses as it does today, at preflight and at commit, with the prior object unchanged.
- An acknowledged upload with no confirmation raised, a confirmation answered no, and a confirmation whose raising fails, each refuse with the prior object unchanged and nothing recorded as applied.
- An acknowledged and confirmed upload replaces, records the observed predecessor's entity tag, length and last-modified, and reports the new object verified.
- Two accounts holding a same-named container with the same number of collisions: the confirmation names the account and bookmark the backend resolved, and an answer given for one does not authorise the other.
- The connection, generation or collision set changes between the confirmation and the start: the start refuses rather than proceeding on the earlier answer.
- On a container carrying a legacy versioning mode, the confirmation names that mode and its archive container, and an archive entry exists afterwards; on a container whose mode is unknown or unsupported, the overwrite is refused.
- An acknowledged upload whose live target is a large object is refused at preflight and at commit, with the manifest and every segment unchanged.
- An acknowledged upload whose body is altered in flight is refused by the store, and the prior object is unchanged.
- A name with no collision in an acknowledged job still publishes conditionally: a racing create of that name between preflight and commit is refused as a conflict, not replaced.

Retained mutants: the acknowledgement checked without the native confirmation; the confirmation raised after the conditional is dropped rather than before; the confirmation naming only the container and the count, which must fail the two-account case; the answer reused for a changed plan; the large-object refusal applied at preflight only; the conditional dropped for every name in an acknowledged job rather than for the acknowledged collisions; the observed predecessor recorded after the replacement rather than before.

The native journey that shows what the confirmation actually renders remains the operator's, as the [0.1.0 test plan](../audits/2026-09-24/0.1.0-test-plan.md) records for ADR 0073's dialogs.

## What this does not establish

It does not close finish-line item L05, lift the capability gate, or lift the valuable-data release hold. It does not change Copy, Mirror, deletion or recovery reclamation. It does not establish behaviour on a modern-versioned container, whose editing ADR 0054 still refuses, and it makes no claim about a deployed cluster.
