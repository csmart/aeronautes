# ADR 0096 — a copy skips a destination only when it is a full-fidelity replica

**Status:** Accepted on 31 October 2026, to be implemented behind the gate for the 0.3.0 copy-overwrite step of the [road to 1.0.0](../audits/2026-10-27/road-to-1.0.0.md), discharging L06/P17 replication fidelity.
Nothing here returns a capability: copy stays deferred under [ADR 0052](0052-v1-capability-gate.md) until the operator's word, so none of this is reachable in a built app and the smoke journey still asserts copy is deferred.
**Scope:** the executor's skip decision for an existing destination — `skippableEqual` and the `VerdictSame`/`VerdictUnknown` dispatch in `internal/transfer/copy/executor.go`.
The planner's preview counts (`internal/transfer/plan/plan.go`) are out of scope and stay listing-based.
No new caller state is required: a preview collision already carries its confirmation, baseline and admission, and a `Same` object gains only a possible refusal, which needs neither (decision 5).

## The requirement, and what existed when this was written

P17 (the [Linux finish line](../audits/2026-09-05/linux-finish-line.md) L06) requires that metadata, content headers, expiry and link targets are never reported as equivalent replication when they differ, and that overwrite refusals are preserved.
The finish line deferred L06 because equality is a property of copy and mirror, and copy was deferred; the 0.3.0 overwrite work ([ADR 0087](0087-copy-overwrite-on-upload-terms.md)) now builds copy, so L06 applies to it.

Equality already has two homes.
The planner (§28.4) decides a preview verdict from the listing: two plain objects are `VerdictSame` when their size and ETag match, a static large object pair is `VerdictSame` on an identical manifest aggregate, and a zero-byte, possible-manifest or unequal-manifest pair is `VerdictUnknown`.
The executor then acts: a `VerdictSame` item is recorded as skipped with no further reading, and a `VerdictUnknown` item is resolved by `runUnknown`, which HEADs both sides and skips only when `skippableEqual` proves content equality (plain size+ETag, or the destination fingerprint plus live logical length for a manifest).

Both equality tests prove **content** equality and nothing more.
Two objects with identical bytes but a different content type, a different expiry, different user metadata, or — for a symlink — a different target are content-equal and are reported equivalent, so the copy skips them.
That is the P17 gap: a skip reported as "the same" leaves the destination a different object from the source, and says so in the preview count.

§27.4 already names the set that makes a copied object a faithful replica.
When a copy transfers, it carries the source's content type, content encoding, content disposition, expiry (`X-Delete-At`, stored absolute), user metadata (`X-Object-Meta-*`, replace-all) and, for a symlink, the link target and account; the destination keeps its own generated large-object manifest, never the source's.
A skip that leaves any of those differing contradicts the replica §27.4 writes on a transfer.

## Why full fidelity, and why only at execution

The operator decided on 31 October 2026 that a copy skips a destination only when it is a full-fidelity replica, including plain objects, and that the check runs at execution rather than in the preview.

Execution is where the fidelity set is affordable.
The preview streams a namespace and must not HEAD every listing row (§15); its counts are advisory, not a snapshot (§28.4), so it keeps deciding from the listing and may still count a content-equal pair as `Same`.
The executor already reads an authoritative HEAD of each object it is about to act on (the `runUnknown` resolution, and the §27.4 metadata apply on a transfer), so comparing the fidelity set there adds no reading the executor was not already going to do for that object — it adds the reading only for a `VerdictSame` pair, which the executor previously skipped on the listing alone.
Putting the guarantee at execution means the preview may say `Same` for a pair the copy then treats as different; that is the advisory-preview contract §28.4 already states, and the authoritative decision is the one that moves — or declines to move — the data.

A difference is not a new outcome; it reaches the operator along the path its preview verdict already put it on.
An object the preview presents as a collision — `Changed` or `Unknown`, the names `confirmCopyOverwrite` offers for acknowledgement — reaches the overwrite path, where the fidelity check means a difference is refused when the operator did not acknowledge replacing it and replaced, with the source's fidelity applied, when they did.
An object the preview counts `Same` is never offered for acknowledgement; ADR 0087 decision 2 forbids capturing a baseline after the answer and decision 4 keeps the acknowledged collision set immutable, so it cannot be acknowledged or replaced in this run — at execution it either skips, as a full-fidelity replica, or refuses.
Either way overwrite refusals are preserved: a fidelity difference the operator never acknowledged refuses rather than silently rewrites a live object's metadata or expiry.

## Decisions

1. **A skip requires a full-fidelity replica.** The executor skips an existing destination only when it proves content equality (§28.4, unchanged) **and** the §27.4 carried set matches: content type, content encoding, content disposition, expiry (`X-Delete-At`), and the user metadata set. A destination's own large-object manifest is not compared, because §27.4 gives the destination its own.
2. **Link identity is part of the replica.** A symlink is equivalent only to a symlink with the same target and target account; a symlink is never skipped against a non-symlink or a different-target link. Because the authoritative following HEAD does not expose the link, a symlink source is routed to the symlink commit, which reads the link with `?symlink=get` and compares at its own fixed point rather than being skipped on the followed target's content.
3. **The executor's own fingerprint is not part of the comparison.** The reserved `X-Object-Meta-Aeronautes-*` fingerprint §28.4 writes on a destination is excluded, because the destination legitimately carries it and the source does not; comparing it would make every stamped destination compare unequal and never skip.
4. **A fidelity difference follows the path its preview verdict already set.** An object the preview presents as a collision (`Changed` or `Unknown` — the names `confirmCopyOverwrite` offers for acknowledgement) reaches the overwrite path, so the fidelity check there refuses it when unacknowledged and replaces it, with the source's fidelity applied, when acknowledged, exactly as ADR 0087 routes a changed destination. An object the preview counts `Same` is never offered for acknowledgement and acquires no baseline (ADR 0087 decisions 2 and 4), so it is never replaced in this run; decision 5 gives its outcomes.
5. **A `Same` object is resolved at execution by a skip-or-refuse check that needs no admission.** Because the fidelity set is not in the listing, a `Same` item no longer skips on the listing alone; the executor HEADs it and either skips it, as a full-fidelity replica, or refuses it — for any fidelity difference, or a destination that changed or vanished since the advisory plan. It never enters the acknowledged-overwrite path, the confirmation, the collision set, the planned or admitted accounting, or the create path: a skip and a refusal are both valid outcomes for an unadmitted item, so a `Same` object carries no new confirmation, admission or membership obligation, and a vanished destination refuses rather than creating an object the operator never planned. A refused `Same` is counted in `OverwritesRefused` and recorded with its difference as the reason, so ADR 0087 decision 8's single count still tells the kinds apart by the item record. `Unknown` objects keep their existing HEAD resolution, now fidelity-aware. The HEAD of each `Same` object is the one reading this record adds, accepted for the fidelity guarantee; the preview is unchanged.
6. **The preview is unchanged and advisory.** The planner keeps deciding its counts from the listing (§28.4, §15); it is not required to HEAD content-equal rows to make its `Same` count exact, and it may differ from the executor's authoritative decision.

## The behaviour oracle

- §28.4 for content equality (`EqualStrong`/`Different`/`Unknown`, its evidence, and the advisory preview), unchanged by this record.
- §27.4 and §17.1's preserved-header replay set for what a copy carries and therefore what a replica is: content type, content encoding, content disposition, expiry, user metadata, and link target/account, the destination keeping its own generated manifest.
- ADR 0087 for the acknowledged-overwrite path a preview collision takes (refuse when unacknowledged; replace at the fixed point when acknowledged), decision 2's rule that no baseline is captured after the answer, decision 4's immutable collision set, and decision 8's two kinds of refusal that one `OverwritesRefused` count keeps distinct in the item record.
- §15 for the preview's never-HEAD-every-row bound, which this record keeps by placing the fidelity check at execution.
- ADR 0052 for the capability gate copy stays behind.

## What this record does NOT discharge

Mirror stays deferred under [ADR 0020](0020-recovery-containment.md); making a destination match a source by syncing already-present metadata is a mirror property this record does not build.
There is no metadata-only transfer mode: a fidelity difference re-copies through the overwrite path rather than issuing a bare metadata POST.
The preview's `Same` count is not made exact and is not a fidelity claim; only the executor's authoritative decision is.
A `Same` object whose fidelity differs refuses and is not made faithful in this run: by the operator's execution-only choice no acknowledgement path reaches it, so bringing it into fidelity needs a metadata edit, a re-run whose fresh preview sees the changed name, or a future preview-fidelity mode that is not built here.
The capability stays gated: this record changes what a copy would do if it ran, not whether it runs.
