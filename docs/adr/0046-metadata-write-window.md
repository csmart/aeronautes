# ADR 0046 — What a completed metadata save may claim

**Status:** Proposed on 14 September 2026; nothing in it is implemented yet.
**Scope:** What a completed object metadata save reports, within §18 and `K-META-003`.
This refines the reporting side of an already decided limit; it does not reopen the limit itself.
It is a bounded part of P26, not P26 itself; the open remainder is named below.

## Context

`Session.UpdateObjectMetadata` reads the object, compares the caller's base fingerprint against that read, builds Swift's replace-all header set from it, and writes.
§18 and `K-META-003` already decide that this is best-effort conflict detection and must not be presented as an atomic compare-and-swap.

The [metadata write window record](../experiments/2026-09-14-metadata-write-window.md) measures what happens when the interval between the read and the write is actually used.
A second editor's complete save landing there is destroyed, and the operation reports `applied` with no mismatch.
Two healthy controls pass in the same run, including one proving the existing fingerprint check still refuses a change that lands before the read.

Three facts bound what any correction can be:

1. `verifyObjectOutcome` asserts that the state after the write equals the intended edit, which a successful overwrite satisfies exactly, so the verify read cannot distinguish the two.
2. `If-Match`, `If-None-Match` and `If-Unmodified-Since` are all ignored on an object POST against the fixture, measured, so there is no conditional write to make the comparison atomic.
3. The entity tag is the content digest and does not change on a metadata POST, so it could not carry the condition even if one were honoured.

## Decision

A completed metadata save states what it establishes and what it does not.

The unqualified `applied` outcome is not retained for a write that could have landed inside the unprotected interval.
A save that succeeds reports that the intended state was written and verified, and reports alongside it that this does not establish that no concurrent edit was overwritten.
The audit entry and the editor carry the same qualification as the service result; they must not present a completed save as evidence of exclusivity.

The preflight fingerprint comparison is unchanged.
It refuses a change that lands before the read, it is the only detection available, and nothing here weakens it.

The interval between the read and the write is narrowed where that costs nothing, and the narrowing is not described as a bound.
A shorter unprotected interval is still unprotected.

## What this does not decide

It does not make concurrent metadata editing safe, and it does not add detection, because none is available.
It does not change the deliberate empty-base override reached through the conflict banner, nor the separate bridge-reachability of that override, which needs a server-issued one-shot capability.
It does not examine the container edit path, which shares this shape.
It closes no finding, grants no release acceptance and leaves the valuable-data hold unchanged.

## Consequences

An operator who saves metadata while another editor is saving the same object is told that their write landed and that it may have replaced work they never saw.
That is worse news than the product gives today, and it is true, which the current report is not.
