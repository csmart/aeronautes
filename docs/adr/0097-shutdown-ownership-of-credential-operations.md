# ADR 0097: shutdown ownership of credential publication and cleanup

**Status:** Accepted.
It records why shell shutdown does not join `BookmarkService`: shutdown can interrupt an unfinished, unacknowledged `Save` or `Delete`, but doing so cannot lose valuable data — the bookmark row changes only in a single all-or-nothing publication, so an interrupted operation leaves an intact committed state, either the prior one or the fully committed new one, plus durable journal records. The in-flight wait is bounded by [ADR 0055](0055-bounded-credential-operations.md), and [ADR 0053](0053-synchronous-writer-shutdown-ownership.md) settles the same question for the metadata writes.

## Context

`ShutdownWork` (`internal/app/shutdown.go`) drains the job runner, the Find service and the synchronous metadata writer (`MutateService`), after which the shell closes the database and releases the instance lock.
It does not join `BookmarkService`, whose `Save` and `Delete` publish and remove per-bookmark credentials in the OS keychain and write the bookmark row to SQLite.
The [register reconciliation](../audits/2026-10-18/register-reconciliation.md) (SEC-02) left open whether it should, and that open clause is part of the durability residual.

## Decision

Shutdown does not join `BookmarkService`. It can interrupt an unfinished `Save` or `Delete`, but interrupting an unacknowledged credential operation cannot lose valuable data.

`Save` records a `prepared` journal entry, releases the writer and coordinator, and waits on the native keychain `Put` (`internal/bookmarks/bookmarks.go:182,200`); only if that `Put` succeeds does it make the final publication that changes the bookmark row (`bookmarks.go:214`).
So the valuable bookmark row changes only in a single all-or-nothing publication under `synchronous=FULL`: a shutdown that interrupts the operation before that publication leaves the prior committed state intact — the old bookmark and its published key for a replacement, or no bookmark at all for a new one — never a torn or half-applied row (see the [durability-limits record](../experiments/2026-11-02-durability-limits.md)).
If the interruption instead falls after that publication has committed, the new state stands, equally intact: a commit can succeed even when its response does not, which `internal/bookmarks/publication.go` handles by reporting the operation unresolved without compensating a candidate that may already be published.
So an unacknowledged operation leaves either the prior or the fully committed new state, and the missing acknowledgement does not say which; what is guaranteed is an intact committed state and retained recovery records, not that the old selection survives.

What an interrupted or failed operation leaves behind is a durable journal record and, possibly, an unused key in the OS keychain — not lost valuable data.
The keychain item is re-enterable and is not the valuable SQLite state (K-SEC-003).
A completed `Delete` retires the old reference, and a later `Cleanup` pass reconciles `retired` rows once the recording backend is active and no bookmark, prepared write, acquisition lease or admitted job still holds the key.
A failed `Put`, by contrast, returns while leaving its reference `prepared` (`bookmarks.go:209`), and the ordinary retired-only `Cleanup` pass does not reconcile that prepared residue — a known limitation, stated here rather than papered over, and still not valuable-data loss.

The native call is bounded only for the caller: [ADR 0055](0055-bounded-credential-operations.md) bounds the wait at `awaitKeychain` (`internal/secrets/keychain.go:60`), not the native operation's lifetime, so an abandoned `Put` continues until the Secret Service answers or the process exits.
Joining that uncancellable call at shutdown would delay the drain without protecting any valuable data, so `ShutdownWork`'s scope is correct as it stands.

## Consequences

- No code changes: `ShutdownWork` joins the owners of background work whose unfinished state would otherwise be lost; `BookmarkService` is not one, because an interrupted credential operation loses no valuable data.
- The clause the register named is resolved: shutdown ownership of credential publication and cleanup is settled as an interruptible, unjoined operation whose valuable effect is all-or-nothing and whose residue is durable journal records.
- The failed-`Put` prepared residue is a known cleanup limitation, not a loss; the live-window exposure and the native-platform limits are unchanged, and this decision neither narrows nor widens ADR 0055.

## Limits

This settles ownership and the no-valuable-data-loss reasoning; it does not change ADR 0055's accepted cancelled-caller exposure, and it resolves only this clause of the durability residual, not the whole valuable-data hold, whose artefact and deployment acceptance evidence (remediation P31 to P35) remains.
