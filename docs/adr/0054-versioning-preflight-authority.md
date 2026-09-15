# ADR 0054: the versioning refusal must rest on an established mode

**Status:** Proposed. Contract only; nothing in it is implemented yet.

## Context

A container metadata edit is the one mutation v1 keeps besides object
metadata.
`MutateService.UpdateContainer` refuses to edit a container that uses
modern object versioning, because rewriting such a container into a
legacy location shape has no safe representation in v1.

That refusal is conditioned on `currentVersioningMode`, which issues its
own container HEAD and returns the empty mode on any error — a session
that cannot be obtained, a read that fails, a transient refusal.
The guard then refuses only when the mode reads exactly `enabled`, so an
empty mode admits the edit.
An unestablished mode is therefore treated as an established absence.

The [versioning preflight record](../experiments/2026-09-15-versioning-preflight.md)
reproduces the consequence through the service against the real
two-account fixture.
One failing container HEAD, with every later request passing, admits an
edit against a container the product has just declared it cannot edit
safely.
Asking for `history_location` rewrote a modern-versioned container into a
legacy shape and it read back `history_location` with the archive set,
which is the exact hazard the refusal names.
Asking for `off` applied the metadata and left modern versioning in
place, because an off intent emits only the legacy removal headers.
Two healthy controls pass in the same run: the refusal holds when the
probe succeeds, and a plain container edit applies.

No conditional write is available to close the window, and this was
measured on containers rather than inherited from the object case.
A container carries no entity tag at all, and `If-Match`,
`If-None-Match: *` and `If-Unmodified-Since` were each ignored with the
write applied.
So there is not even a value a precondition could reference.

The refusal also rests on a different read from the one the write uses.
`currentVersioningMode` issues one container HEAD and `UpdateContainer`
issues another for its own preflight, so the mode decision and the write
are separated by a second round trip.
And `X-Versions-Enabled` is not part of the editable header set, so it is
excluded from `EditableFingerprint` and the conflict check cannot see a
container becoming modern-versioned between the two reads — while that
field's own documentation claims the fingerprint covers versioning
headers.

## Decision

An unestablished versioning mode refuses.

The mode is established once, from the same container read the write is
built on, rather than by a separate probe.
When that read does not yield a mode, the edit is refused and says the
mode could not be established, rather than proceeding as though the
container were unversioned.

`X-Versions-Enabled` joins the editable header set so the conflict check
covers it, and the field documentation stops claiming coverage it does
not have.

## Consequences

An operator whose cluster briefly refuses a container HEAD is told the
edit was not attempted, instead of receiving a verify mismatch after a
write that should never have been issued.

The refusal becomes unconditional on the modern-versioning path rather
than conditional on a probe succeeding, so it no longer fails open.

This is a refusal rather than a prevention, and the record says so:
the window between reading and writing cannot be closed on this
protocol, so a container that becomes modern-versioned after the read is
still edited.
That residue is narrowed by reading the mode from the write's own
preflight rather than from an earlier request, and it is not eliminated.

The legacy transfer, delete and cleanup portions of this item are
deferred with those routes and are not addressed here.
Whether a legacy rewrite destroys existing version history on a
production cluster is not established: the record measures the header
shape, not the fate of stored versions.
