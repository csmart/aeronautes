# ADR 0053: synchronous writer shutdown ownership

**Status:** Accepted for the metadata writes v1 keeps, which are delivered with this decision.
It does not address crash or kill, does not establish whether closing the webview window cancels an in-flight call's context, and does not close L08 or the valuable-data hold.

## Context

The shell drains two owners at shutdown, the job runner and the Find service, then closes the state database and releases the instance lock.
A synchronous metadata write is neither, and nothing joins it.

The [shutdown writer ownership record](../experiments/2026-09-15-shutdown-writer-ownership.md) reproduces the consequence against the actual shell wiring.
A metadata write held between its POST and its audit append does not delay the drain, which returned in microseconds; the database is closed and the lock released while the write is still in flight; the append then fails with the database closed; and the call returns a nil error.
A new metadata write started after the drain is admitted, reaches the store and reports success.

So the operator's data can change, the durable record of that change can be lost, and the interface can report success, all in one shutdown.

Wails does not close this window.
Its shutdown cancels the application context, runs the shutdown hooks and then calls `ServiceShutdown` on the services that implement it, and nothing waits for an in-flight service method.
The context a service method receives is the webview request's, not the application's, so an in-flight write is not cancelled by shutdown and its record can still be written while the database is open.

## Decision

Synchronous writers get one owner, joined from the same place as the other two.

An admission gate in `internal/app` admits a writer while the shell is running and refuses once shutdown has begun.
Closing the gate refuses further entry and waits for entered writers to leave.
This is the shape the job runner already uses; it is not a new mechanism.

A metadata write occupies the gate for its whole duration, leaving only after its final record has been written.
The record is therefore inside the owned region, which is the point: ownership that ended at the store write would still lose the record.

`ShutdownWork` joins the gate alongside the job runner and the Find service.
The shell's existing `wait` and `close` then cover synchronous writers without changing their order or their failure behaviour, and a drain that cannot complete continues to block rather than permitting a close.

A refused write returns a named sentinel and performs no store request.
Refusing after changing the operator's data would be worse than either refusing or proceeding.

## Consequences

A metadata write in flight at shutdown delays the close until its record is durable.
A metadata write attempted after shutdown has begun is refused, with a reason the interface can state.
The drain gains a third owner and can therefore block on a writer that does not return; that is the existing and deliberate behaviour for the other two owners, where an unproved drain is not permission to quit.

The claim is scoped to the metadata writes v1 keeps.
The gate is general and the other writes on the mutation service may enter it, but this record does not claim them.

This does not address crash or kill, where no drain runs.
It does not establish whether closing the webview window cancels an in-flight call's context in a live shell, which the record states as an open limit.
P30's wider durable-outcome work, the deferred capabilities' writers and the valuable-data release hold are unchanged, and L08 does not close with this decision.
