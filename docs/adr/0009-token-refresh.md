# ADR 0009 — Token lifecycle: bounded batches, fresh children, failure-driven reauth

**Status:** Accepted; refresh-before-spawn implemented 2026-08-30 (§65.8 item 1) (2026-08-28)
**Detail:** `DESIGN.md` §26.2; requirements K-SEC-001, K-XFER-001, K-XFER-013

## Context

rclone children hold only a pre-authenticated storage URL + token (never the durable key), and a
token cannot refresh inside a running one-shot child. Stock rclone has no drain mode
(`--max-duration` HARD kills transfers; SOFT lets in-flight objects run past the deadline).

## Decision

- **Preferred:** split long jobs into bounded batches with a fresh child (fresh token) per batch,
  and/or require remaining lifetime to exceed the batch's largest object at a conservative
  throughput floor. Lifetime is learned in priority order: server-provided expiry
  (`X-Auth-Token-Expires` where the cluster sends it — seam-spike item 1), per-bookmark configured
  lifetime, else a bounded maximum child duration.
- **Fallback:** on in-flight expiry the child fails fast (small retry budget), Aeronautes
  re-authenticates with the key it kept, reconciles destination state, and spawns a new child.
  Expiry is child-failure → reauth → reconcile → **new** child; the durable key never enters any
  child (K-XFER-001).
- Restart rebuilds the work set from **unverified** objects only (§28.4); a partially-uploaded
  large object's resume behaviour is unknown until the seam spike settles it and is not promised
  in the UI.
- A single object that cannot move within the assumed lifetime at the floor rate fails at
  preflight with a clear explanation, never mid-flight.

## Consequences

- Long jobs make forward progress across token lifetimes without credential exposure.
- Batch planning is a scheduler concern from the start; orphaned-segment reconciliation (§26.4)
  covers the interrupted-batch case.
