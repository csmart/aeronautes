# Synchronous writer shutdown ownership — 15 September 2026

Finish-line item L08 asks for P30 synchronous writer shutdown ownership, and says to prove the existing runtime guarantee or add only the missing ownership.
The re-scope of 14 September records the item as required for v1, because the held synchronous mutation P30 names is a metadata write and v1 keeps the metadata capability.

This record reproduces the current behaviour.
The guarantee does not hold: none of the item's three claims is true for a synchronous metadata mutation.

## What the shell owns today

`ShutdownWork` in `internal/app/shutdown.go` joins exactly two owners: the job runner and the Find service.
`shellShutdown.wait` in `shutdown.go` runs that drain once and blocks forever if it fails, and `shellShutdown.close` runs the drain, then closes the state database, then releases the instance lock.
`main.go` wires the drain as `app.ShutdownWork(ctx, jobRunner, findService)` and passes `shutdown.wait` as the Wails shutdown hook.

`MutateService` is registered as a service beside the others and is joined by nothing.
It is not part of the drain, and it is not one of the services implementing `ServiceShutdown` — `FindService` is the only one.

## Reproduction

Two probes run the actual `runDesktop` caller with its real services against the mock Swift cluster, so the wiring under test is the shipped wiring rather than a reconstruction.
The first holds a metadata mutation at the read that follows its POST: at that point the operator's metadata has already changed and the audit append has not run, which is the window the hazard names.
The probe and its output are retained in the private packet.

Observed, in one run:

- the drain returned in 3.3 microseconds with the mutation still in flight;
- `runDesktop` returned, so the deferred close ran: the state database was closed and the instance lock released, both while the mutation was still in flight;
- the held mutation then completed, and its audit append failed with `begin write tx: sql: database is closed`;
- the mutation returned a nil error to its caller.

The second probe runs the drain first and then starts a new metadata mutation.
It was admitted, reached the store — `POST /v1/AUTH_test/meta/scope/file` appears in the request log — and returned outcome `applied` with a nil error.

## What that establishes

Each of the item's three claims is false for a synchronous metadata mutation.

A held synchronous mutation does not prevent database or lock release.
Its final record does not prevent them either.
New admission is not closed during the drain.

The existing drain does close admission for the two owners it knows about: `TestShutdownWorkKeepsBothAdmissionsClosed` already proves a cancelled drain leaves both job and Find admission refused and the database open.
That test is unaffected by anything here; the gap is that a third writer exists and is not joined.

The worst of the three is the combination rather than any one of them.
A metadata write can change the operator's data, lose its audit record because the database closed underneath it, and still report success to the caller.
The failed append is written to the process log and to nothing durable.

## What the framework does and does not do

The shell cannot rely on Wails to close this window.
In the pinned version the shutdown sequence cancels the application context, runs the shutdown tasks, and then calls `ServiceShutdown` on services that implement it; nothing anywhere waits for an in-flight service method to return.

The call's context is not the application context.
On the desktop transport the context handed to a service method is the webview request's own context, so cancelling the application context does not cancel an in-flight mutation.
That matters for the correction: the record can still be written during shutdown provided the database is open, so this is an ownership and ordering problem rather than a context problem.

Not established: whether closing the webview window ends that request and cancels the call's context in a live shell.
That needs a real window rather than the mock transport these probes use, and it is recorded as a limit rather than assumed either way.

## Correction

The shell now owns synchronous writers in the shape it already uses for jobs and Find, and joins that owner from the same place.

A small admission gate in `internal/app`: entering it succeeds while the shell is running and fails once shutdown has begun, and closing it refuses further entry and waits for entered work to leave.
`MutateService` enters the gate for the whole of a write, leaving only after its final record, so the record is inside the owned region rather than after it.
`ShutdownWork` joins that gate alongside the job runner and Find, so `wait` covers it and `close` cannot reach the database first.
A refused mutation returns a named sentinel, so the interface can say something true rather than reporting a write it did not attempt.

The delivered v1 claim is the metadata capability.
Every store-reaching `MutateService` method occupies the gate: `UpdateObjectMetadata`, `UpdateContainer`, `CreateContainer`, `CreateMarker`, `DeleteMarker` and `DeleteManifest`.
The last four are not claimed as v1 capabilities; they occupy the gate so a reached write cannot lose its record.

## Limits

This does not establish anything about crash or kill, where no drain runs at all.
It does not establish the webview context question above.
It does not close L08 by itself: P30's wider durable-outcome work and the deferred capabilities' own writers are untouched, and the valuable-data release hold is unchanged.
